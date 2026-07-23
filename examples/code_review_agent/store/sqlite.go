//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package store persists review tasks and related audit records.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

//go:embed schema.sql
var schemaSQL string

// CreateTaskReq creates a review task row.
type CreateTaskReq struct {
	Mode         string
	Executor     string
	RepoPath     string
	InputKind    string
	InputDigest  string
	InputSummary string
}

// InputRecord stores redacted input metadata.
type InputRecord struct {
	DiffTextRedacted string
	FileListJSON     string
	PackageListJSON  string
}

// ReportRecord stores generated reports.
type ReportRecord struct {
	JSONPath   string
	MDPath     string
	ReportJSON string
	ReportMD   string
}

// TaskBundle is the full queryable review package.
type TaskBundle struct {
	Task        review.Report // status/mode/executor/conclusion via fields
	TaskID      string
	Status      string
	Mode        string
	Executor    string
	Conclusion  string
	Error       string
	Input       InputRecord
	SandboxRuns []review.SandboxRunSummary
	Permissions []review.PermissionDecision
	Findings    []review.Finding
	Warnings    []review.Finding
	Artifacts   []review.ArtifactRef
	Metrics     review.MetricsSummary
	ReportJSON  string
	ReportMD    string
}

// ReviewStore is the persistence contract.
type ReviewStore interface {
	Migrate(ctx context.Context) error
	CreateTask(ctx context.Context, req CreateTaskReq) (string, error)
	UpdateTaskStatus(ctx context.Context, taskID, status, conclusion, errMsg string) error
	SaveInput(ctx context.Context, taskID string, in InputRecord) error
	SaveSandboxRun(ctx context.Context, taskID string, run review.SandboxRunSummary) error
	SavePermission(ctx context.Context, taskID string, d review.PermissionDecision) error
	SaveFindings(ctx context.Context, taskID string, findings, warnings []review.Finding) error
	SaveArtifacts(ctx context.Context, taskID string, arts []review.ArtifactRef) error
	SaveMetrics(ctx context.Context, taskID string, m review.MetricsSummary) error
	SaveReport(ctx context.Context, taskID string, rep ReportRecord) error
	GetTaskBundle(ctx context.Context, taskID string) (*TaskBundle, error)
	Close() error
}

// SQLiteStore is the default ReviewStore.
type SQLiteStore struct {
	db *sql.DB
}

// OpenSQLite opens (or creates) a SQLite database at path.
func OpenSQLite(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Reasonable defaults for tests.
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close implements ReviewStore.
func (s *SQLiteStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Migrate applies schema.sql.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schemaSQL)
	return err
}

// CreateTask implements ReviewStore.
func (s *SQLiteStore) CreateTask(ctx context.Context, req CreateTaskReq) (string, error) {
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO review_task(
  id, created_at, updated_at, status, mode, executor, repo_path,
  input_kind, input_digest, input_summary
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, now, now, review.StatusPending, req.Mode, req.Executor, req.RepoPath,
		req.InputKind, req.InputDigest, req.InputSummary,
	)
	return id, err
}

// UpdateTaskStatus implements ReviewStore.
func (s *SQLiteStore) UpdateTaskStatus(ctx context.Context, taskID, status, conclusion, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
UPDATE review_task SET updated_at=?, status=?, conclusion=?, error=? WHERE id=?`,
		now, status, conclusion, errMsg, taskID,
	)
	return err
}

// SaveInput implements ReviewStore.
func (s *SQLiteStore) SaveInput(ctx context.Context, taskID string, in InputRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO review_input(task_id, diff_text_redacted, file_list_json, package_list_json)
VALUES (?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
  diff_text_redacted=excluded.diff_text_redacted,
  file_list_json=excluded.file_list_json,
  package_list_json=excluded.package_list_json`,
		taskID, in.DiffTextRedacted, in.FileListJSON, in.PackageListJSON,
	)
	return err
}

// SaveSandboxRun implements ReviewStore.
func (s *SQLiteStore) SaveSandboxRun(ctx context.Context, taskID string, run review.SandboxRunSummary) error {
	id := run.ID
	if id == "" {
		id = uuid.NewString()
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	trunc := 0
	if run.Truncated {
		trunc = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sandbox_run(
  id, task_id, executor, command, started_at, ended_at, timeout_ms,
  exit_code, status, stdout_bytes, stderr_bytes, truncated, error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, taskID, run.Executor, run.Command, now, now, run.DurationMS,
		run.ExitCode, run.Status, run.StdoutBytes, run.StderrBytes, trunc, run.Error,
	)
	return err
}

// SavePermission implements ReviewStore.
func (s *SQLiteStore) SavePermission(ctx context.Context, taskID string, d review.PermissionDecision) error {
	id := uuid.NewString()
	ts := d.CreatedAt
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO permission_decision(id, task_id, tool_name, command, action, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, taskID, d.ToolName, d.Command, d.Action, d.Reason, ts.Format(time.RFC3339Nano),
	)
	return err
}

// SaveFindings implements ReviewStore.
func (s *SQLiteStore) SaveFindings(ctx context.Context, taskID string, findings, warnings []review.Finding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM finding WHERE task_id=?`, taskID); err != nil {
		return err
	}
	ins := `INSERT INTO finding(
  id, task_id, severity, category, file, line, title, evidence, recommendation,
  confidence, source, rule_id, bucket
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	write := func(list []review.Finding, bucket string) error {
		for _, f := range list {
			if _, err := tx.ExecContext(ctx, ins,
				uuid.NewString(), taskID, f.Severity, f.Category, f.File, f.Line,
				f.Title, f.Evidence, f.Recommendation, f.Confidence, f.Source, f.RuleID, bucket,
			); err != nil {
				return err
			}
		}
		return nil
	}
	if err := write(findings, review.BucketFinding); err != nil {
		return err
	}
	if err := write(warnings, review.BucketWarning); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveArtifacts implements ReviewStore.
func (s *SQLiteStore) SaveArtifacts(ctx context.Context, taskID string, arts []review.ArtifactRef) error {
	for _, a := range arts {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO artifact(id, task_id, name, mime, path_or_ref, size_bytes, sha256)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), taskID, a.Name, a.MIME, a.PathOrRef, a.SizeBytes, a.SHA256,
		); err != nil {
			return err
		}
	}
	return nil
}

// SaveMetrics implements ReviewStore.
func (s *SQLiteStore) SaveMetrics(ctx context.Context, taskID string, m review.MetricsSummary) error {
	sev, _ := json.Marshal(m.SeverityDist)
	exc, _ := json.Marshal(m.ExceptionDist)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO metrics_summary(
  task_id, total_ms, sandbox_ms, tool_calls, permission_denies, permission_asks,
  finding_count, warning_count, severity_json, exception_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
  total_ms=excluded.total_ms,
  sandbox_ms=excluded.sandbox_ms,
  tool_calls=excluded.tool_calls,
  permission_denies=excluded.permission_denies,
  permission_asks=excluded.permission_asks,
  finding_count=excluded.finding_count,
  warning_count=excluded.warning_count,
  severity_json=excluded.severity_json,
  exception_json=excluded.exception_json`,
		taskID, m.TotalDurationMS, m.SandboxDurationMS, m.ToolCallCount,
		m.PermissionDenyCount, m.PermissionAskCount, m.FindingCount, m.WarningCount,
		string(sev), string(exc),
	)
	return err
}

// SaveReport implements ReviewStore.
func (s *SQLiteStore) SaveReport(ctx context.Context, taskID string, rep ReportRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO report(task_id, json_path, md_path, report_json, report_md)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
  json_path=excluded.json_path,
  md_path=excluded.md_path,
  report_json=excluded.report_json,
  report_md=excluded.report_md`,
		taskID, rep.JSONPath, rep.MDPath, rep.ReportJSON, rep.ReportMD,
	)
	return err
}

// GetTaskBundle implements ReviewStore.
func (s *SQLiteStore) GetTaskBundle(ctx context.Context, taskID string) (*TaskBundle, error) {
	b := &TaskBundle{TaskID: taskID}
	row := s.db.QueryRowContext(ctx, `
SELECT status, mode, executor, conclusion, COALESCE(error,'')
FROM review_task WHERE id=?`, taskID)
	if err := row.Scan(&b.Status, &b.Mode, &b.Executor, &b.Conclusion, &b.Error); err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}

	_ = s.db.QueryRowContext(ctx, `
SELECT COALESCE(diff_text_redacted,''), COALESCE(file_list_json,''), COALESCE(package_list_json,'')
FROM review_input WHERE task_id=?`, taskID).Scan(
		&b.Input.DiffTextRedacted, &b.Input.FileListJSON, &b.Input.PackageListJSON,
	)

	rows, err := s.db.QueryContext(ctx, `
SELECT id, executor, command, status, exit_code, COALESCE(timeout_ms,0),
       stdout_bytes, stderr_bytes, truncated, COALESCE(error,'')
FROM sandbox_run WHERE task_id=?`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r review.SandboxRunSummary
		var trunc int
		if err := rows.Scan(&r.ID, &r.Executor, &r.Command, &r.Status, &r.ExitCode,
			&r.DurationMS, &r.StdoutBytes, &r.StderrBytes, &trunc, &r.Error); err != nil {
			return nil, err
		}
		r.Truncated = trunc == 1
		b.SandboxRuns = append(b.SandboxRuns, r)
	}

	prows, err := s.db.QueryContext(ctx, `
SELECT tool_name, command, action, COALESCE(reason,''), created_at
FROM permission_decision WHERE task_id=?`, taskID)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var d review.PermissionDecision
		var ts string
		if err := prows.Scan(&d.ToolName, &d.Command, &d.Action, &d.Reason, &ts); err != nil {
			return nil, err
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		b.Permissions = append(b.Permissions, d)
	}

	frows, err := s.db.QueryContext(ctx, `
SELECT severity, category, file, line, title, COALESCE(evidence,''),
       COALESCE(recommendation,''), confidence, source, rule_id, bucket
FROM finding WHERE task_id=?`, taskID)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var f review.Finding
		var bucket string
		if err := frows.Scan(&f.Severity, &f.Category, &f.File, &f.Line, &f.Title,
			&f.Evidence, &f.Recommendation, &f.Confidence, &f.Source, &f.RuleID, &bucket); err != nil {
			return nil, err
		}
		if bucket == review.BucketWarning {
			b.Warnings = append(b.Warnings, f)
		} else {
			b.Findings = append(b.Findings, f)
		}
	}

	arows, err := s.db.QueryContext(ctx, `
SELECT name, COALESCE(mime,''), path_or_ref, COALESCE(size_bytes,0), COALESCE(sha256,'')
FROM artifact WHERE task_id=?`, taskID)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var a review.ArtifactRef
		if err := arows.Scan(&a.Name, &a.MIME, &a.PathOrRef, &a.SizeBytes, &a.SHA256); err != nil {
			return nil, err
		}
		b.Artifacts = append(b.Artifacts, a)
	}

	var sevJSON, excJSON string
	err = s.db.QueryRowContext(ctx, `
SELECT total_ms, sandbox_ms, tool_calls, permission_denies, permission_asks,
       finding_count, warning_count, severity_json, exception_json
FROM metrics_summary WHERE task_id=?`, taskID).Scan(
		&b.Metrics.TotalDurationMS, &b.Metrics.SandboxDurationMS, &b.Metrics.ToolCallCount,
		&b.Metrics.PermissionDenyCount, &b.Metrics.PermissionAskCount,
		&b.Metrics.FindingCount, &b.Metrics.WarningCount, &sevJSON, &excJSON,
	)
	if err == nil {
		_ = json.Unmarshal([]byte(sevJSON), &b.Metrics.SeverityDist)
		_ = json.Unmarshal([]byte(excJSON), &b.Metrics.ExceptionDist)
	}

	_ = s.db.QueryRowContext(ctx, `
SELECT COALESCE(report_json,''), COALESCE(report_md,'') FROM report WHERE task_id=?`, taskID).
		Scan(&b.ReportJSON, &b.ReportMD)

	return b, nil
}

// dirOf returns the parent directory of path.
func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			if i == 0 {
				return string(path[0])
			}
			return path[:i]
		}
	}
	return "."
}
