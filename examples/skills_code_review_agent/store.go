//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var sqliteSchema string

// ReviewStore is the persistence boundary. SQL backends can implement it
// without changing orchestration or reporting.
type ReviewStore interface {
	SaveReview(
		ctx context.Context,
		report ReviewReport,
		jsonReport []byte,
		markdownReport []byte,
	) error
	GetReview(ctx context.Context, taskID string) (ReviewReport, error)
	GetReport(ctx context.Context, taskID string) ([]byte, []byte, error)
	Close() error
}

// SQLiteStore persists complete review records atomically.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens a SQLite store and applies its idempotent schema.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite path: %w", err)
	}
	db, err := sql.Open(
		"sqlite3",
		fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=on", absolute),
	)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize sqlite schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SaveReview writes a complete review in one transaction.
func (s *SQLiteStore) SaveReview(
	ctx context.Context,
	report ReviewReport,
	jsonReport []byte,
	markdownReport []byte,
) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not initialized")
	}
	report = sanitizeReport(report)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin review transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertReviewTask(ctx, tx, report); err != nil {
		return err
	}
	if err := insertReviewInput(ctx, tx, report); err != nil {
		return err
	}
	if err := insertSandboxRuns(ctx, tx, report); err != nil {
		return err
	}
	if err := insertDecisions(ctx, tx, report); err != nil {
		return err
	}
	for bucket, findings := range map[string][]Finding{
		"finding":     report.Findings,
		"warning":     report.Warnings,
		"needs_human": report.NeedsHumanReview,
	} {
		if err := insertFindings(ctx, tx, report.TaskID, bucket, findings); err != nil {
			return err
		}
	}
	if err := insertArtifacts(ctx, tx, report); err != nil {
		return err
	}
	if err := insertMetrics(ctx, tx, report); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO review_reports(task_id, json_content, markdown_content)
		 VALUES(?, ?, ?)`,
		report.TaskID, Redact(string(jsonReport)), Redact(string(markdownReport)),
	); err != nil {
		return fmt.Errorf("insert report: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit review transaction: %w", err)
	}
	return nil
}

func insertReviewTask(
	ctx context.Context,
	tx *sql.Tx,
	report ReviewReport,
) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO review_tasks
		 (id, status, conclusion, mode, runtime, skill, started_at, completed_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		report.TaskID, report.Status, report.Conclusion, report.Mode,
		report.Runtime, report.Skill, formatTime(report.StartedAt),
		formatTime(report.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("insert review task: %w", err)
	}
	return nil
}

func insertReviewInput(
	ctx context.Context,
	tx *sql.Tx,
	report ReviewReport,
) error {
	changedFiles, err := json.Marshal(report.Input.ChangedFiles)
	if err != nil {
		return fmt.Errorf("marshal changed files: %w", err)
	}
	packages, err := json.Marshal(report.Input.GoPackages)
	if err != nil {
		return fmt.Errorf("marshal go packages: %w", err)
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO review_inputs
		 (task_id, input_kind, sha256, byte_count, changed_files_json,
		  go_packages_json, redacted_preview)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		report.TaskID, report.Input.Kind, report.Input.SHA256,
		report.Input.Bytes, string(changedFiles), string(packages),
		report.Input.RedactedPreview,
	)
	if err != nil {
		return fmt.Errorf("insert review input: %w", err)
	}
	return nil
}

func insertSandboxRuns(
	ctx context.Context,
	tx *sql.Tx,
	report ReviewReport,
) error {
	for _, run := range report.SandboxRuns {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO sandbox_runs
			 (task_id, command, status, exit_code, duration_ms, timed_out,
			  output, error_type, error_message)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			report.TaskID, run.Command, run.Status, run.ExitCode,
			run.DurationMS, run.TimedOut, run.Output,
			run.ErrorType, run.ErrorMessage,
		)
		if err != nil {
			return fmt.Errorf("insert sandbox run: %w", err)
		}
	}
	return nil
}

func insertDecisions(
	ctx context.Context,
	tx *sql.Tx,
	report ReviewReport,
) error {
	for _, decision := range report.Decisions {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO governance_decisions
			 (task_id, tool, command, action, reason, risk, created_at)
			 VALUES(?, ?, ?, ?, ?, ?, ?)`,
			report.TaskID, decision.Tool, decision.Command, decision.Action,
			decision.Reason, decision.Risk, formatTime(decision.CreatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert governance decision: %w", err)
		}
	}
	return nil
}

func insertFindings(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	bucket string,
	findings []Finding,
) error {
	for _, finding := range findings {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO findings
			 (task_id, bucket, severity, category, file_path, line_number,
			  title, evidence, recommendation, confidence, source, rule_id)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			taskID, bucket, finding.Severity, finding.Category, finding.File,
			finding.Line, finding.Title, finding.Evidence,
			finding.Recommendation, finding.Confidence, finding.Source,
			finding.RuleID,
		)
		if err != nil {
			return fmt.Errorf("insert %s: %w", bucket, err)
		}
	}
	return nil
}

func insertArtifacts(
	ctx context.Context,
	tx *sql.Tx,
	report ReviewReport,
) error {
	for _, artifact := range report.Artifacts {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO artifacts(task_id, kind, path, sha256, size_bytes)
			 VALUES(?, ?, ?, ?, ?)`,
			report.TaskID, artifact.Kind, artifact.Path,
			artifact.SHA256, artifact.SizeBytes,
		)
		if err != nil {
			return fmt.Errorf("insert artifact: %w", err)
		}
	}
	return nil
}

func insertMetrics(
	ctx context.Context,
	tx *sql.Tx,
	report ReviewReport,
) error {
	severity, err := json.Marshal(report.Metrics.Severity)
	if err != nil {
		return fmt.Errorf("marshal severity metrics: %w", err)
	}
	errorTypes, err := json.Marshal(report.Metrics.Errors)
	if err != nil {
		return fmt.Errorf("marshal error metrics: %w", err)
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO review_metrics
		 (task_id, total_duration_ms, sandbox_duration_ms, tool_calls,
		  permission_blocked, finding_count, warning_count, severity_json,
		  errors_json)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		report.TaskID, report.Metrics.TotalDurationMS,
		report.Metrics.SandboxDurationMS, report.Metrics.ToolCalls,
		report.Metrics.PermissionBlocked, report.Metrics.FindingCount,
		report.Metrics.WarningCount, string(severity), string(errorTypes),
	)
	if err != nil {
		return fmt.Errorf("insert review metrics: %w", err)
	}
	return nil
}

// GetReview reconstructs all queryable review records by task ID.
func (s *SQLiteStore) GetReview(
	ctx context.Context,
	taskID string,
) (ReviewReport, error) {
	if s == nil || s.db == nil {
		return ReviewReport{}, errors.New("sqlite store is not initialized")
	}
	var report ReviewReport
	var startedAt, completedAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, status, conclusion, mode, runtime, skill,
		        started_at, completed_at
		   FROM review_tasks WHERE id = ?`,
		taskID,
	).Scan(
		&report.TaskID, &report.Status, &report.Conclusion, &report.Mode,
		&report.Runtime, &report.Skill, &startedAt, &completedAt,
	)
	if err != nil {
		return ReviewReport{}, fmt.Errorf("query review task: %w", err)
	}
	report.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return ReviewReport{}, err
	}
	report.CompletedAt, err = parseTime(completedAt)
	if err != nil {
		return ReviewReport{}, err
	}
	if err := s.queryInput(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.querySandboxRuns(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.queryDecisions(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.queryFindings(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.queryArtifacts(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.queryMetrics(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	return report, nil
}

func (s *SQLiteStore) queryInput(
	ctx context.Context,
	taskID string,
	report *ReviewReport,
) error {
	var changedFiles, packages string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT input_kind, sha256, byte_count, changed_files_json,
		        go_packages_json, redacted_preview
		   FROM review_inputs WHERE task_id = ?`,
		taskID,
	).Scan(
		&report.Input.Kind, &report.Input.SHA256, &report.Input.Bytes,
		&changedFiles, &packages, &report.Input.RedactedPreview,
	)
	if err != nil {
		return fmt.Errorf("query review input: %w", err)
	}
	if err := json.Unmarshal([]byte(changedFiles), &report.Input.ChangedFiles); err != nil {
		return fmt.Errorf("decode changed files: %w", err)
	}
	if err := json.Unmarshal([]byte(packages), &report.Input.GoPackages); err != nil {
		return fmt.Errorf("decode go packages: %w", err)
	}
	return nil
}

func (s *SQLiteStore) querySandboxRuns(
	ctx context.Context,
	taskID string,
	report *ReviewReport,
) error {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT command, status, exit_code, duration_ms, timed_out,
		        output, error_type, error_message
		   FROM sandbox_runs WHERE task_id = ? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return fmt.Errorf("query sandbox runs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var run SandboxRun
		if err := rows.Scan(
			&run.Command, &run.Status, &run.ExitCode, &run.DurationMS,
			&run.TimedOut, &run.Output, &run.ErrorType, &run.ErrorMessage,
		); err != nil {
			return fmt.Errorf("scan sandbox run: %w", err)
		}
		report.SandboxRuns = append(report.SandboxRuns, run)
	}
	return rows.Err()
}

func (s *SQLiteStore) queryDecisions(
	ctx context.Context,
	taskID string,
	report *ReviewReport,
) error {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT tool, command, action, reason, risk, created_at
		   FROM governance_decisions WHERE task_id = ? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return fmt.Errorf("query governance decisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var decision PermissionDecision
		var createdAt string
		if err := rows.Scan(
			&decision.Tool, &decision.Command, &decision.Action,
			&decision.Reason, &decision.Risk, &createdAt,
		); err != nil {
			return fmt.Errorf("scan governance decision: %w", err)
		}
		decision.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return err
		}
		report.Decisions = append(report.Decisions, decision)
	}
	return rows.Err()
}

func (s *SQLiteStore) queryFindings(
	ctx context.Context,
	taskID string,
	report *ReviewReport,
) error {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT bucket, severity, category, file_path, line_number,
		        title, evidence, recommendation, confidence, source, rule_id
		   FROM findings WHERE task_id = ? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return fmt.Errorf("query findings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket string
		var finding Finding
		if err := rows.Scan(
			&bucket, &finding.Severity, &finding.Category, &finding.File,
			&finding.Line, &finding.Title, &finding.Evidence,
			&finding.Recommendation, &finding.Confidence, &finding.Source,
			&finding.RuleID,
		); err != nil {
			return fmt.Errorf("scan finding: %w", err)
		}
		switch bucket {
		case "finding":
			report.Findings = append(report.Findings, finding)
		case "warning":
			report.Warnings = append(report.Warnings, finding)
		case "needs_human":
			report.NeedsHumanReview = append(report.NeedsHumanReview, finding)
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) queryArtifacts(
	ctx context.Context,
	taskID string,
	report *ReviewReport,
) error {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT kind, path, sha256, size_bytes
		   FROM artifacts WHERE task_id = ? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var artifact ArtifactRecord
		if err := rows.Scan(
			&artifact.Kind, &artifact.Path, &artifact.SHA256,
			&artifact.SizeBytes,
		); err != nil {
			return fmt.Errorf("scan artifact: %w", err)
		}
		report.Artifacts = append(report.Artifacts, artifact)
	}
	return rows.Err()
}

func (s *SQLiteStore) queryMetrics(
	ctx context.Context,
	taskID string,
	report *ReviewReport,
) error {
	var severity, errorTypes string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT total_duration_ms, sandbox_duration_ms, tool_calls,
		        permission_blocked, finding_count, warning_count,
		        severity_json, errors_json
		   FROM review_metrics WHERE task_id = ?`,
		taskID,
	).Scan(
		&report.Metrics.TotalDurationMS, &report.Metrics.SandboxDurationMS,
		&report.Metrics.ToolCalls, &report.Metrics.PermissionBlocked,
		&report.Metrics.FindingCount, &report.Metrics.WarningCount,
		&severity, &errorTypes,
	)
	if err != nil {
		return fmt.Errorf("query review metrics: %w", err)
	}
	if err := json.Unmarshal([]byte(severity), &report.Metrics.Severity); err != nil {
		return fmt.Errorf("decode severity metrics: %w", err)
	}
	if err := json.Unmarshal([]byte(errorTypes), &report.Metrics.Errors); err != nil {
		return fmt.Errorf("decode error metrics: %w", err)
	}
	return nil
}

// GetReport returns the final redacted JSON and Markdown documents.
func (s *SQLiteStore) GetReport(
	ctx context.Context,
	taskID string,
) ([]byte, []byte, error) {
	var jsonContent, markdownContent string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT json_content, markdown_content
		   FROM review_reports WHERE task_id = ?`,
		taskID,
	).Scan(&jsonContent, &markdownContent)
	if err != nil {
		return nil, nil, fmt.Errorf("query final report: %w", err)
	}
	return []byte(jsonContent), []byte(markdownContent), nil
}

func sanitizeReport(report ReviewReport) ReviewReport {
	report.Input.RedactedPreview = Redact(report.Input.RedactedPreview)
	for i := range report.Findings {
		report.Findings[i] = sanitizeFinding(report.Findings[i])
	}
	for i := range report.Warnings {
		report.Warnings[i] = sanitizeFinding(report.Warnings[i])
	}
	for i := range report.NeedsHumanReview {
		report.NeedsHumanReview[i] = sanitizeFinding(report.NeedsHumanReview[i])
	}
	for i := range report.Decisions {
		report.Decisions[i].Command = Redact(report.Decisions[i].Command)
		report.Decisions[i].Reason = Redact(report.Decisions[i].Reason)
	}
	for i := range report.SandboxRuns {
		report.SandboxRuns[i].Command = Redact(report.SandboxRuns[i].Command)
		report.SandboxRuns[i].Output = Redact(report.SandboxRuns[i].Output)
		report.SandboxRuns[i].ErrorMessage = Redact(
			report.SandboxRuns[i].ErrorMessage,
		)
	}
	return report
}

func sanitizeFinding(finding Finding) Finding {
	finding.Title = Redact(finding.Title)
	finding.Evidence = Redact(finding.Evidence)
	finding.Recommendation = Redact(finding.Recommendation)
	return finding
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time: %w", err)
	}
	return parsed, nil
}
