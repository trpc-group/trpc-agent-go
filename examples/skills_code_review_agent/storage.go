//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Storage handles persistent SQLite database operations for code review agent.
type Storage struct {
	db *sql.DB
}

// NewStorage initializes SQLite database storage and executes schema migrations.
func NewStorage(dbPath string) (*Storage, error) {
	dsn := dbPath
	if dbPath == ":memory:" {
		dsn = "file::memory:?cache=shared"
	} else if !strings.Contains(dbPath, "?") {
		dsn = dbPath + "?_journal_mode=WAL"
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db failed: %w", err)
	}
	db.SetMaxOpenConns(1)

	schema := `
	CREATE TABLE IF NOT EXISTS review_tasks (
		id TEXT PRIMARY KEY,
		repo_path TEXT NOT NULL,
		diff_summary TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		completed_at DATETIME
	);
	CREATE TABLE IF NOT EXISTS sandbox_runs (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		command TEXT NOT NULL,
		status TEXT NOT NULL,
		exit_code INTEGER NOT NULL,
		output_snippet TEXT,
		duration_ms INTEGER NOT NULL,
		FOREIGN KEY(task_id) REFERENCES review_tasks(id)
	);
	CREATE TABLE IF NOT EXISTS permission_decisions (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		command TEXT NOT NULL,
		decision TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		FOREIGN KEY(task_id) REFERENCES review_tasks(id)
	);
	CREATE TABLE IF NOT EXISTS findings (
		id TEXT PRIMARY KEY,
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
		rule_id TEXT NOT NULL,
		FOREIGN KEY(task_id) REFERENCES review_tasks(id)
	);
	CREATE TABLE IF NOT EXISTS audit_events (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		FOREIGN KEY(task_id) REFERENCES review_tasks(id)
	);`

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("execute schema failed: %w", err)
	}

	return &Storage{db: db}, nil
}

// Close closes the database connection.
func (s *Storage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// SaveTask persists a new review task.
func (s *Storage) SaveTask(taskID, repoPath, diffSummary, status string, createdAt time.Time) error {
	_, err := s.db.Exec(
		"INSERT INTO review_tasks (id, repo_path, diff_summary, status, created_at) VALUES (?, ?, ?, ?, ?)",
		taskID, repoPath, diffSummary, status, createdAt,
	)
	if err != nil {
		return fmt.Errorf("save task failed: %w", err)
	}
	return nil
}

// UpdateTaskStatus updates the status and completion timestamp of a task.
func (s *Storage) UpdateTaskStatus(taskID, status string, completedAt time.Time) error {
	_, err := s.db.Exec(
		"UPDATE review_tasks SET status = ?, completed_at = ? WHERE id = ?",
		status, completedAt, taskID,
	)
	if err != nil {
		return fmt.Errorf("update task status failed: %w", err)
	}
	return nil
}

// SaveSandboxRun persists a sandbox execution record.
func (s *Storage) SaveSandboxRun(id, taskID, command, status string, exitCode int, outputSnippet string, durationMs int64) error {
	_, err := s.db.Exec(
		"INSERT INTO sandbox_runs (id, task_id, command, status, exit_code, output_snippet, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?)",
		id, taskID, command, status, exitCode, outputSnippet, durationMs,
	)
	if err != nil {
		return fmt.Errorf("save sandbox run failed: %w", err)
	}
	return nil
}

// SavePermissionDecision persists a permission governance decision.
func (s *Storage) SavePermissionDecision(id, taskID, command, decision, reason string, createdAt time.Time) error {
	_, err := s.db.Exec(
		"INSERT INTO permission_decisions (id, task_id, command, decision, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, taskID, command, decision, reason, createdAt,
	)
	if err != nil {
		return fmt.Errorf("save permission decision failed: %w", err)
	}
	return nil
}

// SaveFinding persists a code review finding.
func (s *Storage) SaveFinding(f Finding) error {
	_, err := s.db.Exec(
		`INSERT INTO findings 
		(id, task_id, severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.TaskID, f.Severity, f.Category, f.File, f.Line, f.Title, f.Evidence, f.Recommendation, f.Confidence, f.Source, f.RuleID,
	)
	if err != nil {
		return fmt.Errorf("save finding failed: %w", err)
	}
	return nil
}

// SaveAuditEvent persists an audit event JSON payload.
func (s *Storage) SaveAuditEvent(id, taskID, eventType, payloadJSON string, createdAt time.Time) error {
	_, err := s.db.Exec(
		"INSERT INTO audit_events (id, task_id, event_type, payload_json, created_at) VALUES (?, ?, ?, ?, ?)",
		id, taskID, eventType, payloadJSON, createdAt,
	)
	if err != nil {
		return fmt.Errorf("save audit event failed: %w", err)
	}
	return nil
}

// GetTaskFindings retrieves all findings for a given task ID.
func (s *Storage) GetTaskFindings(taskID string) ([]Finding, error) {
	rows, err := s.db.Query(
		"SELECT id, task_id, severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id FROM findings WHERE task_id = ?",
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("query findings failed: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.ID, &f.TaskID, &f.Severity, &f.Category, &f.File, &f.Line, &f.Title, &f.Evidence, &f.Recommendation, &f.Confidence, &f.Source, &f.RuleID); err != nil {
			return nil, fmt.Errorf("scan finding failed: %w", err)
		}
		findings = append(findings, f)
	}
	return findings, nil
}
