//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package storage provides SQLite-backed persistence for code review tasks.
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection with review-specific helpers.
type DB struct {
	conn *sql.DB
}

// Open creates or opens a SQLite database at path and initialises the schema.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db := &DB{conn: conn}
	if err := db.init(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the connection.
func (db *DB) Close() { _ = db.conn.Close() }

func (db *DB) init() error {
	_, err := db.conn.Exec(`
CREATE TABLE IF NOT EXISTS review_task (
    id          TEXT PRIMARY KEY,
    diff_hash   TEXT,
    repo_path   TEXT,
    status      TEXT,
    created_at  TEXT,
    finished_at TEXT
);
CREATE TABLE IF NOT EXISTS sandbox_run (
    id          TEXT PRIMARY KEY,
    task_id     TEXT,
    command     TEXT,
    exit_code   INTEGER,
    output      TEXT,
    duration_ms INTEGER,
    created_at  TEXT,
    FOREIGN KEY(task_id) REFERENCES review_task(id)
);
CREATE TABLE IF NOT EXISTS finding (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     TEXT,
    severity    TEXT,
    category    TEXT,
    file        TEXT,
    line        INTEGER,
    title       TEXT,
    evidence    TEXT,
    recommendation TEXT,
    confidence  TEXT,
    source      TEXT,
    rule_id     TEXT,
    FOREIGN KEY(task_id) REFERENCES review_task(id)
);
CREATE TABLE IF NOT EXISTS report (
    task_id     TEXT PRIMARY KEY,
    json_body   TEXT,
    md_body     TEXT,
    created_at  TEXT,
    FOREIGN KEY(task_id) REFERENCES review_task(id)
);`)
	return err
}

// Task status values.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// InsertTask inserts a new review task and returns its ID.
func (db *DB) InsertTask(id, diffHash, repoPath string) error {
	_, err := db.conn.Exec(
		`INSERT INTO review_task(id, diff_hash, repo_path, status, created_at) VALUES(?,?,?,?,?)`,
		id, diffHash, repoPath, StatusRunning, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// FinishTask marks the task as done.
func (db *DB) FinishTask(id, status string) error {
	_, err := db.conn.Exec(
		`UPDATE review_task SET status=?, finished_at=? WHERE id=?`,
		status, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// SandboxRun records a sandbox execution.
type SandboxRun struct {
	ID         string
	TaskID     string
	Command    string
	ExitCode   int
	Output     string
	DurationMs int64
}

// InsertSandboxRun saves a sandbox execution record.
func (db *DB) InsertSandboxRun(r SandboxRun) error {
	_, err := db.conn.Exec(
		`INSERT INTO sandbox_run(id, task_id, command, exit_code, output, duration_ms, created_at) VALUES(?,?,?,?,?,?,?)`,
		r.ID, r.TaskID, r.Command, r.ExitCode, r.Output, r.DurationMs,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// FindingRow is a finding record to store.
type FindingRow struct {
	TaskID         string
	Severity       string
	Category       string
	File           string
	Line           int
	Title          string
	Evidence       string
	Recommendation string
	Confidence     string
	Source         string
	RuleID         string
}

// InsertFindings bulk-inserts findings for a task.
func (db *DB) InsertFindings(rows []FindingRow) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO finding(task_id,severity,category,file,line,title,evidence,recommendation,confidence,source,rule_id)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
	)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(r.TaskID, r.Severity, r.Category, r.File, r.Line,
			r.Title, r.Evidence, r.Recommendation, r.Confidence, r.Source, r.RuleID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SaveReport persists the final report JSON and Markdown.
func (db *DB) SaveReport(taskID, jsonBody, mdBody string) error {
	_, err := db.conn.Exec(
		`INSERT OR REPLACE INTO report(task_id, json_body, md_body, created_at) VALUES(?,?,?,?)`,
		taskID, jsonBody, mdBody, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// QueryTaskFindings returns all findings for a task ID.
func (db *DB) QueryTaskFindings(taskID string) ([]FindingRow, error) {
	rows, err := db.conn.Query(
		`SELECT task_id,severity,category,file,line,title,evidence,recommendation,confidence,source,rule_id
		 FROM finding WHERE task_id=?`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FindingRow
	for rows.Next() {
		var r FindingRow
		if err := rows.Scan(&r.TaskID, &r.Severity, &r.Category, &r.File, &r.Line,
			&r.Title, &r.Evidence, &r.Recommendation, &r.Confidence, &r.Source, &r.RuleID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarshalJSON is a helper to marshal any value to a JSON string.
func MarshalJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}
