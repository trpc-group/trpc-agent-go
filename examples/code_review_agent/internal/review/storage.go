//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// Register the SQLite driver used by the default Store implementation.
	_ "github.com/mattn/go-sqlite3"
)

// Store persists review reports and exposes their normalized audit records.
type Store interface {
	// Save persists a complete report atomically.
	Save(context.Context, Report) error
	// Load retrieves a complete report by task ID.
	Load(context.Context, string) (Report, error)
	// LoadTask retrieves task lifecycle state by task ID.
	LoadTask(context.Context, string) (Task, error)
	// LoadRuns retrieves sandbox runs by task ID.
	LoadRuns(context.Context, string) ([]SandboxRun, error)
	// LoadDecisions retrieves permission decisions by task ID.
	LoadDecisions(context.Context, string) ([]PermissionDecision, error)
	// LoadFilterDecisions retrieves finding-routing decisions by task ID.
	LoadFilterDecisions(context.Context, string) ([]FilterDecision, error)
	// LoadMetrics retrieves monitoring metrics by task ID.
	LoadMetrics(context.Context, string) (Metrics, error)
	// LoadFindings retrieves one finding bucket by task ID.
	LoadFindings(context.Context, string, string) ([]Finding, error)
	// LoadArtifacts retrieves published artifacts by task ID.
	LoadArtifacts(context.Context, string) ([]Artifact, error)
	// Delete removes a report and all normalized audit records atomically.
	Delete(context.Context, string) error
	// Close releases the store resources.
	Close() error
}

type sqliteStore struct{ db *sql.DB }

func openStore(path string) (Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)
	cleanup := func() {
		if !created {
			return
		}
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=on&_journal_mode=DELETE")
	if err != nil {
		cleanup()
		return nil, err
	}
	store := &sqliteStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		cleanup()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		cleanup()
		return nil, err
	}
	return store, nil
}

func (s *sqliteStore) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS review_tasks (id TEXT PRIMARY KEY, status TEXT NOT NULL, input_mode TEXT NOT NULL, started_at TEXT NOT NULL, ended_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS review_inputs (task_id TEXT PRIMARY KEY REFERENCES review_tasks(id), digest TEXT NOT NULL, files_changed INTEGER NOT NULL, go_files INTEGER NOT NULL, added_lines INTEGER NOT NULL, deleted_lines INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sandbox_runs (task_id TEXT NOT NULL REFERENCES review_tasks(id), ordinal INTEGER NOT NULL, command TEXT NOT NULL, executor TEXT NOT NULL, status TEXT NOT NULL, exit_code INTEGER NOT NULL, error_type TEXT NOT NULL, payload_json TEXT NOT NULL, PRIMARY KEY(task_id, ordinal));
CREATE TABLE IF NOT EXISTS permission_decisions (task_id TEXT NOT NULL REFERENCES review_tasks(id), ordinal INTEGER NOT NULL, command TEXT NOT NULL, action TEXT NOT NULL, reason TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(task_id, ordinal));
CREATE TABLE IF NOT EXISTS filter_decisions (task_id TEXT NOT NULL REFERENCES review_tasks(id), ordinal INTEGER NOT NULL, fingerprint TEXT NOT NULL, action TEXT NOT NULL, reason TEXT NOT NULL, target_bucket TEXT NOT NULL, PRIMARY KEY(task_id, ordinal));
CREATE TABLE IF NOT EXISTS findings (task_id TEXT NOT NULL REFERENCES review_tasks(id), bucket TEXT NOT NULL, fingerprint TEXT NOT NULL, severity TEXT NOT NULL, category TEXT NOT NULL, file TEXT NOT NULL, line INTEGER NOT NULL, payload_json TEXT NOT NULL, PRIMARY KEY(task_id, bucket, fingerprint));
CREATE TABLE IF NOT EXISTS artifacts (task_id TEXT NOT NULL REFERENCES review_tasks(id), name TEXT NOT NULL, path TEXT NOT NULL, mime_type TEXT NOT NULL, size_bytes INTEGER NOT NULL, PRIMARY KEY(task_id, name));
CREATE TABLE IF NOT EXISTS review_metrics (task_id TEXT PRIMARY KEY REFERENCES review_tasks(id), payload_json TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS review_reports (task_id TEXT PRIMARY KEY REFERENCES review_tasks(id), conclusion TEXT NOT NULL, payload_json TEXT NOT NULL);
`)
	return err
}

func (s *sqliteStore) Save(ctx context.Context, report Report) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO review_tasks(id,status,input_mode,started_at,ended_at) VALUES(?,?,?,?,?)`, report.Task.ID, report.Task.Status, report.Task.InputMode, report.Task.StartedAt.UTC().Format(timeFormat), report.Task.EndedAt.UTC().Format(timeFormat)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO review_inputs(task_id,digest,files_changed,go_files,added_lines,deleted_lines) VALUES(?,?,?,?,?,?)`, report.Task.ID, report.Input.Digest, report.Input.FilesChanged, report.Input.GoFiles, report.Input.AddedLines, report.Input.DeletedLines); err != nil {
		return err
	}
	for index, run := range report.SandboxRuns {
		payload, err := json.Marshal(run)
		if err != nil {
			return fmt.Errorf("encode sandbox run %d: %w", index, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO sandbox_runs(task_id,ordinal,command,executor,status,exit_code,error_type,payload_json) VALUES(?,?,?,?,?,?,?,?)`, report.Task.ID, index, run.Command, run.Executor, run.Status, run.ExitCode, run.ErrorType, string(payload)); err != nil {
			return err
		}
	}
	for index, decision := range report.PermissionDecisions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO permission_decisions(task_id,ordinal,command,action,reason,created_at) VALUES(?,?,?,?,?,?)`, report.Task.ID, index, decision.Command, decision.Action, redact(decision.Reason), decision.CreatedAt.UTC().Format(timeFormat)); err != nil {
			return err
		}
	}
	for index, decision := range report.FilterDecisions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO filter_decisions(task_id,ordinal,fingerprint,action,reason,target_bucket) VALUES(?,?,?,?,?,?)`, report.Task.ID, index, decision.Fingerprint, decision.Action, redact(decision.Reason), decision.TargetBucket); err != nil {
			return err
		}
	}
	for bucket, findings := range map[string][]Finding{"finding": report.Findings, "warning": report.Warnings, "needs_human_review": report.NeedsHumanReview} {
		for _, finding := range findings {
			payload, err := json.Marshal(finding)
			if err != nil {
				return fmt.Errorf("encode %s finding: %w", bucket, err)
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO findings(task_id,bucket,fingerprint,severity,category,file,line,payload_json) VALUES(?,?,?,?,?,?,?,?)`, report.Task.ID, bucket, finding.Fingerprint, finding.Severity, finding.Category, finding.File, finding.Line, string(payload)); err != nil {
				return err
			}
		}
	}
	for _, artifact := range report.Artifacts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO artifacts(task_id,name,path,mime_type,size_bytes) VALUES(?,?,?,?,?)`, report.Task.ID, artifact.Name, artifact.Path, artifact.MIMEType, artifact.SizeBytes); err != nil {
			return err
		}
	}
	metrics, err := json.Marshal(report.Metrics)
	if err != nil {
		return fmt.Errorf("encode review metrics: %w", err)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode review report: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO review_metrics(task_id,payload_json) VALUES(?,?)`, report.Task.ID, string(metrics)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO review_reports(task_id,conclusion,payload_json) VALUES(?,?,?)`, report.Task.ID, report.Conclusion, string(payload)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) Load(ctx context.Context, taskID string) (Report, error) {
	var payload string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM review_reports WHERE task_id=?`, taskID).Scan(&payload); err != nil {
		return Report{}, err
	}
	var report Report
	if err := json.Unmarshal([]byte(payload), &report); err != nil {
		return Report{}, fmt.Errorf("decode stored report: %w", err)
	}
	return report, nil
}

func (s *sqliteStore) LoadTask(ctx context.Context, taskID string) (Task, error) {
	var task Task
	var startedAt, endedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,status,input_mode,started_at,ended_at FROM review_tasks WHERE id=?`, taskID).
		Scan(&task.ID, &task.Status, &task.InputMode, &startedAt, &endedAt)
	if err != nil {
		return Task{}, err
	}
	if task.StartedAt, err = time.Parse(timeFormat, startedAt); err != nil {
		return Task{}, fmt.Errorf("parse task start time: %w", err)
	}
	if task.EndedAt, err = time.Parse(timeFormat, endedAt); err != nil {
		return Task{}, fmt.Errorf("parse task end time: %w", err)
	}
	return task, nil
}

func (s *sqliteStore) LoadRuns(ctx context.Context, taskID string) ([]SandboxRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM sandbox_runs WHERE task_id=? ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, err
	}
	return decodeJSONRows[SandboxRun](rows, "sandbox run")
}

func (s *sqliteStore) LoadDecisions(ctx context.Context, taskID string) ([]PermissionDecision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT command,action,reason,created_at FROM permission_decisions WHERE task_id=? ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []PermissionDecision
	for rows.Next() {
		var value PermissionDecision
		var createdAt string
		if err := rows.Scan(&value.Command, &value.Action, &value.Reason, &createdAt); err != nil {
			return nil, err
		}
		if value.CreatedAt, err = time.Parse(timeFormat, createdAt); err != nil {
			return nil, fmt.Errorf("parse decision time: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *sqliteStore) LoadFilterDecisions(ctx context.Context, taskID string) ([]FilterDecision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fingerprint,action,reason,target_bucket FROM filter_decisions WHERE task_id=? ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []FilterDecision
	for rows.Next() {
		var value FilterDecision
		if err := rows.Scan(&value.Fingerprint, &value.Action, &value.Reason, &value.TargetBucket); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *sqliteStore) LoadMetrics(ctx context.Context, taskID string) (Metrics, error) {
	var payload string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM review_metrics WHERE task_id=?`, taskID).Scan(&payload); err != nil {
		return Metrics{}, err
	}
	var value Metrics
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return Metrics{}, fmt.Errorf("decode review metrics: %w", err)
	}
	return value, nil
}

func (s *sqliteStore) LoadFindings(ctx context.Context, taskID, bucket string) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM findings WHERE task_id=? AND bucket=? ORDER BY fingerprint`, taskID, bucket)
	if err != nil {
		return nil, err
	}
	return decodeJSONRows[Finding](rows, "finding")
}

func decodeJSONRows[T any](rows *sql.Rows, entity string) ([]T, error) {
	defer rows.Close()
	var values []T
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var value T
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			return nil, fmt.Errorf("decode %s: %w", entity, err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *sqliteStore) LoadArtifacts(ctx context.Context, taskID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,path,mime_type,size_bytes FROM artifacts WHERE task_id=? ORDER BY name`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []Artifact
	for rows.Next() {
		var value Artifact
		if err := rows.Scan(&value.Name, &value.Path, &value.MIMEType, &value.SizeBytes); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *sqliteStore) Delete(ctx context.Context, taskID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"review_reports", "review_metrics", "artifacts", "findings", "filter_decisions", "permission_decisions", "sandbox_runs", "review_inputs", "review_tasks"} {
		column := "task_id"
		if table == "review_tasks" {
			column = "id"
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+column+"=?", taskID); err != nil {
			return fmt.Errorf("delete %s records: %w", table, err)
		}
	}
	return tx.Commit()
}

func (s *sqliteStore) Close() error { return s.db.Close() }

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"
