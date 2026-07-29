//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package store provides the SQLite-backed ReviewStore implementation.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

var _ ReviewStore = (*SQLiteStore)(nil)

// ErrInvalidTransition is returned when Finalize is called with a non-terminal status.
var ErrInvalidTransition = fmt.Errorf("finalize requires StatusCompleted or StatusCompletedWithWarnings")

// SQLiteStore implements ReviewStore backed by SQLite.
type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewSQLiteStore opens or creates a SQLite database at dsn.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	s := &SQLiteStore{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS review_tasks (
			id TEXT PRIMARY KEY,
			repo_path TEXT DEFAULT '',
			diff_file TEXT DEFAULT '',
			diff_summary TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			dry_run INTEGER NOT NULL DEFAULT 0,
			sandbox_type TEXT DEFAULT 'local',
			total_duration_ms INTEGER NOT NULL DEFAULT 0,
			sandbox_duration_ms INTEGER NOT NULL DEFAULT 0,
			tool_call_count INTEGER NOT NULL DEFAULT 0,
			permission_deny_count INTEGER NOT NULL DEFAULT 0,
			findings_total INTEGER NOT NULL DEFAULT 0,
			findings_critical INTEGER NOT NULL DEFAULT 0,
			findings_high INTEGER NOT NULL DEFAULT 0,
			findings_medium INTEGER NOT NULL DEFAULT 0,
			findings_low INTEGER NOT NULL DEFAULT 0,
			findings_warning INTEGER NOT NULL DEFAULT 0,
			need_human_review_count INTEGER NOT NULL DEFAULT 0,
			error_message TEXT DEFAULT '',
			created_at INTEGER NOT NULL,
			completed_at INTEGER DEFAULT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES review_tasks(id),
			severity TEXT NOT NULL,
			category TEXT NOT NULL,
			file_path TEXT NOT NULL,
			line INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL,
			evidence TEXT DEFAULT '',
			recommendation TEXT DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT 'rule',
			rule_id TEXT DEFAULT '',
			needs_human_review INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sandbox_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES review_tasks(id),
			command TEXT NOT NULL,
			exit_code INTEGER NOT NULL DEFAULT 0,
			stdout TEXT DEFAULT '',
			stderr TEXT DEFAULT '',
			timed_out INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			error TEXT DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS permission_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES review_tasks(id),
			tool_name TEXT NOT NULL,
			action TEXT NOT NULL,
			reason TEXT DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}
	return nil
}

// CreateTask inserts a new review task.
func (s *SQLiteStore) CreateTask(ctx context.Context, task *reviewmodel.ReviewTask) error {
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO review_tasks
			(id, repo_path, diff_file, diff_summary, status, dry_run, sandbox_type, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, redact.String(task.RepoPath), task.DiffFile, task.DiffSummary,
		string(task.Status), boolToInt(task.DryRun), task.SandboxType,
		task.CreatedAt.Unix(),
	)
	return err
}

// SaveFinding persists a single finding.
func (s *SQLiteStore) SaveFinding(ctx context.Context, taskID string, finding *reviewmodel.Finding) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO findings
			(task_id, severity, category, file_path, line, title, evidence, recommendation,
			 confidence, source, rule_id, needs_human_review, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, finding.Severity, finding.Category, finding.FilePath, finding.Line,
		finding.Title, redact.String(finding.Evidence), finding.Recommendation,
		finding.Confidence, finding.Source, finding.RuleID,
		boolToInt(finding.NeedsHumanReview), time.Now().Unix(),
	)
	return err
}

// SaveSandboxRun persists a single sandbox execution record.
func (s *SQLiteStore) SaveSandboxRun(ctx context.Context, taskID string, run *reviewmodel.SandboxRun) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sandbox_runs
			(id, task_id, command, exit_code, stdout, stderr, timed_out, duration_ms, error, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, taskID, run.Command, run.ExitCode,
		redact.String(run.Stdout), redact.String(run.Stderr),
		boolToInt(run.TimedOut), run.DurationMs, run.Error,
		time.Now().Unix(),
	)
	return err
}

// SavePermissionDecision persists a single governance decision.
func (s *SQLiteStore) SavePermissionDecision(ctx context.Context, taskID string, dec *reviewmodel.PermissionDecision) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO permission_decisions
			(task_id, tool_name, action, reason, created_at)
			VALUES (?, ?, ?, ?, ?)`,
		taskID, dec.ToolName, dec.Action, dec.Reason, time.Now().Unix(),
	)
	return err
}

// GetTask retrieves a review task by ID.
func (s *SQLiteStore) GetTask(ctx context.Context, taskID string) (*reviewmodel.ReviewTask, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, repo_path, diff_file, diff_summary, status, dry_run, sandbox_type,
			total_duration_ms, sandbox_duration_ms, tool_call_count, permission_deny_count,
			findings_total, findings_critical, findings_high, findings_medium,
			findings_low, findings_warning, need_human_review_count, error_message,
			created_at, completed_at
			FROM review_tasks WHERE id = ?`, taskID)

	var t task
	var createdAt, completedAt sql.NullInt64
	err := row.Scan(&t.ID, &t.RepoPath, &t.DiffFile, &t.DiffSummary, &t.Status,
		&t.DryRun, &t.SandboxType, &t.TotalDurationMs, &t.SandboxDurationMs,
		&t.ToolCallCount, &t.PermissionDenyCount, &t.FindingsTotal,
		&t.FindingsCritical, &t.FindingsHigh, &t.FindingsMedium,
		&t.FindingsLow, &t.FindingsWarning, &t.NeedHumanReviewCount,
		&t.ErrorMessage, &createdAt, &completedAt)
	if err != nil {
		return nil, err
	}
	task := reviewmodel.ReviewTask{
		ID: t.ID, RepoPath: t.RepoPath, DiffFile: t.DiffFile,
		DiffSummary: t.DiffSummary, Status: reviewmodel.ReviewStatus(t.Status),
		DryRun: t.DryRun, SandboxType: t.SandboxType,
		TotalDurationMs: t.TotalDurationMs, SandboxDurationMs: t.SandboxDurationMs,
		ToolCallCount: t.ToolCallCount, PermissionDenyCount: t.PermissionDenyCount,
		FindingsTotal: t.FindingsTotal, FindingsCritical: t.FindingsCritical,
		FindingsHigh: t.FindingsHigh, FindingsMedium: t.FindingsMedium,
		FindingsLow: t.FindingsLow, FindingsWarning: t.FindingsWarning,
		NeedHumanReviewCount: t.NeedHumanReviewCount, ErrorMessage: t.ErrorMessage,
	}
	if createdAt.Valid {
		task.CreatedAt = time.Unix(createdAt.Int64, 0)
	}
	if completedAt.Valid {
		ct := time.Unix(completedAt.Int64, 0)
		task.CompletedAt = &ct
	}
	return &task, nil
}

// GetFindings retrieves all findings for a task.
func (s *SQLiteStore) GetFindings(ctx context.Context, taskID string) ([]reviewmodel.Finding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT severity, category, file_path, line, title, evidence, recommendation,
			confidence, source, rule_id, needs_human_review
			FROM findings WHERE task_id = ? ORDER BY severity, category, file_path, line`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []reviewmodel.Finding
	for rows.Next() {
		var f reviewmodel.Finding
		var needsHumanReview int
		if err := rows.Scan(&f.Severity, &f.Category, &f.FilePath, &f.Line,
			&f.Title, &f.Evidence, &f.Recommendation, &f.Confidence,
			&f.Source, &f.RuleID, &needsHumanReview); err != nil {
			return nil, err
		}
		f.NeedsHumanReview = needsHumanReview != 0
		findings = append(findings, f)
	}
	return findings, rows.Err()
}

// Finalize marks a task as completed. Status must be StatusCompleted or
// StatusCompletedWithWarnings; any other value returns ErrInvalidTransition.
func (s *SQLiteStore) Finalize(ctx context.Context, taskID string, task *reviewmodel.ReviewTask) error {
	if task.Status != reviewmodel.StatusCompleted &&
		task.Status != reviewmodel.StatusCompletedWithWarnings {
		return ErrInvalidTransition
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE review_tasks SET
			status = ?, total_duration_ms = ?, sandbox_duration_ms = ?,
			tool_call_count = ?, permission_deny_count = ?,
			findings_total = ?, findings_critical = ?, findings_high = ?,
			findings_medium = ?, findings_low = ?, findings_warning = ?,
			need_human_review_count = ?, error_message = ?, completed_at = ?
			WHERE id = ?`,
		string(task.Status), task.TotalDurationMs, task.SandboxDurationMs,
		task.ToolCallCount, task.PermissionDenyCount,
		task.FindingsTotal, task.FindingsCritical, task.FindingsHigh,
		task.FindingsMedium, task.FindingsLow, task.FindingsWarning,
		task.NeedHumanReviewCount, task.ErrorMessage, now, taskID,
	)
	return err
}

// GetSandboxRuns retrieves all sandbox execution records for a task.
func (s *SQLiteStore) GetSandboxRuns(ctx context.Context, taskID string) ([]reviewmodel.SandboxRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, command, exit_code, stdout, stderr, timed_out, duration_ms, error
			FROM sandbox_runs WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []reviewmodel.SandboxRun
	for rows.Next() {
		var r reviewmodel.SandboxRun
		var timedOut int
		if err := rows.Scan(&r.ID, &r.Command, &r.ExitCode,
			&r.Stdout, &r.Stderr, &timedOut, &r.DurationMs, &r.Error); err != nil {
			return nil, err
		}
		r.TimedOut = timedOut != 0
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// GetPermissionDecisions retrieves all governance decisions for a task.
func (s *SQLiteStore) GetPermissionDecisions(ctx context.Context, taskID string) ([]reviewmodel.PermissionDecision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tool_name, action, reason FROM permission_decisions WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var decisions []reviewmodel.PermissionDecision
	for rows.Next() {
		var d reviewmodel.PermissionDecision
		if err := rows.Scan(&d.ToolName, &d.Action, &d.Reason); err != nil {
			return nil, err
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}

// Close releases the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Task represents the raw store row.
type task struct {
	ID                   string
	RepoPath             string
	DiffFile             string
	DiffSummary          string
	Status               string
	DryRun               bool
	SandboxType          string
	TotalDurationMs      int64
	SandboxDurationMs    int64
	ToolCallCount        int
	PermissionDenyCount  int
	FindingsTotal        int
	FindingsCritical     int
	FindingsHigh         int
	FindingsMedium       int
	FindingsLow          int
	FindingsWarning      int
	NeedHumanReviewCount int
	ErrorMessage         string
}

var _ = json.Valid // keep json import
