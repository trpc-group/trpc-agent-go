//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package storage persists the review audit chain.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/domain"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
)

const sqliteDriverName = "sqlite3"

// ErrReviewNotFound is returned when no audit report exists for a task id.
var ErrReviewNotFound = errors.New("review not found")

// Store persists and retrieves review audits.
type Store interface {
	RecordDecision(context.Context, string, DecisionRecord) error
	Finalize(context.Context, report.DTO) error
	GetReview(context.Context, string) (report.DTO, error)
	Close() error
}

// DecisionRecord is one permission decision bound to an immutable execution plan.
type DecisionRecord struct {
	Action     string
	Reason     string
	PlanDigest string
	CreatedAt  time.Time
}

// SQLiteStore stores review audit rows in SQLite through database/sql.
type SQLiteStore struct {
	path string
	db   *sql.DB
}

// SQLiteAvailable reports whether the built-in pure-Go SQLite driver is registered.
func SQLiteAvailable() bool {
	for _, name := range sql.Drivers() {
		if name == sqliteDriverName {
			return true
		}
	}
	return false
}

// OpenSQLite opens or creates an audit database.
func OpenSQLite(path string) (*SQLiteStore, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(on)")
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &SQLiteStore{path: path, db: db}
	if err := s.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DB returns the underlying database/sql handle for diagnostics and tests.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// Close closes the owned database connection.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// RecordDecision persists a governance decision before staging or execution.
func (s *SQLiteStore) RecordDecision(ctx context.Context, taskID string, rec DecisionRecord) error {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO review_tasks(task_id,status) VALUES(?,?)",
		taskID, "running",
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO governance_decisions(task_id,action,reason,plan_digest,created_at) VALUES(?,?,?,?,?)",
		taskID, rec.Action, rec.Reason, rec.PlanDigest, rec.CreatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// Finalize writes the final report and all audit entities in one transaction.
func (s *SQLiteStore) Finalize(ctx context.Context, dto report.DTO) error {
	js, err := report.RenderJSON(dto)
	if err != nil {
		return err
	}
	var clean report.DTO
	if err := json.Unmarshal(js, &clean); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO review_tasks(task_id,status,input_kind,input_digest,input_files,input_hunks,input_added_lines) VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT(task_id) DO UPDATE SET status=excluded.status,input_kind=excluded.input_kind,input_digest=excluded.input_digest,input_files=excluded.input_files,input_hunks=excluded.input_hunks,input_added_lines=excluded.input_added_lines`,
		clean.TaskID, string(clean.Status), clean.Input.Kind, clean.Input.Digest, clean.Input.Files, clean.Input.Hunks, clean.Input.AddedLines,
	); err != nil {
		return err
	}
	if err := deleteChildren(ctx, tx, clean.TaskID); err != nil {
		return err
	}
	for _, run := range clean.SandboxRuns {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO sandbox_runs(task_id,command_id,stdout,stderr,exit_code,timed_out,truncated,outcome,duration_ms) VALUES(?,?,?,?,?,?,?,?,?)",
			clean.TaskID, run.CommandID, run.Stdout, run.Stderr, run.ExitCode, run.TimedOut, run.Truncated, run.Outcome, run.DurationMS,
		); err != nil {
			return err
		}
	}
	var decisionCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM governance_decisions WHERE task_id=?", clean.TaskID).Scan(&decisionCount); err != nil {
		return err
	}
	if decisionCount == 0 {
		for _, g := range clean.Governance {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO governance_decisions(task_id,action,reason,plan_digest,created_at) VALUES(?,?,?,?,?)",
				clean.TaskID, g, "", "", time.Now().UTC().Format(time.RFC3339Nano),
			); err != nil {
				return err
			}
		}
	}
	if err := insertFindings(ctx, tx, clean.TaskID, clean.Findings, false); err != nil {
		return err
	}
	if err := insertFindings(ctx, tx, clean.TaskID, clean.NeedsHumanReview, true); err != nil {
		return err
	}
	artifacts := clean.ArtifactDetails
	if len(artifacts) == 0 {
		for _, a := range clean.Artifacts {
			artifacts = append(artifacts, report.Artifact{Path: a, Durable: true})
		}
	}
	for _, a := range artifacts {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO artifacts(task_id,path,sha256,bytes,content_type,durable) VALUES(?,?,?,?,?,?)",
			clean.TaskID, a.Path, a.SHA256, a.Bytes, a.ContentType, a.Durable,
		); err != nil {
			return err
		}
	}
	for k, v := range clean.Metrics {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO review_metrics(task_id,name,value) VALUES(?,?,?)",
			clean.TaskID, k, v,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT OR REPLACE INTO reports(task_id,json,markdown) VALUES(?,?,?)",
		clean.TaskID, string(js), report.RenderMarkdown(clean),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// GetReview reads one aggregate report from a read-only transaction snapshot.
func (s *SQLiteStore) GetReview(ctx context.Context, taskID string) (report.DTO, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return report.DTO{}, err
	}
	defer rollbackUnlessCommitted(tx)
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT json FROM reports WHERE task_id=?", taskID).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return report.DTO{}, ErrReviewNotFound
		}
		return report.DTO{}, err
	}
	var dto report.DTO
	if err := json.Unmarshal([]byte(raw), &dto); err != nil {
		return report.DTO{}, err
	}
	if err := tx.Commit(); err != nil {
		return report.DTO{}, err
	}
	return dto, nil
}

func (s *SQLiteStore) init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, schemaSQL)
	return err
}

func insertFindings(ctx context.Context, tx *sql.Tx, taskID string, findings []domain.Finding, needsHumanReview bool) error {
	for _, f := range findings {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO findings(task_id,severity,category,file,line,title,evidence,recommendation,confidence,source,rule_id,needs_human_review) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
			taskID, string(f.Severity), f.Category, f.File, f.Line, f.Title, f.Evidence, f.Recommendation, f.Confidence, f.Source, f.RuleID, needsHumanReview,
		); err != nil {
			return err
		}
	}
	return nil
}

func deleteChildren(ctx context.Context, tx *sql.Tx, taskID string) error {
	for _, stmt := range []string{
		"DELETE FROM sandbox_runs WHERE task_id=?",
		"DELETE FROM findings WHERE task_id=?",
		"DELETE FROM artifacts WHERE task_id=?",
		"DELETE FROM review_metrics WHERE task_id=?",
		"DELETE FROM reports WHERE task_id=?",
	} {
		if _, err := tx.ExecContext(ctx, stmt, taskID); err != nil {
			return err
		}
	}
	return nil
}

func rollbackUnlessCommitted(tx *sql.Tx) {
	_ = tx.Rollback()
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS review_tasks(task_id TEXT PRIMARY KEY, status TEXT NOT NULL, input_kind TEXT NOT NULL DEFAULT '', input_digest TEXT NOT NULL DEFAULT '', input_files INTEGER NOT NULL DEFAULT 0, input_hunks INTEGER NOT NULL DEFAULT 0, input_added_lines INTEGER NOT NULL DEFAULT 0, created_at TEXT DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS sandbox_runs(id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES review_tasks(task_id), command_id TEXT, stdout TEXT, stderr TEXT, exit_code INTEGER, timed_out INTEGER, truncated INTEGER, outcome TEXT NOT NULL, duration_ms INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS governance_decisions(id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES review_tasks(task_id), action TEXT NOT NULL, reason TEXT NOT NULL, plan_digest TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS findings(id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES review_tasks(task_id), severity TEXT, category TEXT, file TEXT, line INTEGER, title TEXT, evidence TEXT, recommendation TEXT, confidence REAL, source TEXT, rule_id TEXT, needs_human_review INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS artifacts(id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES review_tasks(task_id), path TEXT NOT NULL, sha256 TEXT NOT NULL DEFAULT '', bytes INTEGER NOT NULL DEFAULT 0, content_type TEXT NOT NULL DEFAULT '', durable INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS review_metrics(id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES review_tasks(task_id), name TEXT NOT NULL, value INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS reports(task_id TEXT PRIMARY KEY REFERENCES review_tasks(task_id), json TEXT NOT NULL, markdown TEXT NOT NULL);
`
