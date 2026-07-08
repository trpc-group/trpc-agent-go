//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type FindingSeverity string

const (
	SeverityHigh   FindingSeverity = "HIGH"
	SeverityMedium FindingSeverity = "MEDIUM"
	SeverityLow    FindingSeverity = "LOW"
	SeverityReview FindingSeverity = "REVIEW"
)

type FindingCategory string

const (
	CategorySecurity        FindingCategory = "SECURITY"
	CategoryReliability     FindingCategory = "RELIABILITY"
	CategoryPerformance     FindingCategory = "PERFORMANCE"
	CategoryMaintainability FindingCategory = "MAINTAINABILITY"
	CategoryBestPractice    FindingCategory = "BEST_PRACTICE"
)

type Finding struct {
	ID          string
	TaskID      string
	RuleID      string
	Filepath    string
	LineNumber  int
	Column      int
	Severity    FindingSeverity
	Category    FindingCategory
	Message     string
	Evidence    string
	Suggestion  string
	Confidence  float64
	NeedsReview bool
	CreatedAt   time.Time
}

type ReviewTask struct {
	ID          string
	DiffPath    string
	RepoPath    string
	Status      string
	StartedAt   time.Time
	CompletedAt *time.Time
	TotalTimeMs int64
}

type SandboxRun struct {
	ID         string
	TaskID     string
	Command    string
	Output     string
	Error      string
	ExitCode   int
	TimedOut   bool
	DurationMs int64
	CreatedAt  time.Time
}

type PermissionRecord struct {
	ID        string
	TaskID    string
	Command   string
	Action    string
	Reason    string
	CreatedAt time.Time
}

type Report struct {
	ID        string
	TaskID    string
	Content   string
	Format    string
	CreatedAt time.Time
}

type Artifact struct {
	ID          string
	TaskID      string
	Name        string
	Path        string
	ContentType string
	Size        int64
	CreatedAt   time.Time
}

type TelemetryMetrics struct {
	ID                     string
	TaskID                 string
	TotalReviewTimeMs      int64
	SandboxExecutionTimeMs int64
	SandboxExecutions      int
	ToolCalls              int
	PermissionBlocks       int
	TotalFindings          int
	Errors                 int
	TasksCompleted         int
	TasksFailed            int
	FindingsBySeverityJSON string
	CreatedAt              time.Time
}

type Storage interface {
	Init(ctx context.Context) error
	CreateReviewTask(ctx context.Context, task ReviewTask) error
	UpdateReviewTask(ctx context.Context, task ReviewTask) error
	GetReviewTask(ctx context.Context, id string) (*ReviewTask, error)
	CreateFinding(ctx context.Context, finding Finding) error
	GetFindingsByTask(ctx context.Context, taskID string) ([]Finding, error)
	CreateSandboxRun(ctx context.Context, run SandboxRun) error
	GetSandboxRunsByTask(ctx context.Context, taskID string) ([]SandboxRun, error)
	CreatePermissionRecord(ctx context.Context, record PermissionRecord) error
	GetPermissionRecords(ctx context.Context, taskID string) ([]PermissionRecord, error)
	CreateReport(ctx context.Context, report Report) error
	GetReport(ctx context.Context, id string) (*Report, error)
	CreateArtifact(ctx context.Context, artifact Artifact) error
	GetArtifactsByTask(ctx context.Context, taskID string) ([]Artifact, error)
	CreateTelemetryMetrics(ctx context.Context, metrics TelemetryMetrics) error
	GetTelemetryMetricsByTask(ctx context.Context, taskID string) ([]TelemetryMetrics, error)
	Close() error
}

type SQLiteStorage struct {
	db *sql.DB
}

func NewSQLiteStorage(path string) (*SQLiteStorage, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return &SQLiteStorage{db: db}, nil
}

func (s *SQLiteStorage) Init(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS review_task (
			id TEXT PRIMARY KEY,
			diff_path TEXT,
			repo_path TEXT,
			status TEXT,
			started_at INTEGER,
			completed_at INTEGER NULL,
			total_time_ms INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS finding (
			id TEXT PRIMARY KEY,
			task_id TEXT,
			rule_id TEXT,
			filepath TEXT,
			line_number INTEGER,
			column INTEGER,
			severity TEXT,
			category TEXT,
			message TEXT,
			evidence TEXT,
			suggestion TEXT,
			confidence REAL,
			needs_review INTEGER,
			created_at INTEGER,
			FOREIGN KEY(task_id) REFERENCES review_task(id)
		)`,
		`CREATE TABLE IF NOT EXISTS sandbox_run (
			id TEXT PRIMARY KEY,
			task_id TEXT,
			command TEXT,
			output TEXT,
			error TEXT,
			exit_code INTEGER,
			timed_out INTEGER,
			duration_ms INTEGER,
			created_at INTEGER,
			FOREIGN KEY(task_id) REFERENCES review_task(id)
		)`,
		`CREATE TABLE IF NOT EXISTS permission_record (
			id TEXT PRIMARY KEY,
			task_id TEXT,
			command TEXT,
			action TEXT,
			reason TEXT,
			created_at INTEGER,
			FOREIGN KEY(task_id) REFERENCES review_task(id)
		)`,
		`CREATE TABLE IF NOT EXISTS report (
			id TEXT PRIMARY KEY,
			task_id TEXT,
			content TEXT,
			format TEXT,
			created_at INTEGER,
			FOREIGN KEY(task_id) REFERENCES review_task(id)
		)`,
		`CREATE TABLE IF NOT EXISTS artifact (
			id TEXT PRIMARY KEY,
			task_id TEXT,
			name TEXT,
			path TEXT,
			content_type TEXT,
			size INTEGER,
			created_at INTEGER,
			FOREIGN KEY(task_id) REFERENCES review_task(id)
		)`,
		`CREATE TABLE IF NOT EXISTS telemetry_metrics (
			id TEXT PRIMARY KEY,
			task_id TEXT,
			total_review_time_ms INTEGER,
			sandbox_execution_time_ms INTEGER,
			sandbox_executions INTEGER,
			tool_calls INTEGER,
			permission_blocks INTEGER,
			total_findings INTEGER,
			errors INTEGER,
			tasks_completed INTEGER,
			tasks_failed INTEGER,
			findings_by_severity TEXT,
			created_at INTEGER,
			FOREIGN KEY(task_id) REFERENCES review_task(id)
		)`,
	}

	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}

	return nil
}

func (s *SQLiteStorage) CreateReviewTask(ctx context.Context, task ReviewTask) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO review_task (id, diff_path, repo_path, status, started_at, completed_at, total_time_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.DiffPath, task.RepoPath, task.Status, task.StartedAt.UnixNano(),
		toNullInt64(task.CompletedAt), task.TotalTimeMs)
	if err != nil {
		return fmt.Errorf("insert review task: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) UpdateReviewTask(ctx context.Context, task ReviewTask) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE review_task SET diff_path = ?, repo_path = ?, status = ?, 
			started_at = ?, completed_at = ?, total_time_ms = ?
		WHERE id = ?
	`, task.DiffPath, task.RepoPath, task.Status, task.StartedAt.UnixNano(),
		toNullInt64(task.CompletedAt), task.TotalTimeMs, task.ID)
	if err != nil {
		return fmt.Errorf("update review task: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetReviewTask(ctx context.Context, id string) (*ReviewTask, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, diff_path, repo_path, status, started_at, completed_at, total_time_ms
		FROM review_task WHERE id = ?
	`, id)

	var task ReviewTask
	var startedAt int64
	var completedAt sql.NullInt64
	err := row.Scan(&task.ID, &task.DiffPath, &task.RepoPath, &task.Status,
		&startedAt, &completedAt, &task.TotalTimeMs)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("select review task: %w", err)
	}

	task.StartedAt = time.Unix(0, startedAt)
	if completedAt.Valid {
		t := time.Unix(0, completedAt.Int64)
		task.CompletedAt = &t
	}

	return &task, nil
}

func (s *SQLiteStorage) CreateFinding(ctx context.Context, finding Finding) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO finding (id, task_id, rule_id, filepath, line_number, column, 
			severity, category, message, evidence, suggestion, confidence, needs_review, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, finding.ID, finding.TaskID, finding.RuleID, finding.Filepath, finding.LineNumber,
		finding.Column, finding.Severity, finding.Category, finding.Message,
		finding.Evidence, finding.Suggestion, finding.Confidence,
		boolToInt(finding.NeedsReview), finding.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("insert finding: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetFindingsByTask(ctx context.Context, taskID string) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, rule_id, filepath, line_number, column, 
			severity, category, message, evidence, suggestion, confidence, needs_review, created_at
		FROM finding WHERE task_id = ? ORDER BY severity DESC, line_number ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("select findings: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var f Finding
		var createdAt int64
		var needsReview int
		err := rows.Scan(&f.ID, &f.TaskID, &f.RuleID, &f.Filepath, &f.LineNumber,
			&f.Column, &f.Severity, &f.Category, &f.Message, &f.Evidence,
			&f.Suggestion, &f.Confidence, &needsReview, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		f.NeedsReview = needsReview == 1
		f.CreatedAt = time.Unix(0, createdAt)
		findings = append(findings, f)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate findings: %w", err)
	}

	return findings, nil
}

func (s *SQLiteStorage) CreateSandboxRun(ctx context.Context, run SandboxRun) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sandbox_run (id, task_id, command, output, error, exit_code, 
			timed_out, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.TaskID, run.Command, run.Output, run.Error, run.ExitCode,
		boolToInt(run.TimedOut), run.DurationMs, run.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("insert sandbox run: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetSandboxRunsByTask(ctx context.Context, taskID string) ([]SandboxRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, command, output, error, exit_code, timed_out, duration_ms, created_at
		FROM sandbox_run WHERE task_id = ? ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("select sandbox runs: %w", err)
	}
	defer rows.Close()

	var runs []SandboxRun
	for rows.Next() {
		var r SandboxRun
		var createdAt int64
		var timedOut int
		err := rows.Scan(&r.ID, &r.TaskID, &r.Command, &r.Output, &r.Error,
			&r.ExitCode, &timedOut, &r.DurationMs, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan sandbox run: %w", err)
		}
		r.TimedOut = timedOut == 1
		r.CreatedAt = time.Unix(0, createdAt)
		runs = append(runs, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandbox runs: %w", err)
	}

	return runs, nil
}

func (s *SQLiteStorage) CreatePermissionRecord(ctx context.Context, record PermissionRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO permission_record (id, task_id, command, action, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, record.ID, record.TaskID, record.Command, record.Action, record.Reason, record.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("insert permission record: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetPermissionRecords(ctx context.Context, taskID string) ([]PermissionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, command, action, reason, created_at
		FROM permission_record WHERE task_id = ? ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("select permission records: %w", err)
	}
	defer rows.Close()

	var records []PermissionRecord
	for rows.Next() {
		var r PermissionRecord
		var createdAt int64
		err := rows.Scan(&r.ID, &r.TaskID, &r.Command, &r.Action, &r.Reason, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan permission record: %w", err)
		}
		r.CreatedAt = time.Unix(0, createdAt)
		records = append(records, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permission records: %w", err)
	}

	return records, nil
}

func (s *SQLiteStorage) CreateReport(ctx context.Context, report Report) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO report (id, task_id, content, format, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, report.ID, report.TaskID, report.Content, report.Format, report.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("insert report: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetReport(ctx context.Context, id string) (*Report, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, content, format, created_at
		FROM report WHERE id = ?
	`, id)

	var report Report
	var createdAt int64
	err := row.Scan(&report.ID, &report.TaskID, &report.Content, &report.Format, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("select report: %w", err)
	}

	report.CreatedAt = time.Unix(0, createdAt)
	return &report, nil
}

func (s *SQLiteStorage) CreateArtifact(ctx context.Context, artifact Artifact) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO artifact (id, task_id, name, path, content_type, size, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, artifact.ID, artifact.TaskID, artifact.Name, artifact.Path,
		artifact.ContentType, artifact.Size, artifact.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("insert artifact: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetArtifactsByTask(ctx context.Context, taskID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, name, path, content_type, size, created_at
		FROM artifact WHERE task_id = ? ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("select artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []Artifact
	for rows.Next() {
		var a Artifact
		var createdAt int64
		err := rows.Scan(&a.ID, &a.TaskID, &a.Name, &a.Path,
			&a.ContentType, &a.Size, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		a.CreatedAt = time.Unix(0, createdAt)
		artifacts = append(artifacts, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}

	return artifacts, nil
}

func (s *SQLiteStorage) CreateTelemetryMetrics(ctx context.Context, metrics TelemetryMetrics) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO telemetry_metrics (id, task_id, total_review_time_ms, sandbox_execution_time_ms,
			sandbox_executions, tool_calls, permission_blocks, total_findings, errors,
			tasks_completed, tasks_failed, findings_by_severity, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, metrics.ID, metrics.TaskID, metrics.TotalReviewTimeMs, metrics.SandboxExecutionTimeMs,
		metrics.SandboxExecutions, metrics.ToolCalls, metrics.PermissionBlocks,
		metrics.TotalFindings, metrics.Errors, metrics.TasksCompleted, metrics.TasksFailed,
		metrics.FindingsBySeverityJSON, metrics.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("insert telemetry metrics: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetTelemetryMetricsByTask(ctx context.Context, taskID string) ([]TelemetryMetrics, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, total_review_time_ms, sandbox_execution_time_ms,
			sandbox_executions, tool_calls, permission_blocks, total_findings, errors,
			tasks_completed, tasks_failed, findings_by_severity, created_at
		FROM telemetry_metrics WHERE task_id = ? ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("select telemetry metrics: %w", err)
	}
	defer rows.Close()

	var results []TelemetryMetrics
	for rows.Next() {
		var m TelemetryMetrics
		var createdAt int64
		err := rows.Scan(&m.ID, &m.TaskID, &m.TotalReviewTimeMs, &m.SandboxExecutionTimeMs,
			&m.SandboxExecutions, &m.ToolCalls, &m.PermissionBlocks, &m.TotalFindings,
			&m.Errors, &m.TasksCompleted, &m.TasksFailed, &m.FindingsBySeverityJSON, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan telemetry metrics: %w", err)
		}
		m.CreatedAt = time.Unix(0, createdAt)
		results = append(results, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telemetry metrics: %w", err)
	}

	return results, nil
}

func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}

func toNullInt64(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{Valid: false}
	}
	return sql.NullInt64{Int64: t.UnixNano(), Valid: true}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
