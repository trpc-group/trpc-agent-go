//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// rowScanner is the Scan contract shared by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// LoadTaskSnapshot returns the bounded task projection used for report
// generation. Session events and artifact bodies remain in their framework
// services and are referenced by the task key and artifact versions.
func (s *SQLite) LoadTaskSnapshot(
	ctx context.Context,
	taskID string,
) (ReviewSnapshot, error) {
	if err := s.ready(); err != nil {
		return ReviewSnapshot{}, err
	}

	var snapshot ReviewSnapshot
	if err := s.loadTask(ctx, taskID, &snapshot.Task); err != nil {
		return ReviewSnapshot{}, err
	}

	permissions, err := s.loadPermissions(ctx, taskID)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	sandboxRuns, err := s.loadSandboxRuns(ctx, taskID)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	results, err := s.loadResults(ctx, taskID)
	if err != nil {
		return ReviewSnapshot{}, err
	}

	snapshot.PermissionDecisions = permissions
	snapshot.SandboxRuns = sandboxRuns
	snapshot.Results = results
	return snapshot, nil
}

func (s *SQLite) loadTask(
	ctx context.Context,
	taskID string,
	task *ReviewTaskRecord,
) error {
	loaded, err := scanReviewTask(
		s.db.QueryRowContext(ctx, _sqlSelectReviewTask, taskID),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("review task %s does not exist", taskID)
	}
	if err != nil {
		return fmt.Errorf("load review task %s: %w", taskID, err)
	}
	*task = loaded
	return nil
}

func (s *SQLite) loadPermissions(
	ctx context.Context,
	taskID string,
) ([]PermissionDecisionRecord, error) {
	rows, err := s.db.QueryContext(ctx, _sqlSelectPermissionDecisions, taskID)
	if err != nil {
		return nil, fmt.Errorf(
			"load permission decisions for task %s: %w",
			taskID,
			err,
		)
	}
	defer rows.Close()

	var records []PermissionDecisionRecord
	for rows.Next() {
		record, err := scanPermissionDecision(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *SQLite) loadSandboxRuns(
	ctx context.Context,
	taskID string,
) ([]SandboxRunRecord, error) {
	rows, err := s.db.QueryContext(ctx, _sqlSelectSandboxRuns, taskID)
	if err != nil {
		return nil, fmt.Errorf("load sandbox runs for task %s: %w", taskID, err)
	}
	defer rows.Close()

	var records []SandboxRunRecord
	for rows.Next() {
		record, err := scanSandboxRun(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *SQLite) loadResults(
	ctx context.Context,
	taskID string,
) ([]ReviewResultRecord, error) {
	rows, err := s.db.QueryContext(ctx, _sqlSelectReviewResults, taskID)
	if err != nil {
		return nil, fmt.Errorf("load review results for task %s: %w", taskID, err)
	}
	defer rows.Close()

	var records []ReviewResultRecord
	for rows.Next() {
		record, err := scanReviewResult(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func scanReviewTask(scanner rowScanner) (ReviewTaskRecord, error) {
	var (
		task            ReviewTaskRecord
		inputName       sql.NullString
		inputVersion    sql.NullInt64
		conclusion      sql.NullString
		jsonName        sql.NullString
		jsonVersion     sql.NullInt64
		markdownName    sql.NullString
		markdownVersion sql.NullInt64
		startedAt       string
		finishedAt      sql.NullString
		errorType       sql.NullString
		errorMessage    sql.NullString
	)

	// Column order matches _sqlSelectReviewTask.
	if err := scanner.Scan(
		&task.TaskID,
		&task.AppName,
		&task.UserID,
		&task.Status,
		&task.InputKind,
		&task.InputSummaryJSON,
		&inputName,
		&inputVersion,
		&task.MonitoringSummaryJSON,
		&conclusion,
		&jsonName,
		&jsonVersion,
		&markdownName,
		&markdownVersion,
		&startedAt,
		&finishedAt,
		&errorType,
		&errorMessage,
	); err != nil {
		return ReviewTaskRecord{}, err
	}

	task.InputArtifactName = inputName.String
	task.Conclusion = conclusion.String
	task.JSONReportName = jsonName.String
	task.MarkdownReportName = markdownName.String
	task.ErrorType = errorType.String
	task.ErrorMessage = errorMessage.String
	task.InputArtifactVersion = nullableIntPointer(inputVersion)
	task.JSONReportVersion = nullableIntPointer(jsonVersion)
	task.MarkdownReportVersion = nullableIntPointer(markdownVersion)
	task.StartedAt, _ = parseStoredTime(startedAt)
	if finishedAt.Valid {
		task.FinishedAt, _ = parseStoredTime(finishedAt.String)
	}
	return task, nil
}

func scanPermissionDecision(
	scanner rowScanner,
) (PermissionDecisionRecord, error) {
	var (
		record    PermissionDecisionRecord
		decidedAt string
	)

	// Column order matches _sqlSelectPermissionDecisions.
	if err := scanner.Scan(
		&record.ToolCallID,
		&record.DecisionKind,
		&record.Operation,
		&record.ToolName,
		&record.CommandPreview,
		&record.Decision,
		&record.Reason,
		&decidedAt,
	); err != nil {
		return PermissionDecisionRecord{}, err
	}
	record.DecidedAt, _ = parseStoredTime(decidedAt)
	return record, nil
}

func scanSandboxRun(scanner rowScanner) (SandboxRunRecord, error) {
	var (
		record          SandboxRunRecord
		exitCode        sql.NullInt64
		timedOut        int
		outputTruncated int
		durationMS      int64
		startedAt       string
		finishedAt      sql.NullString
	)

	// Column order matches _sqlSelectSandboxRuns.
	if err := scanner.Scan(
		&record.ToolCallID,
		&record.Backend,
		&record.Workdir,
		&record.CommandPreview,
		&record.Status,
		&exitCode,
		&timedOut,
		&record.OutputSummary,
		&outputTruncated,
		&record.RedactionCount,
		&startedAt,
		&finishedAt,
		&durationMS,
		&record.ErrorType,
		&record.ErrorMessage,
	); err != nil {
		return SandboxRunRecord{}, err
	}

	record.ExitCode = nullableIntPointer(exitCode)
	record.TimedOut = timedOut != 0
	record.OutputTruncated = outputTruncated != 0
	record.Duration = time.Duration(durationMS) * time.Millisecond
	record.StartedAt, _ = parseStoredTime(startedAt)
	if finishedAt.Valid {
		record.FinishedAt, _ = parseStoredTime(finishedAt.String)
	}
	return record, nil
}

func scanReviewResult(scanner rowScanner) (ReviewResultRecord, error) {
	var (
		record    ReviewResultRecord
		createdAt string
	)

	// Column order matches _sqlSelectReviewResults.
	if err := scanner.Scan(
		&record.ResultKind,
		&record.Severity,
		&record.Category,
		&record.File,
		&record.Line,
		&record.Title,
		&record.Evidence,
		&record.Recommendation,
		&record.Confidence,
		&record.Source,
		&record.RuleID,
		&createdAt,
	); err != nil {
		return ReviewResultRecord{}, err
	}
	record.CreatedAt, _ = parseStoredTime(createdAt)
	return record, nil
}

func nullableIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}
