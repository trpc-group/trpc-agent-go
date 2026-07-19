//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package store persists task-scoped code review projections.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SQLite persists task-scoped review projections.
type SQLite struct {
	db *sql.DB
}

// NewSQLite creates a review store using an initialized caller-owned database.
func NewSQLite(db *sql.DB) (store *SQLite, err error) {
	if db == nil {
		return nil, errors.New("sqlite database is required")
	}
	return &SQLite{db: db}, nil
}

// SaveTask creates or replaces the lifecycle projection for a review task.
func (s *SQLite) SaveTask(ctx context.Context, task ReviewTaskRecord) error {
	if err := s.ready(); err != nil {
		return err
	}
	if task.TaskID == "" || task.AppName == "" || task.UserID == "" {
		return errors.New("review task requires task id, app name, and user id")
	}
	if task.StartedAt.IsZero() {
		task.StartedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO review_tasks (
	task_id, app_name, user_id, task_status, input_kind, input_summary_json,
	input_artifact_name, input_artifact_version, monitoring_summary_json,
	conclusion, json_report_name, json_report_version, markdown_report_name,
	markdown_report_version, started_at, finished_at, error_type, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
	app_name = excluded.app_name, user_id = excluded.user_id,
	task_status = excluded.task_status, input_kind = excluded.input_kind,
	input_summary_json = excluded.input_summary_json,
	input_artifact_name = excluded.input_artifact_name,
	input_artifact_version = excluded.input_artifact_version,
	monitoring_summary_json = excluded.monitoring_summary_json,
	conclusion = excluded.conclusion, json_report_name = excluded.json_report_name,
	json_report_version = excluded.json_report_version,
	markdown_report_name = excluded.markdown_report_name,
	markdown_report_version = excluded.markdown_report_version,
	started_at = excluded.started_at, finished_at = excluded.finished_at,
	error_type = excluded.error_type, error_message = excluded.error_message,
	updated_at = CURRENT_TIMESTAMP`,
		task.TaskID,
		task.AppName,
		task.UserID,
		emptyDefault(task.Status, "running"),
		emptyDefault(task.InputKind, "manual"),
		jsonDefault(task.InputSummaryJSON, "{}"),
		nullableString(task.InputArtifactName),
		nullableInt(task.InputArtifactVersion),
		jsonDefault(task.MonitoringSummaryJSON, "{}"),
		nullableString(task.Conclusion),
		nullableString(task.JSONReportName),
		nullableInt(task.JSONReportVersion),
		nullableString(task.MarkdownReportName),
		nullableInt(task.MarkdownReportVersion),
		formatTime(task.StartedAt),
		nullableTime(task.FinishedAt),
		nullableString(task.ErrorType),
		nullableString(task.ErrorMessage),
	)
	if err != nil {
		return fmt.Errorf("save review task %s: %w", task.TaskID, err)
	}
	return nil
}

// UpdateTaskConclusion records the Agent's final structured conclusion.
func (s *SQLite) UpdateTaskConclusion(ctx context.Context, taskID, conclusion string) error {
	if err := s.ready(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE review_tasks SET conclusion = ?, updated_at = CURRENT_TIMESTAMP
WHERE task_id = ?`, nullableString(conclusion), taskID)
	if err != nil {
		return fmt.Errorf("update review task conclusion %s: %w", taskID, err)
	}
	return requireUpdatedTask(result, taskID)
}

// UpdateTaskMonitoring records the bounded JSON monitoring summary.
func (s *SQLite) UpdateTaskMonitoring(ctx context.Context, taskID, summaryJSON string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !json.Valid([]byte(summaryJSON)) {
		return errors.New("monitoring summary must be valid JSON")
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE review_tasks SET monitoring_summary_json = ?, updated_at = CURRENT_TIMESTAMP
WHERE task_id = ?`, summaryJSON, taskID)
	if err != nil {
		return fmt.Errorf("update review task monitoring %s: %w", taskID, err)
	}
	return requireUpdatedTask(result, taskID)
}

// UpdateTaskReports stores artifact references without copying report content
// into the Review Store.
func (s *SQLite) UpdateTaskReports(ctx context.Context, taskID string, refs ReportReferences) error {
	if err := s.ready(); err != nil {
		return err
	}
	if refs.JSONName == "" || refs.MarkdownName == "" {
		return errors.New("JSON and Markdown report names are required")
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE review_tasks SET json_report_name = ?, json_report_version = ?,
	markdown_report_name = ?, markdown_report_version = ?, updated_at = CURRENT_TIMESTAMP
WHERE task_id = ?`, refs.JSONName, refs.JSONVersion, refs.MarkdownName,
		refs.MarkdownVersion, taskID)
	if err != nil {
		return fmt.Errorf("update review task reports %s: %w", taskID, err)
	}
	return requireUpdatedTask(result, taskID)
}

// UpdateTaskInput records the durable projection of prepared review input.
func (s *SQLite) UpdateTaskInput(ctx context.Context, taskID string, input TaskInputRecord) error {
	if err := s.ready(); err != nil {
		return err
	}
	if taskID == "" || input.InputKind == "" || input.InputArtifactName == "" {
		return errors.New("task input requires task id, input kind, and artifact name")
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE review_tasks SET input_kind = ?, input_summary_json = ?,
	input_artifact_name = ?, input_artifact_version = ?,
	updated_at = CURRENT_TIMESTAMP WHERE task_id = ?`,
		input.InputKind, jsonDefault(input.InputSummaryJSON, "{}"),
		input.InputArtifactName, input.InputArtifactVersion, taskID)
	if err != nil {
		return fmt.Errorf("update review task input %s: %w", taskID, err)
	}
	return requireUpdatedTask(result, taskID)
}

// FinishTask records the terminal task status and any orchestration error.
func (s *SQLite) FinishTask(ctx context.Context, taskID string, runErr error) error {
	status := "completed"
	errorType := ""
	errorMessage := ""
	if runErr != nil {
		status = "failed"
		errorType = fmt.Sprintf("%T", runErr)
		errorMessage = runErr.Error()
	}
	return s.FinalizeTask(ctx, taskID, TaskFinalization{
		Status: status, MonitoringSummaryJSON: "{}",
		ErrorType: errorType, ErrorMessage: errorMessage,
	})
}

// FinalizeTask atomically publishes the complete terminal task projection.
func (s *SQLite) FinalizeTask(
	ctx context.Context,
	taskID string,
	finalization TaskFinalization,
) error {
	if err := s.ready(); err != nil {
		return err
	}
	if taskID == "" {
		return errors.New("review task id is required")
	}
	switch finalization.Status {
	case "completed", "failed", "canceled":
	default:
		return fmt.Errorf("invalid terminal review task status %q", finalization.Status)
	}
	if !json.Valid([]byte(finalization.MonitoringSummaryJSON)) {
		return errors.New("monitoring summary must be valid JSON")
	}
	if finalization.FinishedAt.IsZero() {
		finalization.FinishedAt = time.Now()
	}
	var jsonName, markdownName any
	var jsonVersion, markdownVersion any
	if finalization.Reports != nil {
		if finalization.Reports.JSONName == "" || finalization.Reports.MarkdownName == "" {
			return errors.New("JSON and Markdown report names are required")
		}
		jsonName, jsonVersion = finalization.Reports.JSONName, finalization.Reports.JSONVersion
		markdownName, markdownVersion = finalization.Reports.MarkdownName, finalization.Reports.MarkdownVersion
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE review_tasks SET task_status = ?, monitoring_summary_json = ?,
	json_report_name = ?, json_report_version = ?, markdown_report_name = ?,
	markdown_report_version = ?, finished_at = ?, error_type = ?,
	error_message = ?, updated_at = CURRENT_TIMESTAMP WHERE task_id = ?`,
		finalization.Status, finalization.MonitoringSummaryJSON,
		jsonName, jsonVersion, markdownName, markdownVersion,
		formatTime(finalization.FinishedAt), nullableString(finalization.ErrorType),
		nullableString(finalization.ErrorMessage), taskID)
	if err != nil {
		return fmt.Errorf("finalize review task %s: %w", taskID, err)
	}
	return requireUpdatedTask(result, taskID)
}

// SavePermissionDecision records a governance decision made before execution.
func (s *SQLite) SavePermissionDecision(ctx context.Context, taskID string, decision PermissionDecisionRecord) error {
	if err := s.ready(); err != nil {
		return err
	}
	if taskID == "" {
		return errors.New("review task id is required")
	}
	if decision.DecidedAt.IsZero() {
		decision.DecidedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO permission_decisions (
	task_id, tool_call_id, decision_kind, operation, tool_name,
	command_preview, decision, reason, decided_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, taskID, nullableString(decision.ToolCallID),
		emptyDefault(decision.DecisionKind, "tool_permission"),
		emptyDefault(decision.Operation, "tool_call"), nullableString(decision.ToolName),
		nullableString(decision.CommandPreview), emptyDefault(decision.Decision, "allow"),
		nullableString(decision.Reason), formatTime(decision.DecidedAt))
	if err != nil {
		return fmt.Errorf("save permission decision for task %s: %w", taskID, err)
	}
	return nil
}

// SaveSandboxRun records one governed workspace execution attempt.
func (s *SQLite) SaveSandboxRun(ctx context.Context, taskID string, run SandboxRunRecord) error {
	if err := s.ready(); err != nil {
		return err
	}
	if taskID == "" {
		return errors.New("review task id is required")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sandbox_runs (
	task_id, tool_call_id, backend, workdir, command_preview, env_allowlist_json,
	timeout_ms, output_limit_bytes, artifact_limit_bytes, sandbox_status,
	exit_code, timed_out, stdout_summary, stderr_summary, stdout_truncated,
	stderr_truncated, redaction_count, started_at, finished_at, duration_ms,
	error_type, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, nullableString(run.ToolCallID), emptyDefault(run.Backend, "unknown"),
		nullableString(run.Workdir), run.CommandPreview, jsonDefault(run.EnvAllowlistJSON, "[]"),
		durationMillis(run.Timeout), run.OutputLimitBytes, run.ArtifactLimitBytes,
		emptyDefault(run.Status, "succeeded"), nullableInt(run.ExitCode), boolInt(run.TimedOut),
		nullableString(run.StdoutSummary), nullableString(run.StderrSummary),
		boolInt(run.StdoutTruncated), boolInt(run.StderrTruncated), run.RedactionCount,
		formatTime(run.StartedAt), nullableTime(run.FinishedAt), durationMillis(run.Duration),
		nullableString(run.ErrorType), nullableString(run.ErrorMessage))
	if err != nil {
		return fmt.Errorf("save sandbox run for task %s: %w", taskID, err)
	}
	return nil
}

// SubmitReviewResults atomically replaces the task's complete structured result
// projection. The caller supplies the canonical final arrays for one complete
// submit_review_results invocation; framework Session events retain the tool
// call history separately.
func (s *SQLite) SubmitReviewResults(
	ctx context.Context,
	taskID string,
	results []ReviewResultRecord,
	conclusion string,
) (ReviewResultCounts, error) {
	if err := s.ready(); err != nil {
		return ReviewResultCounts{}, err
	}
	if taskID == "" {
		return ReviewResultCounts{}, errors.New("review task id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewResultCounts{}, fmt.Errorf(
			"begin review submission for task %s: %w",
			taskID,
			err,
		)
	}
	defer func() { _ = tx.Rollback() }()

	// Acquire the SQLite write transaction and reject an unknown task before
	// replacing any projection rows.
	confirmed, err := tx.ExecContext(ctx, `
UPDATE review_tasks SET updated_at = updated_at WHERE task_id = ?`, taskID)
	if err != nil {
		return ReviewResultCounts{}, fmt.Errorf(
			"confirm review task %s: %w",
			taskID,
			err,
		)
	}
	if err := requireUpdatedTask(confirmed, taskID); err != nil {
		return ReviewResultCounts{}, err
	}
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM review_results WHERE task_id = ?`,
		taskID,
	); err != nil {
		return ReviewResultCounts{}, fmt.Errorf(
			"replace review results for task %s: %w",
			taskID,
			err,
		)
	}

	var counts ReviewResultCounts
	for _, result := range results {
		if err := insertReviewResult(ctx, tx, taskID, result); err != nil {
			return ReviewResultCounts{}, err
		}
		switch result.ResultKind {
		case "finding":
			counts.FindingCount++
		case "warning":
			counts.WarningCount++
		case "needs_human_review":
			counts.HumanReviewCount++
		default:
			return ReviewResultCounts{}, fmt.Errorf(
				"save review result for task %s: unsupported result kind %q",
				taskID,
				result.ResultKind,
			)
		}
	}

	updated, err := tx.ExecContext(ctx, `
UPDATE review_tasks SET conclusion = ?, updated_at = CURRENT_TIMESTAMP
WHERE task_id = ?`, conclusion, taskID)
	if err != nil {
		return ReviewResultCounts{}, fmt.Errorf(
			"update review task conclusion %s: %w",
			taskID,
			err,
		)
	}
	if err := requireUpdatedTask(updated, taskID); err != nil {
		return ReviewResultCounts{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReviewResultCounts{}, fmt.Errorf(
			"commit review submission for task %s: %w",
			taskID,
			err,
		)
	}
	return counts, nil
}

func insertReviewResult(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	result ReviewResultRecord,
) error {
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now()
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO review_results (
	task_id, result_kind, severity, category, file_path, line, title, evidence,
	recommendation, confidence, source, rule_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID,
		result.ResultKind, result.Severity, result.Category, result.File,
		result.Line, result.Title,
		result.Evidence, nullableString(result.Recommendation), result.Confidence,
		result.Source, result.RuleID, formatTime(result.CreatedAt))
	if err != nil {
		return fmt.Errorf("save review result for task %s: %w", taskID, err)
	}
	return nil
}

func (s *SQLite) ready() error {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not initialized")
	}
	return nil
}

func emptyDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func jsonDefault(value, fallback string) string {
	if value == "" || !json.Valid([]byte(value)) {
		return fallback
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTime(value time.Time) string        { return value.UTC().Format(time.RFC3339Nano) }
func durationMillis(value time.Duration) int64 { return value.Milliseconds() }

func parseStoredTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func requireUpdatedTask(result sql.Result, taskID string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check review task update %s: %w", taskID, err)
	}
	if rows == 0 {
		return fmt.Errorf("review task %s does not exist", taskID)
	}
	return nil
}
