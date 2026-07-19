// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LoadTaskSnapshot returns the bounded task projection used for report
// generation. Session events and artifact bodies remain in their framework
// services and are referenced by the task key and artifact versions.
func (s *SQLite) LoadTaskSnapshot(ctx context.Context, taskID string) (ReviewSnapshot, error) {
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

func (s *SQLite) loadTask(ctx context.Context, taskID string, task *ReviewTaskRecord) error {
	var inputVersion, jsonVersion, markdownVersion sql.NullInt64
	var inputName, conclusion, jsonName, markdownName sql.NullString
	var finishedAt, errorType, errorMessage sql.NullString
	var startedAt string
	err := s.db.QueryRowContext(ctx, `SELECT task_id, app_name, user_id,
	task_status, input_kind, input_summary_json, input_artifact_name,
	input_artifact_version, monitoring_summary_json, conclusion,
	json_report_name, json_report_version, markdown_report_name,
	markdown_report_version, started_at, finished_at, error_type, error_message
FROM review_tasks WHERE task_id = ?`, taskID).Scan(
		&task.TaskID, &task.AppName, &task.UserID, &task.Status, &task.InputKind,
		&task.InputSummaryJSON, &inputName, &inputVersion,
		&task.MonitoringSummaryJSON, &conclusion, &jsonName, &jsonVersion,
		&markdownName, &markdownVersion, &startedAt, &finishedAt,
		&errorType, &errorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("review task %s does not exist", taskID)
	}
	if err != nil {
		return fmt.Errorf("load review task %s: %w", taskID, err)
	}
	task.InputArtifactName, task.Conclusion = inputName.String, conclusion.String
	task.JSONReportName, task.MarkdownReportName = jsonName.String, markdownName.String
	task.ErrorType, task.ErrorMessage = errorType.String, errorMessage.String
	task.InputArtifactVersion = nullableIntPointer(inputVersion)
	task.JSONReportVersion = nullableIntPointer(jsonVersion)
	task.MarkdownReportVersion = nullableIntPointer(markdownVersion)
	task.StartedAt, _ = parseStoredTime(startedAt)
	if finishedAt.Valid {
		task.FinishedAt, _ = parseStoredTime(finishedAt.String)
	}
	return nil
}

func (s *SQLite) loadPermissions(ctx context.Context, taskID string) ([]PermissionDecisionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(tool_call_id, ''),
	decision_kind, operation, COALESCE(tool_name, ''), COALESCE(command_preview, ''),
	decision, COALESCE(reason, ''), decided_at FROM permission_decisions
WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("load permission decisions for task %s: %w", taskID, err)
	}
	defer rows.Close()
	var records []PermissionDecisionRecord
	for rows.Next() {
		var record PermissionDecisionRecord
		var decidedAt string
		if err := rows.Scan(&record.ToolCallID, &record.DecisionKind, &record.Operation,
			&record.ToolName, &record.CommandPreview, &record.Decision, &record.Reason,
			&decidedAt); err != nil {
			return nil, err
		}
		record.DecidedAt, _ = parseStoredTime(decidedAt)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *SQLite) loadSandboxRuns(ctx context.Context, taskID string) ([]SandboxRunRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(tool_call_id, ''), backend,
	COALESCE(workdir, ''), command_preview, env_allowlist_json, timeout_ms,
	output_limit_bytes, artifact_limit_bytes, sandbox_status, exit_code, timed_out,
	COALESCE(stdout_summary, ''), COALESCE(stderr_summary, ''), stdout_truncated,
	stderr_truncated, redaction_count, started_at, finished_at, duration_ms,
	COALESCE(error_type, ''), COALESCE(error_message, '') FROM sandbox_runs
WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("load sandbox runs for task %s: %w", taskID, err)
	}
	defer rows.Close()
	var records []SandboxRunRecord
	for rows.Next() {
		var record SandboxRunRecord
		var exitCode sql.NullInt64
		var timedOut, stdoutTruncated, stderrTruncated int
		var timeoutMS, durationMS int64
		var startedAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&record.ToolCallID, &record.Backend, &record.Workdir,
			&record.CommandPreview, &record.EnvAllowlistJSON, &timeoutMS,
			&record.OutputLimitBytes, &record.ArtifactLimitBytes, &record.Status,
			&exitCode, &timedOut, &record.StdoutSummary, &record.StderrSummary,
			&stdoutTruncated, &stderrTruncated, &record.RedactionCount,
			&startedAt, &finishedAt, &durationMS, &record.ErrorType,
			&record.ErrorMessage); err != nil {
			return nil, err
		}
		record.ExitCode = nullableIntPointer(exitCode)
		record.TimedOut = timedOut != 0
		record.StdoutTruncated, record.StderrTruncated = stdoutTruncated != 0, stderrTruncated != 0
		record.Timeout, record.Duration = time.Duration(timeoutMS)*time.Millisecond, time.Duration(durationMS)*time.Millisecond
		record.StartedAt, _ = parseStoredTime(startedAt)
		if finishedAt.Valid {
			record.FinishedAt, _ = parseStoredTime(finishedAt.String)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *SQLite) loadResults(ctx context.Context, taskID string) ([]ReviewResultRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT result_kind, severity, category,
	file_path, line, title, evidence, COALESCE(recommendation, ''), confidence,
	source, rule_id, created_at FROM review_results WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("load review results for task %s: %w", taskID, err)
	}
	defer rows.Close()
	var records []ReviewResultRecord
	for rows.Next() {
		var record ReviewResultRecord
		var createdAt string
		if err := rows.Scan(&record.ResultKind, &record.Severity, &record.Category,
			&record.File, &record.Line, &record.Title, &record.Evidence,
			&record.Recommendation, &record.Confidence, &record.Source,
			&record.RuleID, &createdAt); err != nil {
			return nil, err
		}
		record.CreatedAt, _ = parseStoredTime(createdAt)
		records = append(records, record)
	}
	return records, rows.Err()
}

func nullableIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}
