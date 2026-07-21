//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package store persists review tasks and audit data.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// SQLiteStore stores review data in SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// Open opens and initializes the SQLite database.
func Open(ctx context.Context, path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	s := &SQLiteStore{db: db}
	if err := s.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// CreateTask inserts a new task.
func (s *SQLiteStore) CreateTask(ctx context.Context, task review.ReviewTask) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO review_tasks(id, status, input_type, input_summary, repo_path, started_at, error)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Status, task.InputType, redaction.RedactText(task.InputSummary),
		task.RepoPath, task.StartedAt.Format(time.RFC3339Nano), redaction.RedactText(task.Error))
	return err
}

// FinishTask marks a task as completed or failed.
func (s *SQLiteStore) FinishTask(ctx context.Context, task review.ReviewTask) error {
	finished := ""
	if task.FinishedAt != nil {
		finished = task.FinishedAt.Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE review_tasks SET status = ?, finished_at = ?, error = ? WHERE id = ?`,
		task.Status, finished, redaction.RedactText(task.Error), task.ID)
	return err
}

// SaveFindings stores findings for a task.
func (s *SQLiteStore) SaveFindings(ctx context.Context, taskID string, findings []review.Finding) error {
	for _, f := range findings {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO review_findings(task_id, severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			taskID, f.Severity, f.Category, f.File, f.Line, redaction.RedactText(f.Title),
			redaction.RedactText(f.Evidence), redaction.RedactText(f.Recommendation),
			f.Confidence, f.Source, f.RuleID); err != nil {
			return err
		}
	}
	return nil
}

// SaveSandboxRuns stores sandbox runs.
func (s *SQLiteStore) SaveSandboxRuns(ctx context.Context, taskID string, runs []review.SandboxRun) error {
	for _, run := range runs {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO sandbox_runs(task_id, command, status, exit_code, duration_ms, stdout_excerpt, stderr_excerpt, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			taskID, redaction.RedactText(run.Command), run.Status, run.ExitCode, run.DurationMS,
			redaction.RedactText(run.StdoutExcerpt), redaction.RedactText(run.StderrExcerpt),
			redaction.RedactText(run.Error)); err != nil {
			return err
		}
	}
	return nil
}

// SavePermissionDecisions stores command governance decisions.
func (s *SQLiteStore) SavePermissionDecisions(ctx context.Context, taskID string, decisions []review.PermissionDecision) error {
	for _, d := range decisions {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO permission_decisions(task_id, command, decision, reason, created_at)
VALUES (?, ?, ?, ?, ?)`,
			taskID, redaction.RedactText(d.Command), d.Decision, redaction.RedactText(d.Reason),
			d.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

// SaveFilterDecisions stores noise-control filter decisions.
func (s *SQLiteStore) SaveFilterDecisions(ctx context.Context, taskID string, decisions []review.FilterDecision) error {
	for _, d := range decisions {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO filter_decisions(task_id, rule_id, file, line, source, confidence, stage, decision, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			taskID, d.RuleID, d.File, d.Line, d.Source, d.Confidence,
			d.Stage, d.Decision, redaction.RedactText(d.Reason),
			d.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

// SaveArtifacts stores artifacts.
func (s *SQLiteStore) SaveArtifacts(ctx context.Context, taskID string, artifacts []review.Artifact) error {
	for _, a := range artifacts {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO artifacts(task_id, kind, path, sha256, size_bytes)
VALUES (?, ?, ?, ?, ?)`,
			taskID, a.Kind, redaction.RedactText(a.Path), a.SHA256, a.SizeBytes); err != nil {
			return err
		}
	}
	return nil
}

// SaveReport stores the final report metadata and summary.
func (s *SQLiteStore) SaveReport(ctx context.Context, taskID string, report review.ReviewReport, jsonPath, markdownPath string) error {
	summary, err := json.Marshal(report.Metrics)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO review_reports(task_id, json_path, markdown_path, summary_json)
VALUES (?, ?, ?, ?)`,
		taskID, redaction.RedactText(jsonPath), redaction.RedactText(markdownPath), redaction.RedactText(string(summary)))
	return err
}

// CountFindings returns the number of stored findings for tests and demos.
func (s *SQLiteStore) CountFindings(ctx context.Context, taskID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM review_findings WHERE task_id = ?`, taskID).Scan(&n)
	return n, err
}

// GetTask returns a full persisted snapshot for one task ID.
func (s *SQLiteStore) GetTask(ctx context.Context, taskID string) (review.TaskSnapshot, error) {
	var snap review.TaskSnapshot
	var started string
	var finished sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT id, status, input_type, input_summary, repo_path, started_at, finished_at, error
FROM review_tasks WHERE id = ?`, taskID).Scan(
		&snap.Task.ID, &snap.Task.Status, &snap.Task.InputType,
		&snap.Task.InputSummary, &snap.Task.RepoPath, &started, &finished,
		&snap.Task.Error,
	)
	if err != nil {
		return snap, err
	}
	if t, err := time.Parse(time.RFC3339Nano, started); err == nil {
		snap.Task.StartedAt = t
	}
	if finished.Valid && finished.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, finished.String); err == nil {
			snap.Task.FinishedAt = &t
		}
	}
	findings, err := s.getFindings(ctx, taskID)
	if err != nil {
		return snap, err
	}
	snap.Findings = findings
	runs, err := s.getSandboxRuns(ctx, taskID)
	if err != nil {
		return snap, err
	}
	snap.SandboxRuns = runs
	decisions, err := s.getPermissionDecisions(ctx, taskID)
	if err != nil {
		return snap, err
	}
	snap.PermissionDecisions = decisions
	filters, err := s.getFilterDecisions(ctx, taskID)
	if err != nil {
		return snap, err
	}
	snap.FilterDecisions = filters
	artifacts, err := s.getArtifacts(ctx, taskID)
	if err != nil {
		return snap, err
	}
	snap.Artifacts = artifacts
	_ = s.db.QueryRowContext(ctx, `
SELECT json_path, markdown_path, summary_json FROM review_reports WHERE task_id = ?`,
		taskID).Scan(&snap.Report.JSONPath, &snap.Report.MarkdownPath, &snap.Report.SummaryJSON)
	if snap.Findings == nil {
		snap.Findings = []review.Finding{}
	}
	if snap.SandboxRuns == nil {
		snap.SandboxRuns = []review.SandboxRun{}
	}
	if snap.PermissionDecisions == nil {
		snap.PermissionDecisions = []review.PermissionDecision{}
	}
	if snap.FilterDecisions == nil {
		snap.FilterDecisions = []review.FilterDecision{}
	}
	if snap.Artifacts == nil {
		snap.Artifacts = []review.Artifact{}
	}
	return snap, nil
}

// init creates the SQLite schema when it does not exist yet.
func (s *SQLiteStore) init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS review_tasks (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			input_type TEXT NOT NULL,
			input_summary TEXT NOT NULL,
			repo_path TEXT,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			error TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS review_findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
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
		);`,
		`CREATE TABLE IF NOT EXISTS sandbox_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			command TEXT NOT NULL,
			status TEXT NOT NULL,
			exit_code INTEGER,
			duration_ms INTEGER,
			stdout_excerpt TEXT,
			stderr_excerpt TEXT,
			error TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS permission_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			command TEXT NOT NULL,
			decision TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS filter_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			rule_id TEXT NOT NULL,
			file TEXT NOT NULL,
			line INTEGER NOT NULL,
			source TEXT NOT NULL,
			confidence REAL NOT NULL,
			stage TEXT NOT NULL,
			decision TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS review_reports (
			task_id TEXT PRIMARY KEY,
			json_path TEXT NOT NULL,
			markdown_path TEXT NOT NULL,
			summary_json TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			path TEXT NOT NULL,
			sha256 TEXT,
			size_bytes INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_review_findings_task ON review_findings(task_id);`,
		`CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task ON sandbox_runs(task_id);`,
		`CREATE INDEX IF NOT EXISTS idx_permission_decisions_task ON permission_decisions(task_id);`,
		`CREATE INDEX IF NOT EXISTS idx_filter_decisions_task ON filter_decisions(task_id);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init sqlite: %w", err)
		}
	}
	return nil
}

// getFindings loads all persisted findings of a task.
func (s *SQLiteStore) getFindings(ctx context.Context, taskID string) ([]review.Finding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id
FROM review_findings WHERE task_id = ? ORDER BY file, line, rule_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []review.Finding
	for rows.Next() {
		var f review.Finding
		if err := rows.Scan(&f.Severity, &f.Category, &f.File, &f.Line,
			&f.Title, &f.Evidence, &f.Recommendation, &f.Confidence,
			&f.Source, &f.RuleID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// getSandboxRuns loads all persisted sandbox runs of a task.
func (s *SQLiteStore) getSandboxRuns(ctx context.Context, taskID string) ([]review.SandboxRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT command, status, exit_code, duration_ms, stdout_excerpt, stderr_excerpt, error
FROM sandbox_runs WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []review.SandboxRun
	for rows.Next() {
		var run review.SandboxRun
		if err := rows.Scan(&run.Command, &run.Status, &run.ExitCode,
			&run.DurationMS, &run.StdoutExcerpt, &run.StderrExcerpt,
			&run.Error); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// getPermissionDecisions loads the permission audit trail of a task.
func (s *SQLiteStore) getPermissionDecisions(ctx context.Context, taskID string) ([]review.PermissionDecision, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT command, decision, reason, created_at
FROM permission_decisions WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []review.PermissionDecision
	for rows.Next() {
		var d review.PermissionDecision
		var created string
		if err := rows.Scan(&d.Command, &d.Decision, &d.Reason, &created); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			d.CreatedAt = t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// getFilterDecisions loads the filter audit trail of a task.
func (s *SQLiteStore) getFilterDecisions(ctx context.Context, taskID string) ([]review.FilterDecision, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT rule_id, file, line, source, confidence, stage, decision, reason, created_at
FROM filter_decisions WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []review.FilterDecision
	for rows.Next() {
		var d review.FilterDecision
		var created string
		if err := rows.Scan(&d.RuleID, &d.File, &d.Line, &d.Source,
			&d.Confidence, &d.Stage, &d.Decision, &d.Reason, &created); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			d.CreatedAt = t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// getArtifacts loads artifact records of a task.
func (s *SQLiteStore) getArtifacts(ctx context.Context, taskID string) ([]review.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT kind, path, sha256, size_bytes FROM artifacts WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []review.Artifact
	for rows.Next() {
		var a review.Artifact
		if err := rows.Scan(&a.Kind, &a.Path, &a.SHA256, &a.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
