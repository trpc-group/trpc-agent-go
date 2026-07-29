//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package store provides the SQLite implementation of the review store.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

//go:embed schema.sql
var schemaFS embed.FS

var memoryStoreID atomic.Uint64

// SQLiteStore persists reviews in a consumer-owned SQLite database.
type SQLiteStore struct {
	db                   *sql.DB
	afterReadTaskForTest func()
}

var _ review.ReviewStore = (*SQLiteStore)(nil)

// NewSQLiteStore opens path, configures SQLite, and applies the embedded schema.
func NewSQLiteStore(ctx context.Context, path string) (*SQLiteStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}

	dsn, fileDatabase := sqliteDSN(path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite review store: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite review store: %w", err)
	}
	if fileDatabase {
		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("enable sqlite wal: %w", err)
		}
	}
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read sqlite schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(schema)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply sqlite schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Close releases the database resources owned by the store.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// CreateTask persists a queryable task before review work begins.
func (s *SQLiteStore) CreateTask(ctx context.Context, task review.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	task.CreatedAt = task.CreatedAt.UTC()
	task.UpdatedAt = task.UpdatedAt.UTC()
	task.TerminalError = redact.String(task.TerminalError)
	if err := task.Validate(); err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	if task.Status != review.TaskStatusPending || task.Phase != review.PhaseCreated {
		return errors.New("create task requires pending status and created phase")
	}
	stmt, err := s.db.PrepareContext(ctx, `INSERT INTO review_tasks
		(id, schema_version, status, phase, mode, created_at, updated_at, terminal_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare create task: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx, task.ID, task.SchemaVersion, task.Status, task.Phase,
		task.Mode, formatTime(task.CreatedAt), formatTime(task.UpdatedAt), task.TerminalError)
	if err != nil {
		return fmt.Errorf("create task %q: %w", task.ID, err)
	}
	return nil
}

// TransitionPhase advances a non-terminal task to a later running phase.
func (s *SQLiteStore) TransitionPhase(
	ctx context.Context,
	taskID string,
	phase review.Phase,
	updatedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if taskID == "" {
		return errors.New("transition task id is required")
	}
	if phase == review.PhaseCreated || phase == review.PhaseCompleted || phaseRank(phase) < 0 {
		return fmt.Errorf("transition task %q to invalid phase %q", taskID, phase)
	}
	return s.updateTask(ctx, taskID, func(task review.Task) (review.Task, error) {
		if task.Status != review.TaskStatusPending && task.Status != review.TaskStatusRunning {
			return review.Task{}, fmt.Errorf("task status %q is terminal", task.Status)
		}
		if phaseRank(phase) <= phaseRank(task.Phase) {
			return review.Task{}, fmt.Errorf("phase %q does not follow %q", phase, task.Phase)
		}
		if updatedAt.Before(task.UpdatedAt) {
			return review.Task{}, errors.New("updated at precedes current task timestamp")
		}
		task.Status = review.TaskStatusRunning
		task.Phase = phase
		task.UpdatedAt = updatedAt.UTC()
		return task, nil
	})
}

// RecordSandboxRun appends one validated sandbox execution record.
func (s *SQLiteStore) RecordSandboxRun(ctx context.Context, run review.SandboxRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	run = sanitizeSandboxRun(run)
	if err := run.Validate(); err != nil {
		return fmt.Errorf("record sandbox run: %w", err)
	}
	stmt, err := s.db.PrepareContext(ctx, `INSERT INTO sandbox_runs
		(task_id, schema_version, command, status, duration_ns, exit_code,
		 timed_out, stdout, stderr, truncated)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ? FROM review_tasks
		WHERE id = ? AND status IN ('pending', 'running')`)
	if err != nil {
		return fmt.Errorf("prepare sandbox run: %w", err)
	}
	defer stmt.Close()
	result, err := stmt.ExecContext(ctx, run.TaskID, run.SchemaVersion, run.Command, run.Status,
		int64(run.Duration), nullableInt(run.ExitCode), run.TimedOut, run.Stdout, run.Stderr,
		run.Truncated, run.TaskID)
	if err != nil {
		return fmt.Errorf("record sandbox run for task %q: %w", run.TaskID, err)
	}
	return requireInserted(result, "sandbox run", run.TaskID)
}

// RecordGovernanceDecision appends one validated governance decision.
func (s *SQLiteStore) RecordGovernanceDecision(ctx context.Context, decision review.GovernanceDecision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	decision = sanitizeGovernanceDecision(decision)
	if decision.TaskID == "" {
		return errors.New("record governance decision task id is required")
	}
	if err := decision.Validate(); err != nil {
		return fmt.Errorf("record governance decision: %w", err)
	}
	stmt, err := s.db.PrepareContext(ctx, `INSERT INTO governance_decisions
		(task_id, schema_version, decision_id, kind, tool, action, reason, rule)
		SELECT ?, ?, ?, ?, ?, ?, ?, ? FROM review_tasks
		WHERE id = ? AND status IN ('pending', 'running')
		ON CONFLICT(task_id, kind, decision_id) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("prepare governance decision: %w", err)
	}
	defer stmt.Close()
	result, err := stmt.ExecContext(ctx, decision.TaskID, decision.SchemaVersion,
		decision.DecisionID, decision.Kind, decision.Tool, decision.Action, decision.Reason,
		decision.Rule, decision.TaskID)
	if err != nil {
		return fmt.Errorf("record governance decision for task %q: %w", decision.TaskID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("record governance decision rows affected: %w", err)
	}
	if rows == 1 {
		return nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM review_tasks
		WHERE id = ? AND status IN ('pending', 'running')`, decision.TaskID).Scan(&exists); err != nil {
		return fmt.Errorf("check governance decision task: %w", err)
	}
	if exists == 1 {
		return nil
	}
	return fmt.Errorf("governance decision task %q is missing or terminal", decision.TaskID)
}

// FailTask records a terminal failure at the supplied lifecycle phase.
func (s *SQLiteStore) FailTask(
	ctx context.Context,
	taskID string,
	phase review.Phase,
	terminalError string,
	updatedAt time.Time,
) error {
	return s.terminateTask(
		ctx, taskID, phase, review.TaskStatusFailed, terminalError, updatedAt)
}

// CancelTask records terminal cancellation at the supplied lifecycle phase.
func (s *SQLiteStore) CancelTask(
	ctx context.Context,
	taskID string,
	phase review.Phase,
	terminalError string,
	updatedAt time.Time,
) error {
	return s.terminateTask(
		ctx, taskID, phase, review.TaskStatusCanceled, terminalError, updatedAt)
}

func (s *SQLiteStore) terminateTask(
	ctx context.Context,
	taskID string,
	phase review.Phase,
	status review.TaskStatus,
	terminalError string,
	updatedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if taskID == "" {
		return errors.New("fail task id is required")
	}
	terminalError = redact.String(terminalError)
	if terminalError == "" {
		return errors.New("fail task terminal error is required")
	}
	if phase == review.PhaseCompleted || phaseRank(phase) < 0 {
		return fmt.Errorf("fail task %q at invalid phase %q", taskID, phase)
	}
	return s.updateTask(ctx, taskID, func(task review.Task) (review.Task, error) {
		if task.Status != review.TaskStatusPending && task.Status != review.TaskStatusRunning {
			return review.Task{}, fmt.Errorf("task status %q is terminal", task.Status)
		}
		if phaseRank(phase) < phaseRank(task.Phase) {
			return review.Task{}, fmt.Errorf("phase %q precedes %q", phase, task.Phase)
		}
		if updatedAt.Before(task.UpdatedAt) {
			return review.Task{}, errors.New("updated at precedes current task timestamp")
		}
		task.Status = status
		task.Phase = phase
		task.UpdatedAt = updatedAt.UTC()
		task.TerminalError = terminalError
		return task, nil
	})
}

// CompleteTask atomically stores final records and marks the task completed.
func (s *SQLiteStore) CompleteTask(ctx context.Context, completion review.Completion) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, finding := range completion.Findings {
		if redact.String(finding.TaskID) != finding.TaskID ||
			redact.String(finding.File) != finding.File ||
			redact.String(finding.RuleID) != finding.RuleID ||
			redact.String(finding.SemanticAnchor) != finding.SemanticAnchor ||
			redact.String(finding.Fingerprint) != finding.Fingerprint {
			return errors.New("complete task: secret-bearing finding identity")
		}
	}
	completion = sanitizeCompletion(completion)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin complete task: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err := acquireWriteLock(ctx, tx, completion.TaskID); err != nil {
		return fmt.Errorf("lock task for completion: %w", err)
	}

	task, err := loadTask(ctx, tx, completion.TaskID)
	if err != nil {
		return fmt.Errorf("load task for completion: %w", err)
	}
	if task.Status != review.TaskStatusRunning || task.Phase != review.PhaseFinalize {
		return fmt.Errorf("complete task %q requires running finalize state, got %s/%s",
			task.ID, task.Status, task.Phase)
	}
	if completion.UpdatedAt.Before(task.UpdatedAt) {
		return fmt.Errorf("complete task %q updated at precedes current task timestamp", task.ID)
	}
	task.Status = review.TaskStatusCompleted
	task.Phase = review.PhaseCompleted
	task.UpdatedAt = completion.UpdatedAt.UTC()
	task.TerminalError = ""
	if err := task.Validate(); err != nil {
		return fmt.Errorf("complete task %q: %w", task.ID, err)
	}
	runs, err := loadSandboxRuns(ctx, tx, task.ID)
	if err != nil {
		return fmt.Errorf("load sandbox runs for completion: %w", err)
	}
	decisions, err := loadGovernanceDecisions(ctx, tx, task.ID)
	if err != nil {
		return fmt.Errorf("load governance decisions for completion: %w", err)
	}
	report := review.Report{
		SchemaVersion:       review.SchemaVersion,
		Task:                task,
		Input:               completion.Input,
		SandboxRuns:         runs,
		GovernanceDecisions: decisions,
		Findings:            completion.Findings,
		Artifacts:           completion.Artifacts,
		Metrics:             completion.Metrics,
		Conclusion:          completion.Conclusion,
	}
	if completion.TaskID != task.ID {
		return fmt.Errorf("complete task id %q does not match %q", completion.TaskID, task.ID)
	}
	if err := report.Validate(); err != nil {
		return fmt.Errorf("complete task %q: %w", task.ID, err)
	}
	if err := validateReportMetadata(
		completion.Report, task.ID, completion.PublicationArtifacts); err != nil {
		return fmt.Errorf("complete task %q: %w", task.ID, err)
	}

	if err := insertCompletion(ctx, tx, completion); err != nil {
		return err
	}
	if err := writeTask(ctx, tx, task); err != nil {
		return fmt.Errorf("mark task %q completed: %w", task.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit completed task %q: %w", task.ID, err)
	}
	return nil
}

// GetReview reconstructs all records currently persisted for taskID.
func (s *SQLiteStore) GetReview(
	ctx context.Context,
	taskID string,
) (stored review.StoredReview, err error) {
	if err := ctx.Err(); err != nil {
		return review.StoredReview{}, err
	}
	if taskID == "" {
		return review.StoredReview{}, errors.New("get review task id is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return review.StoredReview{}, fmt.Errorf("begin get review %q: %w", taskID, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stored.Report.SchemaVersion = review.SchemaVersion
	stored.Report.Task, err = loadTask(ctx, tx, taskID)
	if err != nil {
		return review.StoredReview{}, fmt.Errorf("get review task %q: %w", taskID, err)
	}
	if s.afterReadTaskForTest != nil {
		s.afterReadTaskForTest()
	}
	stored.Report.SandboxRuns, err = loadSandboxRuns(ctx, tx, taskID)
	if err != nil {
		return review.StoredReview{}, fmt.Errorf("get review sandbox runs: %w", err)
	}
	stored.Report.GovernanceDecisions, err = loadGovernanceDecisions(ctx, tx, taskID)
	if err != nil {
		return review.StoredReview{}, fmt.Errorf("get review governance decisions: %w", err)
	}
	if err := loadCompletion(ctx, tx, taskID, &stored); err != nil {
		return review.StoredReview{}, fmt.Errorf("get review completion: %w", err)
	}
	if stored.Report.Task.Status == review.TaskStatusCompleted {
		if err := stored.Report.Validate(); err != nil {
			return review.StoredReview{}, fmt.Errorf("validate stored review %q: %w", taskID, err)
		}
		if err := validateReportMetadata(
			stored.Metadata, taskID, stored.PublicationArtifacts); err != nil {
			return review.StoredReview{}, fmt.Errorf("validate stored report metadata %q: %w", taskID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return review.StoredReview{}, fmt.Errorf("commit get review %q: %w", taskID, err)
	}
	return stored, nil
}

func (s *SQLiteStore) updateTask(
	ctx context.Context,
	taskID string,
	mutate func(review.Task) (review.Task, error),
) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update task %q: %w", taskID, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err := acquireWriteLock(ctx, tx, taskID); err != nil {
		return fmt.Errorf("lock task %q: %w", taskID, err)
	}
	task, err := loadTask(ctx, tx, taskID)
	if err != nil {
		return fmt.Errorf("load task %q: %w", taskID, err)
	}
	task, err = mutate(task)
	if err != nil {
		return fmt.Errorf("update task %q: %w", taskID, err)
	}
	if err := task.Validate(); err != nil {
		return fmt.Errorf("update task %q: %w", taskID, err)
	}
	if err := writeTask(ctx, tx, task); err != nil {
		return fmt.Errorf("update task %q: %w", taskID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task %q: %w", taskID, err)
	}
	return nil
}

func writeTask(ctx context.Context, tx *sql.Tx, task review.Task) error {
	stmt, err := tx.PrepareContext(ctx, `UPDATE review_tasks
		SET status = ?, phase = ?, updated_at = ?, terminal_error = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	result, err := stmt.ExecContext(ctx, task.Status, task.Phase, formatTime(task.UpdatedAt), task.TerminalError, task.ID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("updated %d rows", rows)
	}
	return nil
}

func insertCompletion(ctx context.Context, tx *sql.Tx, completion review.Completion) error {
	changedFiles, err := json.Marshal(completion.Input.ChangedFiles)
	if err != nil {
		return fmt.Errorf("encode changed files: %w", err)
	}
	if err := execPrepared(ctx, tx, `INSERT INTO review_inputs
		(task_id, schema_version, source, digest, changed_files) VALUES (?, ?, ?, ?, ?)`,
		completion.Input.TaskID, completion.Input.SchemaVersion, completion.Input.Source,
		completion.Input.Digest, string(changedFiles)); err != nil {
		return fmt.Errorf("insert review input: %w", err)
	}
	for index, finding := range completion.Findings {
		if err := execPrepared(ctx, tx, `INSERT INTO findings
			(task_id, schema_version, severity, category, layer, file, line, end_line,
			 semantic_anchor, title, evidence, recommendation, confidence, source, rule_id,
			 fingerprint, disposition)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			finding.TaskID, finding.SchemaVersion, finding.Severity, finding.Category,
			finding.Layer, finding.File, finding.Line, finding.EndLine, finding.SemanticAnchor,
			finding.Title, finding.Evidence, finding.Recommendation, finding.Confidence,
			finding.Source, finding.RuleID, finding.Fingerprint, finding.Disposition); err != nil {
			return fmt.Errorf("insert finding %d: %w", index, err)
		}
	}
	for index, artifact := range completion.Artifacts {
		if err := execPrepared(ctx, tx, `INSERT INTO artifacts
			(task_id, role, schema_version, name, reference, digest, mime_type, size)
			VALUES (?, 'evidence', ?, ?, ?, ?, ?, ?)`, artifact.TaskID, artifact.SchemaVersion,
			artifact.Name, artifact.Reference, artifact.Digest, artifact.MIMEType, artifact.Size); err != nil {
			return fmt.Errorf("insert evidence artifact %d: %w", index, err)
		}
	}
	for index, artifact := range completion.PublicationArtifacts {
		if err := execPrepared(ctx, tx, `INSERT INTO artifacts
			(task_id, role, schema_version, name, reference, digest, mime_type, size)
			VALUES (?, 'publication', ?, ?, ?, ?, ?, ?)`, artifact.TaskID, artifact.SchemaVersion,
			artifact.Name, artifact.Reference, artifact.Digest, artifact.MIMEType, artifact.Size); err != nil {
			return fmt.Errorf("insert publication artifact %d: %w", index, err)
		}
	}
	severityCounts, err := json.Marshal(completion.Metrics.SeverityCounts)
	if err != nil {
		return fmt.Errorf("encode severity counts: %w", err)
	}
	errorTypeCounts, err := json.Marshal(completion.Metrics.ErrorTypeCounts)
	if err != nil {
		return fmt.Errorf("encode error type counts: %w", err)
	}
	if err := execPrepared(ctx, tx, `INSERT INTO review_metrics
		(task_id, schema_version, total_duration_ns, sandbox_duration_ns, tool_invocations,
		 permission_blocks, finding_total, severity_counts, warning_count,
		 human_review_count, error_type_counts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		completion.TaskID, completion.Metrics.SchemaVersion, int64(completion.Metrics.TotalDuration),
		int64(completion.Metrics.SandboxDuration), completion.Metrics.ToolInvocations,
		completion.Metrics.PermissionBlocks, completion.Metrics.FindingTotal, string(severityCounts),
		completion.Metrics.WarningCount, completion.Metrics.HumanReviewCount,
		string(errorTypeCounts)); err != nil {
		return fmt.Errorf("insert review metrics: %w", err)
	}
	if err := execPrepared(ctx, tx, `INSERT INTO review_reports
		(task_id, schema_version, digest, json_artifact_reference,
		 markdown_artifact_reference, conclusion) VALUES (?, ?, ?, ?, ?, ?)`,
		completion.Report.TaskID, completion.Report.SchemaVersion, completion.Report.Digest,
		completion.Report.JSONArtifactReference, completion.Report.MarkdownArtifactReference,
		completion.Conclusion); err != nil {
		return fmt.Errorf("insert review report: %w", err)
	}
	return nil
}

func execPrepared(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx, args...)
	return err
}

func acquireWriteLock(ctx context.Context, tx *sql.Tx, taskID string) error {
	for {
		err := execPrepared(ctx, tx, `UPDATE review_tasks SET id = id WHERE id = ?`, taskID)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isSQLiteLockError(err) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func requireInserted(result sql.Result, record, taskID string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect inserted %s: %w", record, err)
	}
	if rows != 1 {
		return fmt.Errorf("record %s for missing or terminal task %q", record, taskID)
	}
	return nil
}

func loadTask(ctx context.Context, tx *sql.Tx, taskID string) (review.Task, error) {
	stmt, err := tx.PrepareContext(ctx, `SELECT schema_version, id, status, phase, mode,
		created_at, updated_at, terminal_error FROM review_tasks WHERE id = ?`)
	if err != nil {
		return review.Task{}, fmt.Errorf("prepare task query: %w", err)
	}
	defer stmt.Close()
	var task review.Task
	var createdAt, updatedAt string
	err = stmt.QueryRowContext(ctx, taskID).Scan(
		&task.SchemaVersion, &task.ID, &task.Status, &task.Phase, &task.Mode,
		&createdAt, &updatedAt, &task.TerminalError)
	if err != nil {
		return review.Task{}, err
	}
	task.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return review.Task{}, fmt.Errorf("parse created at: %w", err)
	}
	task.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return review.Task{}, fmt.Errorf("parse updated at: %w", err)
	}
	return task, nil
}

func loadSandboxRuns(ctx context.Context, tx *sql.Tx, taskID string) ([]review.SandboxRun, error) {
	stmt, err := tx.PrepareContext(ctx, `SELECT schema_version, task_id, command, status,
		duration_ns, exit_code, timed_out, stdout, stderr, truncated
		FROM sandbox_runs WHERE task_id = ? ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("prepare sandbox run query: %w", err)
	}
	defer stmt.Close()
	rows, err := stmt.QueryContext(ctx, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []review.SandboxRun
	for rows.Next() {
		var run review.SandboxRun
		var duration int64
		var exitCode sql.NullInt64
		if err := rows.Scan(&run.SchemaVersion, &run.TaskID, &run.Command, &run.Status,
			&duration, &exitCode, &run.TimedOut, &run.Stdout, &run.Stderr, &run.Truncated); err != nil {
			return nil, err
		}
		run.Duration = time.Duration(duration)
		if exitCode.Valid {
			value := int(exitCode.Int64)
			run.ExitCode = &value
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func loadGovernanceDecisions(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
) ([]review.GovernanceDecision, error) {
	stmt, err := tx.PrepareContext(ctx, `SELECT schema_version, task_id, decision_id, kind,
		tool, action, reason, rule FROM governance_decisions WHERE task_id = ? ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("prepare governance decision query: %w", err)
	}
	defer stmt.Close()
	rows, err := stmt.QueryContext(ctx, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decisions []review.GovernanceDecision
	for rows.Next() {
		var decision review.GovernanceDecision
		if err := rows.Scan(&decision.SchemaVersion, &decision.TaskID, &decision.DecisionID,
			&decision.Kind, &decision.Tool, &decision.Action, &decision.Reason,
			&decision.Rule); err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}

func loadCompletion(ctx context.Context, tx *sql.Tx, taskID string, stored *review.StoredReview) error {
	stmt, err := tx.PrepareContext(ctx, `SELECT schema_version, task_id, source, digest,
		changed_files FROM review_inputs WHERE task_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare review input query: %w", err)
	}
	defer stmt.Close()
	var changedFiles string
	err = stmt.QueryRowContext(ctx, taskID).Scan(
		&stored.Report.Input.SchemaVersion, &stored.Report.Input.TaskID,
		&stored.Report.Input.Source, &stored.Report.Input.Digest, &changedFiles)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(changedFiles), &stored.Report.Input.ChangedFiles); err != nil {
		return fmt.Errorf("decode changed files: %w", err)
	}
	findings, err := loadFindings(ctx, tx, taskID)
	if err != nil {
		return err
	}
	stored.Report.Findings = findings
	artifacts, err := loadArtifacts(ctx, tx, taskID, "evidence")
	if err != nil {
		return err
	}
	stored.Report.Artifacts = artifacts
	stored.PublicationArtifacts, err = loadArtifacts(ctx, tx, taskID, "publication")
	if err != nil {
		return err
	}
	if err := loadMetrics(ctx, tx, taskID, &stored.Report.Metrics); err != nil {
		return err
	}
	reportStmt, err := tx.PrepareContext(ctx, `SELECT schema_version, task_id, digest,
		json_artifact_reference, markdown_artifact_reference, conclusion
		FROM review_reports WHERE task_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare review report query: %w", err)
	}
	defer reportStmt.Close()
	return reportStmt.QueryRowContext(ctx, taskID).Scan(
		&stored.Metadata.SchemaVersion, &stored.Metadata.TaskID, &stored.Metadata.Digest,
		&stored.Metadata.JSONArtifactReference, &stored.Metadata.MarkdownArtifactReference,
		&stored.Report.Conclusion)
}

func loadFindings(ctx context.Context, tx *sql.Tx, taskID string) ([]review.Finding, error) {
	stmt, err := tx.PrepareContext(ctx, `SELECT schema_version, task_id, severity, category,
		layer, file, line, end_line, semantic_anchor, title, evidence, recommendation,
		confidence, source, rule_id, fingerprint, disposition
		FROM findings WHERE task_id = ? ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("prepare finding query: %w", err)
	}
	defer stmt.Close()
	rows, err := stmt.QueryContext(ctx, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var findings []review.Finding
	for rows.Next() {
		var finding review.Finding
		if err := rows.Scan(&finding.SchemaVersion, &finding.TaskID, &finding.Severity,
			&finding.Category, &finding.Layer, &finding.File, &finding.Line, &finding.EndLine,
			&finding.SemanticAnchor, &finding.Title, &finding.Evidence, &finding.Recommendation,
			&finding.Confidence, &finding.Source, &finding.RuleID, &finding.Fingerprint,
			&finding.Disposition); err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	return findings, rows.Err()
}

func loadArtifacts(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	role string,
) ([]review.ArtifactRecord, error) {
	stmt, err := tx.PrepareContext(ctx, `SELECT schema_version, task_id, name, reference,
		digest, mime_type, size FROM artifacts WHERE task_id = ? AND role = ? ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("prepare artifact query: %w", err)
	}
	defer stmt.Close()
	rows, err := stmt.QueryContext(ctx, taskID, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var artifacts []review.ArtifactRecord
	for rows.Next() {
		var artifact review.ArtifactRecord
		if err := rows.Scan(&artifact.SchemaVersion, &artifact.TaskID, &artifact.Name,
			&artifact.Reference, &artifact.Digest, &artifact.MIMEType, &artifact.Size); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, rows.Err()
}

func loadMetrics(ctx context.Context, tx *sql.Tx, taskID string, metrics *review.Metrics) error {
	stmt, err := tx.PrepareContext(ctx, `SELECT schema_version, total_duration_ns,
		sandbox_duration_ns, tool_invocations, permission_blocks, finding_total,
		severity_counts, warning_count, human_review_count, error_type_counts
		FROM review_metrics WHERE task_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare review metrics query: %w", err)
	}
	defer stmt.Close()
	var totalDuration, sandboxDuration int64
	var severityCounts, errorTypeCounts string
	err = stmt.QueryRowContext(ctx, taskID).Scan(
		&metrics.SchemaVersion, &totalDuration, &sandboxDuration, &metrics.ToolInvocations,
		&metrics.PermissionBlocks, &metrics.FindingTotal, &severityCounts,
		&metrics.WarningCount, &metrics.HumanReviewCount, &errorTypeCounts)
	if err != nil {
		return err
	}
	metrics.TotalDuration = time.Duration(totalDuration)
	metrics.SandboxDuration = time.Duration(sandboxDuration)
	if err := json.Unmarshal([]byte(severityCounts), &metrics.SeverityCounts); err != nil {
		return fmt.Errorf("decode severity counts: %w", err)
	}
	if err := json.Unmarshal([]byte(errorTypeCounts), &metrics.ErrorTypeCounts); err != nil {
		return fmt.Errorf("decode error type counts: %w", err)
	}
	return nil
}

func validateReportMetadata(
	metadata review.ReportMetadata,
	taskID string,
	artifacts []review.ArtifactRecord,
) error {
	if metadata.SchemaVersion != review.SchemaVersion {
		return fmt.Errorf("report metadata schema version %q is invalid", metadata.SchemaVersion)
	}
	if metadata.TaskID != taskID {
		return fmt.Errorf("report metadata task id %q does not match %q", metadata.TaskID, taskID)
	}
	if metadata.Digest == "" || metadata.JSONArtifactReference == "" || metadata.MarkdownArtifactReference == "" {
		return errors.New("report metadata fields are required")
	}
	if metadata.JSONArtifactReference == metadata.MarkdownArtifactReference {
		return errors.New("json and markdown report artifact references must be distinct")
	}
	var jsonArtifacts, markdownArtifacts []review.ArtifactRecord
	for _, artifact := range artifacts {
		switch artifact.Reference {
		case metadata.JSONArtifactReference:
			jsonArtifacts = append(jsonArtifacts, artifact)
		case metadata.MarkdownArtifactReference:
			markdownArtifacts = append(markdownArtifacts, artifact)
		}
	}
	if len(jsonArtifacts) != 1 {
		return errors.New("json report artifact reference is not persisted")
	}
	if len(markdownArtifacts) != 1 {
		return errors.New("markdown report artifact reference is not persisted")
	}
	jsonArtifact := jsonArtifacts[0]
	if jsonArtifact.Name != "review_report.json" {
		return fmt.Errorf("json report artifact name must be review_report.json, got %q", jsonArtifact.Name)
	}
	if jsonArtifact.MIMEType != "application/json" {
		return fmt.Errorf("json report artifact mime type must be application/json, got %q", jsonArtifact.MIMEType)
	}
	if metadata.Digest != jsonArtifact.Digest {
		return errors.New("report digest must equal json artifact digest")
	}
	markdownArtifact := markdownArtifacts[0]
	if markdownArtifact.Name != "review_report.md" {
		return fmt.Errorf("markdown report artifact name must be review_report.md, got %q", markdownArtifact.Name)
	}
	if markdownArtifact.MIMEType != "text/markdown" {
		return fmt.Errorf("markdown report artifact mime type must be text/markdown, got %q", markdownArtifact.MIMEType)
	}
	return nil
}

func sanitizeSandboxRun(run review.SandboxRun) review.SandboxRun {
	run.Command = redact.String(run.Command)
	run.Stdout = redact.String(run.Stdout)
	run.Stderr = redact.String(run.Stderr)
	return run
}

func sanitizeGovernanceDecision(decision review.GovernanceDecision) review.GovernanceDecision {
	decision.Tool = redact.String(decision.Tool)
	decision.Reason = redact.String(decision.Reason)
	decision.Rule = redact.String(decision.Rule)
	return decision
}

func sanitizeCompletion(completion review.Completion) review.Completion {
	completion.Input.Digest = redact.String(completion.Input.Digest)
	completion.Input.ChangedFiles = sanitizeStrings(completion.Input.ChangedFiles)

	completion.Findings = append([]review.Finding(nil), completion.Findings...)
	for index := range completion.Findings {
		finding := &completion.Findings[index]
		finding.Category = redact.String(finding.Category)
		finding.Title = redact.String(finding.Title)
		finding.Evidence = redact.String(finding.Evidence)
		finding.Recommendation = redact.String(finding.Recommendation)
	}

	completion.Artifacts = append([]review.ArtifactRecord(nil), completion.Artifacts...)
	for index := range completion.Artifacts {
		artifact := &completion.Artifacts[index]
		artifact.Name = redact.String(artifact.Name)
		artifact.Reference = redact.String(artifact.Reference)
		artifact.Digest = redact.String(artifact.Digest)
		artifact.MIMEType = redact.String(artifact.MIMEType)
	}
	completion.PublicationArtifacts = append(
		[]review.ArtifactRecord(nil), completion.PublicationArtifacts...)
	for index := range completion.PublicationArtifacts {
		artifact := &completion.PublicationArtifacts[index]
		artifact.Name = redact.String(artifact.Name)
		artifact.Reference = redact.String(artifact.Reference)
		artifact.Digest = redact.String(artifact.Digest)
		artifact.MIMEType = redact.String(artifact.MIMEType)
	}

	completion.Metrics.SeverityCounts = cloneSeverityCounts(completion.Metrics.SeverityCounts)
	completion.Metrics.ErrorTypeCounts = sanitizeCountKeys(completion.Metrics.ErrorTypeCounts)
	completion.Report.Digest = redact.String(completion.Report.Digest)
	completion.Report.JSONArtifactReference = redact.String(completion.Report.JSONArtifactReference)
	completion.Report.MarkdownArtifactReference = redact.String(completion.Report.MarkdownArtifactReference)
	completion.Conclusion = redact.String(completion.Conclusion)
	return completion
}

func sanitizeStrings(values []string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = redact.String(value)
	}
	return result
}

func cloneSeverityCounts(counts map[review.Severity]int) map[review.Severity]int {
	if counts == nil {
		return nil
	}
	result := make(map[review.Severity]int, len(counts))
	for severity, count := range counts {
		result[severity] = count
	}
	return result
}

func sanitizeCountKeys(counts map[string]int) map[string]int {
	if counts == nil {
		return nil
	}
	result := make(map[string]int, len(counts))
	for key, count := range counts {
		result[redact.String(key)] += count
	}
	return result
}

func sqliteDSN(path string) (string, bool) {
	query := url.Values{
		"_busy_timeout": []string{"25"},
		"_foreign_keys": []string{"on"},
	}
	if path == ":memory:" {
		query.Set("cache", "shared")
		query.Set("mode", "memory")
		return fmt.Sprintf("file:review-store-%d?%s", memoryStoreID.Add(1), query.Encode()), false
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}
	return u.String(), true
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func phaseRank(phase review.Phase) int {
	switch phase {
	case review.PhaseCreated:
		return 0
	case review.PhaseInput:
		return 1
	case review.PhaseRules:
		return 2
	case review.PhaseSandbox:
		return 3
	case review.PhaseAssist:
		return 4
	case review.PhaseFinalize:
		return 5
	case review.PhaseCompleted:
		return 6
	default:
		return -1
	}
}
