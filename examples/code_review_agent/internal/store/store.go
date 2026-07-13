//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package store persists task-scoped code review projections.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ReviewTaskRecord is the lifecycle root and the lookup bridge to the complete
// framework Session key.
type ReviewTaskRecord struct {
	TaskID                string
	AppName               string
	UserID                string
	Status                string
	InputKind             string
	InputSummaryJSON      string
	InputArtifactName     string
	InputArtifactVersion  *int
	MonitoringSummaryJSON string
	Conclusion            string
	JSONReportName        string
	JSONReportVersion     *int
	MarkdownReportName    string
	MarkdownReportVersion *int
	StartedAt             time.Time
	FinishedAt            time.Time
	ErrorType             string
	ErrorMessage          string
}

// TaskInputRecord is the input projection produced after parsing, masking, and
// saving the complete masked diff artifact. Keeping this update separate from
// SaveTask prevents input preparation from accidentally overwriting lifecycle
// fields that were established when the task started.
type TaskInputRecord struct {
	InputKind            string
	InputSummaryJSON     string
	InputArtifactName    string
	InputArtifactVersion int
}

// PermissionDecisionRecord records a system governance decision made before
// an operation executes.
type PermissionDecisionRecord struct {
	ToolCallID     string
	DecisionKind   string
	Operation      string
	ToolName       string
	CommandPreview string
	Decision       string
	Reason         string
	DecidedAt      time.Time
}

// SandboxRunRecord records facts observed by the governed execution wrapper.
type SandboxRunRecord struct {
	ToolCallID         string
	Backend            string
	Workdir            string
	CommandPreview     string
	EnvAllowlistJSON   string
	Timeout            time.Duration
	OutputLimitBytes   int64
	ArtifactLimitBytes int64
	Status             string
	ExitCode           *int
	TimedOut           bool
	StdoutSummary      string
	StderrSummary      string
	StdoutTruncated    bool
	StderrTruncated    bool
	RedactionCount     int
	StartedAt          time.Time
	FinishedAt         time.Time
	Duration           time.Duration
	ErrorType          string
	ErrorMessage       string
}

// ReviewResultRecord is a finding, warning, or human-review item submitted by
// the Agent through submit_review_results.
type ReviewResultRecord struct {
	ResultKind     string
	Severity       string
	Category       string
	File           string
	Line           int
	Title          string
	Evidence       string
	Recommendation string
	Confidence     float64
	Source         string
	RuleID         string
	CreatedAt      time.Time
}

// SQLiteStore persists task-scoped review projections.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLite creates a review store using an initialized caller-owned database.
func NewSQLite(db *sql.DB) (store *SQLiteStore, err error) {
	if db == nil {
		return nil, errors.New("sqlite database is required")
	}
	return &SQLiteStore{db: db}, nil
}

// SaveTask creates or replaces the lifecycle projection for a review task.
func (s *SQLiteStore) SaveTask(ctx context.Context, task ReviewTaskRecord) error {
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
		task.TaskID, task.AppName, task.UserID, emptyDefault(task.Status, "running"),
		emptyDefault(task.InputKind, "manual"), jsonDefault(task.InputSummaryJSON, "{}"),
		nullableString(task.InputArtifactName), nullableInt(task.InputArtifactVersion),
		jsonDefault(task.MonitoringSummaryJSON, "{}"), nullableString(task.Conclusion),
		nullableString(task.JSONReportName), nullableInt(task.JSONReportVersion),
		nullableString(task.MarkdownReportName), nullableInt(task.MarkdownReportVersion),
		formatTime(task.StartedAt), nullableTime(task.FinishedAt),
		nullableString(task.ErrorType), nullableString(task.ErrorMessage),
	)
	if err != nil {
		return fmt.Errorf("save review task %s: %w", task.TaskID, err)
	}
	return nil
}

// UpdateTaskConclusion records the Agent's final structured conclusion.
func (s *SQLiteStore) UpdateTaskConclusion(ctx context.Context, taskID, conclusion string) error {
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

// UpdateTaskInput records the durable projection of prepared review input.
func (s *SQLiteStore) UpdateTaskInput(ctx context.Context, taskID string, input TaskInputRecord) error {
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
func (s *SQLiteStore) FinishTask(ctx context.Context, taskID string, runErr error) error {
	if err := s.ready(); err != nil {
		return err
	}
	status := "completed"
	errorType := ""
	errorMessage := ""
	if runErr != nil {
		status = "failed"
		errorType = fmt.Sprintf("%T", runErr)
		errorMessage = runErr.Error()
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE review_tasks SET task_status = ?, finished_at = ?, error_type = ?,
	error_message = ?, updated_at = CURRENT_TIMESTAMP WHERE task_id = ?`,
		status, formatTime(time.Now()), nullableString(errorType),
		nullableString(errorMessage), taskID)
	if err != nil {
		return fmt.Errorf("finish review task %s: %w", taskID, err)
	}
	return requireUpdatedTask(result, taskID)
}

// SavePermissionDecision records a governance decision made before execution.
func (s *SQLiteStore) SavePermissionDecision(ctx context.Context, taskID string, decision PermissionDecisionRecord) error {
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
func (s *SQLiteStore) SaveSandboxRun(ctx context.Context, taskID string, run SandboxRunRecord) error {
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

// SaveReviewResult persists one deduplicated Agent review result.
func (s *SQLiteStore) SaveReviewResult(ctx context.Context, taskID string, result ReviewResultRecord) error {
	if err := s.ready(); err != nil {
		return err
	}
	if taskID == "" {
		return errors.New("review task id is required")
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now()
	}
	dedupeKey := resultDedupeKey(result)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO review_results (
	task_id, result_kind, severity, category, file_path, line, title, evidence,
	recommendation, confidence, source, rule_id, dedupe_key, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, taskID,
		emptyDefault(result.ResultKind, "finding"), emptyDefault(result.Severity, "medium"),
		emptyDefault(result.Category, "general"), result.File, result.Line, result.Title,
		result.Evidence, nullableString(result.Recommendation), result.Confidence,
		emptyDefault(result.Source, "agent"), emptyDefault(result.RuleID, "agent"),
		dedupeKey, formatTime(result.CreatedAt))
	if err != nil {
		return fmt.Errorf("save review result for task %s: %w", taskID, err)
	}
	return nil
}

func (s *SQLiteStore) ready() error {
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

func resultDedupeKey(result ReviewResultRecord) string {
	payload := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", result.Category, result.File, result.Line, result.Title, result.RuleID)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
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
