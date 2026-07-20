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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store persists review tasks and audit records.
type Store struct {
	db   *sql.DB
	path string
}

// ReviewStore is the persistence boundary for alternate SQL backends.
type ReviewStore interface {
	// Close releases any backend resources.
	Close() error
	// SaveReport persists a complete report.
	SaveReport(context.Context, ReviewReport, string, string) error
	// LoadReport loads a full report by task id.
	LoadReport(context.Context, string) (ReviewReport, error)
	// LoadLatestTaskIDByDiffHash loads the latest task id for a diff hash.
	LoadLatestTaskIDByDiffHash(context.Context, string) (string, error)
	// LoadTask loads a task by id.
	LoadTask(context.Context, string) (ReviewTask, error)
	// LoadSandboxRuns loads sandbox runs by task id.
	LoadSandboxRuns(context.Context, string) ([]SandboxRun, error)
	// LoadPermissionDecisions loads permission decisions by task id.
	LoadPermissionDecisions(context.Context, string) ([]PermissionRecord, error)
	// LoadFilterDecisions loads filter decisions by task id.
	LoadFilterDecisions(context.Context, string) ([]FilterRecord, error)
	// LoadFindings loads findings by task id and bucket.
	LoadFindings(context.Context, string, string, int, int) ([]Finding, error)
	// LoadArtifacts loads artifact metadata by task id.
	LoadArtifacts(context.Context, string) ([]ArtifactRecord, error)
	// LoadMetrics loads metrics by task id.
	LoadMetrics(context.Context, string) (Metrics, error)
}

// OpenStore opens a SQLite review store and applies the schema migration.
func OpenStore(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("empty db path")
	}
	if err := ensurePrivateSQLitePath(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, path: path}
	if err := s.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := hardenSQLitePermissions(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init(ctx context.Context) error {
	stmts := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS review_tasks (
			id TEXT PRIMARY KEY,
			input_kind TEXT NOT NULL,
			diff_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			completed_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS review_inputs (
			task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
			diff_hash TEXT NOT NULL,
			file_count INTEGER NOT NULL,
			line_count INTEGER NOT NULL,
			summary_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS review_task_aliases (
			diff_hash TEXT PRIMARY KEY,
			latest_task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sandbox_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
			runtime TEXT NOT NULL,
			command TEXT NOT NULL,
			status TEXT NOT NULL,
			exit_code INTEGER NOT NULL,
			output TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			timed_out INTEGER NOT NULL,
			truncated INTEGER NOT NULL,
			error_type TEXT NOT NULL,
			started_at TEXT NOT NULL,
			completed_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS permission_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
			tool_name TEXT NOT NULL,
			command TEXT NOT NULL,
			action TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS filter_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
			filter TEXT NOT NULL,
			action TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
			bucket TEXT NOT NULL,
			severity TEXT NOT NULL,
			category TEXT NOT NULL,
			file TEXT NOT NULL,
			line INTEGER NOT NULL,
			title TEXT NOT NULL,
			evidence TEXT NOT NULL,
			recommendation TEXT NOT NULL,
			confidence REAL NOT NULL,
			source TEXT NOT NULL,
			rule_id TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS reports (
			task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
			json_path TEXT NOT NULL,
			md_path TEXT NOT NULL,
			report_json TEXT NOT NULL,
			conclusion TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS metrics (
			task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
			metrics_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task ON sandbox_runs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_review_tasks_diff_hash ON review_tasks(diff_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_permission_task ON permission_decisions(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_filter_task ON filter_decisions(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_findings_task ON findings(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_findings_lookup ON findings(task_id, file, line, category, rule_id)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_task ON artifacts(task_id)`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, ?);`,
	}
	for i, stmt := range stmts {
		var err error
		if i == len(stmts)-1 {
			_, err = s.db.ExecContext(ctx, stmt, time.Now().UTC().Format(time.RFC3339Nano))
		} else {
			_, err = s.db.ExecContext(ctx, stmt)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// SaveReport persists a complete review report and all child audit records.
func (s *Store) SaveReport(ctx context.Context, report ReviewReport, jsonPath string, mdPath string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO review_tasks(id, input_kind, diff_hash, status, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		report.Task.ID, report.Task.InputKind, report.Task.DiffHash, report.Task.Status,
		formatTime(report.Task.StartedAt), formatTime(report.Task.CompletedAt),
	); err != nil {
		return err
	}
	inputJSON, err := json.Marshal(redactDiffSummary(report.Input))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO review_inputs(task_id, diff_hash, file_count, line_count, summary_json)
		VALUES (?, ?, ?, ?, ?)`,
		report.Task.ID, report.Input.Hash, len(report.Input.Files), report.Input.LineCount, string(inputJSON),
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO review_task_aliases(diff_hash, latest_task_id, updated_at)
		VALUES (?, ?, ?)`,
		report.Task.DiffHash, report.Task.ID, formatTime(time.Now().UTC()),
	); err != nil {
		return err
	}
	for _, run := range report.SandboxRuns {
		run = redactRun(run)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sandbox_runs(task_id, runtime, command, status, exit_code, output, duration_ms, timed_out, truncated, error_type, started_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.TaskID, run.Runtime, run.Command, run.Status, run.ExitCode, run.Output,
			run.Duration.Milliseconds(), boolInt(run.TimedOut), boolInt(run.Truncated), run.ErrorType,
			formatTime(run.StartedAt), formatTime(run.CompletedAt),
		); err != nil {
			return err
		}
	}
	if err := insertPermissionDecisions(ctx, tx, report.PermissionSummary); err != nil {
		return err
	}
	if err := insertFilterDecisions(ctx, tx, report.FilterSummary); err != nil {
		return err
	}
	if err := insertFindings(ctx, tx, report.Task.ID, "finding", report.Findings); err != nil {
		return err
	}
	if err := insertFindings(ctx, tx, report.Task.ID, "warning", report.Warnings); err != nil {
		return err
	}
	if err := insertFindings(ctx, tx, report.Task.ID, "needs_human_review", report.NeedsHumanReview); err != nil {
		return err
	}
	if err := insertArtifacts(ctx, tx, report.Artifacts); err != nil {
		return err
	}
	metricsJSON, err := json.Marshal(report.Metrics)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO metrics(task_id, metrics_json, created_at) VALUES (?, ?, ?)`,
		report.Task.ID, string(metricsJSON), formatTime(time.Now().UTC()),
	); err != nil {
		return err
	}
	reportJSON, err := json.Marshal(redactReviewReport(report))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO reports(task_id, json_path, md_path, report_json, conclusion, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		report.Task.ID, jsonPath, mdPath, string(reportJSON), RedactSecrets(report.Conclusion), formatTime(time.Now().UTC()),
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return hardenSQLitePermissions(s.path)
}

func insertFindings(ctx context.Context, tx *sql.Tx, taskID string, bucket string, findings []Finding) error {
	for _, f := range findings {
		f = redactFinding(f)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO findings(task_id, bucket, severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			taskID, bucket, f.Severity, f.Category, f.File, f.Line, f.Title, f.Evidence,
			f.Recommendation, f.Confidence, f.Source, f.RuleID,
		); err != nil {
			return err
		}
	}
	return nil
}

func insertPermissionDecisions(ctx context.Context, tx *sql.Tx, decisions []PermissionRecord) error {
	for _, decision := range decisions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO permission_decisions(task_id, tool_name, command, action, reason, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			decision.TaskID, decision.ToolName, decision.Command, decision.Action, decision.Reason, formatTime(decision.CreatedAt),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertFilterDecisions(ctx context.Context, tx *sql.Tx, decisions []FilterRecord) error {
	for _, decision := range decisions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO filter_decisions(task_id, filter, action, reason, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			decision.TaskID, decision.Filter, decision.Action, decision.Reason, formatTime(decision.CreatedAt),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertArtifacts(ctx context.Context, tx *sql.Tx, artifacts []ArtifactRecord) error {
	for _, artifact := range artifacts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO artifacts(task_id, name, path, mime_type, size_bytes, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			artifact.TaskID, artifact.Name, artifact.Path, artifact.MIMEType, artifact.SizeBytes, formatTime(artifact.CreatedAt),
		); err != nil {
			return err
		}
	}
	return nil
}

// LoadReport returns the persisted full review report for a task.
func (s *Store) LoadReport(ctx context.Context, taskID string) (ReviewReport, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT report_json FROM reports WHERE task_id = ?`, taskID).Scan(&raw); err != nil {
		return ReviewReport{}, err
	}
	var report ReviewReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return ReviewReport{}, err
	}
	return report, nil
}

// LoadLatestTaskIDByDiffHash returns the newest task id recorded for a diff hash.
func (s *Store) LoadLatestTaskIDByDiffHash(ctx context.Context, diffHash string) (string, error) {
	var taskID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT latest_task_id FROM review_task_aliases WHERE diff_hash = ?`,
		diffHash,
	).Scan(&taskID); err != nil {
		return "", err
	}
	return taskID, nil
}

// LoadTask returns the persisted review task by id.
func (s *Store) LoadTask(ctx context.Context, taskID string) (ReviewTask, error) {
	var task ReviewTask
	var startedAt, completedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, input_kind, diff_hash, status, started_at, completed_at FROM review_tasks WHERE id = ?`,
		taskID,
	).Scan(&task.ID, &task.InputKind, &task.DiffHash, &task.Status, &startedAt, &completedAt)
	if err != nil {
		return ReviewTask{}, err
	}
	task.StartedAt = parseTime(startedAt)
	task.CompletedAt = parseTime(completedAt)
	return task, nil
}

// LoadSandboxRuns returns sandbox executions for a task.
func (s *Store) LoadSandboxRuns(ctx context.Context, taskID string) ([]SandboxRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, runtime, command, status, exit_code, output, duration_ms, timed_out, truncated, error_type, started_at, completed_at
		FROM sandbox_runs WHERE task_id = ? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SandboxRun{}
	for rows.Next() {
		var run SandboxRun
		var durationMS int64
		var timedOut, truncated int
		var startedAt, completedAt string
		if err := rows.Scan(
			&run.TaskID, &run.Runtime, &run.Command, &run.Status,
			&run.ExitCode, &run.Output, &durationMS, &timedOut,
			&truncated, &run.ErrorType, &startedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		run.Duration = time.Duration(durationMS) * time.Millisecond
		run.TimedOut = timedOut != 0
		run.Truncated = truncated != 0
		run.StartedAt = parseTime(startedAt)
		run.CompletedAt = parseTime(completedAt)
		out = append(out, run)
	}
	return out, rows.Err()
}

// LoadPermissionDecisions returns governance decisions for a task.
func (s *Store) LoadPermissionDecisions(ctx context.Context, taskID string) ([]PermissionRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, tool_name, command, action, reason, created_at
		FROM permission_decisions WHERE task_id = ? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PermissionRecord{}
	for rows.Next() {
		var rec PermissionRecord
		var createdAt string
		if err := rows.Scan(&rec.TaskID, &rec.ToolName, &rec.Command, &rec.Action, &rec.Reason, &createdAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// LoadFilterDecisions returns deterministic filter decisions for a task.
func (s *Store) LoadFilterDecisions(ctx context.Context, taskID string) ([]FilterRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, filter, action, reason, created_at
		FROM filter_decisions WHERE task_id = ? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FilterRecord{}
	for rows.Next() {
		var rec FilterRecord
		var createdAt string
		if err := rows.Scan(&rec.TaskID, &rec.Filter, &rec.Action, &rec.Reason, &createdAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// LoadFindings returns findings by task and bucket with pagination.
func (s *Store) LoadFindings(ctx context.Context, taskID string, bucket string, limit int, offset int) ([]Finding, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id
		FROM findings WHERE task_id = ? AND bucket = ? ORDER BY id LIMIT ? OFFSET ?`,
		taskID, bucket, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Finding{}
	for rows.Next() {
		var f Finding
		if err := rows.Scan(
			&f.Severity, &f.Category, &f.File, &f.Line, &f.Title,
			&f.Evidence, &f.Recommendation, &f.Confidence, &f.Source,
			&f.RuleID,
		); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// LoadArtifacts returns persisted artifact metadata for a task.
func (s *Store) LoadArtifacts(ctx context.Context, taskID string) ([]ArtifactRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, name, path, mime_type, size_bytes, created_at
		FROM artifacts WHERE task_id = ? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArtifactRecord{}
	for rows.Next() {
		var rec ArtifactRecord
		var createdAt string
		if err := rows.Scan(&rec.TaskID, &rec.Name, &rec.Path, &rec.MIMEType, &rec.SizeBytes, &createdAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// LoadMetrics returns the review metrics summary for a task.
func (s *Store) LoadMetrics(ctx context.Context, taskID string) (Metrics, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT metrics_json FROM metrics WHERE task_id = ?`, taskID).Scan(&raw); err != nil {
		return Metrics{}, err
	}
	var metrics Metrics
	if err := json.Unmarshal([]byte(raw), &metrics); err != nil {
		return Metrics{}, err
	}
	if metrics.SeverityCounts == nil {
		metrics.SeverityCounts = map[string]int{}
	}
	if metrics.ErrorCounts == nil {
		metrics.ErrorCounts = map[string]int{}
	}
	return metrics, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func ensurePrivateSQLitePath(path string) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

func hardenSQLitePermissions(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
