//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ReviewTask represents a code review task.
type ReviewTask struct {
	ID              string     `json:"id"`
	InputType       string     `json:"input_type"`
	InputPath       string     `json:"input_path"`
	DiffSummary     string     `json:"diff_summary"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	TotalDurationMs int64      `json:"total_duration_ms"`
}

// ReviewReport represents a generated report.
type ReviewReport struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	ReportJSON string    `json:"report_json"`
	ReportMD   string    `json:"report_md"`
	CreatedAt  time.Time `json:"created_at"`
}

// Artifact records a bounded output produced by a review task.
type Artifact struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Name      string    `json:"name"`
	MIMEType  string    `json:"mime_type"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// Storage defines the persistence interface for the code review
// agent. This allows switching SQLite for other SQL backends.
type Storage interface {
	InitSchema(ctx context.Context) error
	SaveTask(ctx context.Context, task *ReviewTask) error
	UpdateTaskStatus(ctx context.Context, taskID, status string, completedAt time.Time, totalDurationMs int64) error
	SaveSandboxRun(ctx context.Context, run *SandboxRun) error
	SavePermissionDecision(ctx context.Context, d *PermissionRecord) error
	SaveFinding(ctx context.Context, taskID string, f *Finding) error
	SaveReport(ctx context.Context, r *ReviewReport) error
	SaveMonitoring(ctx context.Context, m *MonitoringSummary) error
	SaveArtifact(ctx context.Context, artifact *Artifact) error
	GetTask(ctx context.Context, taskID string) (*ReviewTask, error)
	GetFindingsByTask(ctx context.Context, taskID string) ([]Finding, error)
	GetSandboxRunsByTask(ctx context.Context, taskID string) ([]SandboxRun, error)
	GetArtifactsByTask(ctx context.Context, taskID string) ([]Artifact, error)
	Close() error
}

// SQLiteStorage implements Storage using a SQLite database.
type SQLiteStorage struct {
	db *sql.DB
}

// NewSQLiteStorage opens (or creates) a SQLite database at dbPath.
func NewSQLiteStorage(ctx context.Context, dbPath string) (*SQLiteStorage, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &SQLiteStorage{db: db}
	if err := s.InitSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// InitSchema creates all required tables if they don't exist.
func (s *SQLiteStorage) InitSchema(ctx context.Context) error {
	schema := `
CREATE TABLE IF NOT EXISTS review_tasks (
    id TEXT PRIMARY KEY,
    input_type TEXT NOT NULL,
    input_path TEXT NOT NULL,
    diff_summary TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    completed_at TEXT,
    total_duration_ms INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS sandbox_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    command TEXT NOT NULL,
    permission_decision TEXT NOT NULL,
    permission_reason TEXT DEFAULT '',
    status TEXT NOT NULL,
    stdout TEXT DEFAULT '',
    stderr TEXT DEFAULT '',
    exit_code INTEGER DEFAULT 0,
    duration_ms INTEGER DEFAULT 0,
    timed_out INTEGER DEFAULT 0,
    error TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS permission_decisions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    command TEXT NOT NULL,
    decision TEXT NOT NULL,
    reason TEXT DEFAULT '',
    timestamp TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS findings (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    severity TEXT NOT NULL,
    category TEXT NOT NULL,
    file TEXT NOT NULL,
    line INTEGER NOT NULL,
    title TEXT NOT NULL,
    evidence TEXT DEFAULT '',
    recommendation TEXT DEFAULT '',
    confidence REAL DEFAULT 0,
    source TEXT DEFAULT 'rule',
    rule_id TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    name TEXT NOT NULL,
    mime_type TEXT DEFAULT '',
    size INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS review_reports (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    report_json TEXT NOT NULL,
    report_md TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS monitoring_summary (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    total_duration_ms INTEGER DEFAULT 0,
    sandbox_duration_ms INTEGER DEFAULT 0,
    tool_call_count INTEGER DEFAULT 0,
    permission_block_count INTEGER DEFAULT 0,
    finding_count INTEGER DEFAULT 0,
    critical_count INTEGER DEFAULT 0,
    high_count INTEGER DEFAULT 0,
    medium_count INTEGER DEFAULT 0,
    low_count INTEGER DEFAULT 0,
    warning_count INTEGER DEFAULT 0,
    error_types TEXT DEFAULT '{}',
    created_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

CREATE INDEX IF NOT EXISTS idx_findings_task ON findings(task_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task ON sandbox_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_permission_decisions_task ON permission_decisions(task_id);
CREATE INDEX IF NOT EXISTS idx_review_reports_task ON review_reports(task_id);
CREATE INDEX IF NOT EXISTS idx_monitoring_summary_task ON monitoring_summary(task_id);
`
	_, err := s.db.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	return nil
}

// SaveTask inserts or replaces a review task.
func (s *SQLiteStorage) SaveTask(ctx context.Context, task *ReviewTask) error {
	_, err := s.db.ExecContext(ctx, `
INSERT OR REPLACE INTO review_tasks
    (id, input_type, input_path, diff_summary, status, created_at, completed_at, total_duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.InputType, task.InputPath, task.DiffSummary,
		task.Status, task.CreatedAt.Format(time.RFC3339Nano),
		formatTimePtr(task.CompletedAt), task.TotalDurationMs,
	)
	return err
}

// UpdateTaskStatus updates the status and completion info of a task.
func (s *SQLiteStorage) UpdateTaskStatus(
	ctx context.Context, taskID, status string,
	completedAt time.Time, totalDurationMs int64,
) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE review_tasks SET status = ?, completed_at = ?, total_duration_ms = ?
WHERE id = ?`,
		status, completedAt.Format(time.RFC3339Nano),
		totalDurationMs, taskID,
	)
	return err
}

// SaveSandboxRun inserts a sandbox run record.
func (s *SQLiteStorage) SaveSandboxRun(ctx context.Context, run *SandboxRun) error {
	timedOut := 0
	if run.TimedOut {
		timedOut = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sandbox_runs
    (id, task_id, command, permission_decision, permission_reason,
     status, stdout, stderr, exit_code, duration_ms, timed_out,
     error, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, run.Command,
		string(run.PermissionDecision), run.PermissionReason,
		string(run.Status), run.Stdout, run.Stderr, run.ExitCode,
		run.Duration.Milliseconds(), timedOut, run.Error,
		time.Now().Format(time.RFC3339Nano),
	)
	return err
}

// SavePermissionDecision inserts a permission decision record.
func (s *SQLiteStorage) SavePermissionDecision(
	ctx context.Context, d *PermissionRecord,
) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO permission_decisions
    (id, task_id, command, decision, reason, timestamp)
VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.TaskID, d.Command, string(d.Decision),
		d.Reason, d.Timestamp,
	)
	return err
}

// SaveFinding inserts a finding record.
func (s *SQLiteStorage) SaveFinding(
	ctx context.Context, taskID string, f *Finding,
) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO findings
    (id, task_id, severity, category, file, line, title, evidence,
     recommendation, confidence, source, rule_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, taskID, f.Severity, f.Category, f.File, f.Line,
		f.Title, f.Evidence, f.Recommendation, f.Confidence,
		f.Source, f.RuleID, time.Now().Format(time.RFC3339Nano),
	)
	return err
}

// SaveReport inserts a report record.
func (s *SQLiteStorage) SaveReport(ctx context.Context, r *ReviewReport) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO review_reports
    (id, task_id, report_json, report_md, created_at)
VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.ReportJSON, r.ReportMD,
		r.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// SaveArtifact inserts artifact metadata. Artifact bodies remain in the report
// table or bounded output files rather than being duplicated as database blobs.
func (s *SQLiteStorage) SaveArtifact(ctx context.Context, artifact *Artifact) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO artifacts (id, task_id, name, mime_type, size, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, artifact.ID, artifact.TaskID, artifact.Name,
		artifact.MIMEType, artifact.Size, artifact.CreatedAt.Format(time.RFC3339Nano))
	return err
}

// SaveMonitoring inserts a monitoring summary record.
func (s *SQLiteStorage) SaveMonitoring(
	ctx context.Context, m *MonitoringSummary,
) error {
	errorTypesJSON, _ := json.Marshal(m.ErrorTypes)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO monitoring_summary
    (id, task_id, total_duration_ms, sandbox_duration_ms,
     tool_call_count, permission_block_count, finding_count,
     critical_count, high_count, medium_count, low_count,
     warning_count, error_types, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.TaskID, m.TotalDurationMs, m.SandboxDurationMs,
		m.ToolCallCount, m.PermissionBlockCount, m.FindingCount,
		m.SeverityCounts[SeverityCritical],
		m.SeverityCounts[SeverityHigh],
		m.SeverityCounts[SeverityMedium],
		m.SeverityCounts[SeverityLow],
		m.WarningCount, string(errorTypesJSON),
		time.Now().Format(time.RFC3339Nano),
	)
	return err
}

// GetTask retrieves a review task by ID.
func (s *SQLiteStorage) GetTask(ctx context.Context, taskID string) (*ReviewTask, error) {
	var t ReviewTask
	var createdAtStr string
	var completedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT id, input_type, input_path, diff_summary, status,
       created_at, completed_at, total_duration_ms
FROM review_tasks WHERE id = ?`, taskID,
	).Scan(&t.ID, &t.InputType, &t.InputPath, &t.DiffSummary,
		&t.Status, &createdAtStr, &completedAt, &t.TotalDurationMs,
	)
	if err != nil {
		return nil, err
	}
	t.CreatedAt = parseTime(createdAtStr)
	if completedAt.Valid && completedAt.String != "" {
		ct := parseTime(completedAt.String)
		t.CompletedAt = &ct
	}
	return &t, nil
}

// GetFindingsByTask retrieves all findings for a task.
func (s *SQLiteStorage) GetFindingsByTask(ctx context.Context, taskID string) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, severity, category, file, line, title, evidence,
       recommendation, confidence, source, rule_id
FROM findings WHERE task_id = ?
ORDER BY
    CASE severity
        WHEN 'critical' THEN 0
        WHEN 'high' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'low' THEN 3
    END, file, line`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.ID, &f.Severity, &f.Category, &f.File,
			&f.Line, &f.Title, &f.Evidence, &f.Recommendation,
			&f.Confidence, &f.Source, &f.RuleID); err != nil {
			return nil, err
		}
		findings = append(findings, f)
	}
	return findings, rows.Err()
}

// GetSandboxRunsByTask retrieves all sandbox runs for a task.
func (s *SQLiteStorage) GetSandboxRunsByTask(ctx context.Context, taskID string) ([]SandboxRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, command, permission_decision, permission_reason,
       status, stdout, stderr, exit_code, duration_ms, timed_out, error
FROM sandbox_runs WHERE task_id = ?`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []SandboxRun
	for rows.Next() {
		var r SandboxRun
		var timedOut int
		var durationMs int64
		if err := rows.Scan(&r.ID, &r.Command,
			&r.PermissionDecision, &r.PermissionReason,
			&r.Status, &r.Stdout, &r.Stderr, &r.ExitCode,
			&durationMs, &timedOut, &r.Error); err != nil {
			return nil, err
		}
		r.TimedOut = timedOut != 0
		r.Duration = time.Duration(durationMs) * time.Millisecond
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// GetArtifactsByTask retrieves artifact metadata for a task.
func (s *SQLiteStorage) GetArtifactsByTask(ctx context.Context, taskID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, name, mime_type, size, created_at
FROM artifacts WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var artifacts []Artifact
	for rows.Next() {
		var artifact Artifact
		var createdAt string
		if err := rows.Scan(&artifact.ID, &artifact.TaskID, &artifact.Name,
			&artifact.MIMEType, &artifact.Size, &createdAt); err != nil {
			return nil, err
		}
		artifact.CreatedAt = parseTime(createdAt)
		artifacts = append(artifacts, artifact)
	}
	return artifacts, rows.Err()
}

// Close closes the database connection.
func (s *SQLiteStorage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ScanResult holds the result of a scan query for convenience.
type ScanResult struct {
	TaskID      string
	Findings    []Finding
	SandboxRuns []SandboxRun
	Artifacts   []Artifact
}

// ScanTask retrieves all data for a task in one call.
func (s *SQLiteStorage) ScanTask(ctx context.Context, taskID string) (*ScanResult, error) {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	_ = task
	findings, err := s.GetFindingsByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	runs, err := s.GetSandboxRunsByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.GetArtifactsByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return &ScanResult{
		TaskID:      taskID,
		Findings:    findings,
		SandboxRuns: runs,
		Artifacts:   artifacts,
	}, nil
}

// DB returns the underlying database handle (for testing).
func (s *SQLiteStorage) DB() *sql.DB {
	return s.db
}

// Quote ensures a string is safe for SQL (basic; for testing only).
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
