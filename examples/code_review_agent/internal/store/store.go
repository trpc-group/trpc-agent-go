//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package store implements the SQLite-backed persistent store for the code
// review agent.
//
// The store uses modernc.org/sqlite, a pure-Go (CGO-free) SQLite driver that
// registers itself under the driver name "sqlite" via a blank import. Foreign
// keys are explicitly enabled in Init because modernc.org/sqlite defaults
// PRAGMA foreign_keys to OFF.
//
// SaveTaskReport writes the entire TaskReport aggregate inside a single
// transaction so that either the whole task is persisted or nothing is. On any
// error the transaction is rolled back, leaving the database untouched.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed" // blank import for the go:embed directive
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	_ "modernc.org/sqlite" // blank import registers the "sqlite" driver
)

// schemaSQL is the embedded contents of schema.sql. Embedding keeps the schema
// in sync with the binary without requiring an external file at runtime.
//
//go:embed schema.sql
var schemaSQL string

// ErrTaskNotFound is returned by LoadTaskReport when no review_task row exists
// for the requested task_id.
var ErrTaskNotFound = errors.New("store: task not found")

// Store is the persistence contract for the code review agent.
type Store interface {
	// Init opens the database, enables foreign keys and applies schema.sql.
	// It must be called once before any other method.
	Init(ctx context.Context) error
	// Close releases the underlying database handle.
	Close() error
	// SaveTaskReport persists a TaskReport and all of its child rows inside a
	// single transaction.
	SaveTaskReport(ctx context.Context, r TaskReport) error
	// LoadTaskReport reassembles a TaskReport from the database. It returns
	// ErrTaskNotFound if the task_id does not exist.
	LoadTaskReport(ctx context.Context, taskID string) (*TaskReport, error)
	// ListTasks returns the most recent task summaries, newest first.
	ListTasks(ctx context.Context, limit int) ([]TaskSummary, error)
}

// sqliteStore is the concrete Store backed by modernc.org/sqlite.
type sqliteStore struct {
	db   *sql.DB
	path string
}

// New returns a Store backed by the SQLite database at path. The path ":memory:"
// opens an in-memory database (useful for tests). Init must be called before
// the store is used.
func New(path string) Store {
	return &sqliteStore{path: path}
}

// Init opens the database, enables foreign keys and applies schema.sql. It is
// safe to call multiple times: a repeated call is a no-op if the database
// handle is already open, preventing connection leaks. Every CREATE TABLE and
// CREATE INDEX statement uses IF NOT EXISTS, so re-applying the schema is
// idempotent.
func (s *sqliteStore) Init(ctx context.Context) error {
	if s.db != nil {
		return nil
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return fmt.Errorf("store: open %q: %w", s.path, err)
	}
	// SQLite serializes writes; limiting the pool to a single connection
	// avoids "database is locked" errors under concurrent access.
	db.SetMaxOpenConns(1)
	s.db = db
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys=ON;`); err != nil {
		_ = s.db.Close()
		s.db = nil
		return fmt.Errorf("store: enable foreign keys: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		_ = s.db.Close()
		s.db = nil
		return fmt.Errorf("store: apply schema: %w", err)
	}
	return nil
}

// Close releases the database handle. It is a no-op if the store was never
// initialised.
func (s *sqliteStore) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// SaveTaskReport persists the entire TaskReport aggregate inside a single
// transaction. The review_task row is inserted first, then each child table in
// order. On any error the transaction is rolled back so no partial rows
// survive.
//
// Finding inserts use INSERT OR IGNORE so that a repeated fingerprint within
// the same task is silently deduplicated rather than aborting the transaction.
func (s *sqliteStore) SaveTaskReport(ctx context.Context, r TaskReport) error {
	if s.db == nil {
		return errors.New("store: not initialised")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := insertReviewTask(ctx, tx, r.Task); err != nil {
		return err
	}
	for i := range r.Findings {
		if err := insertFinding(ctx, tx, r.Findings[i]); err != nil {
			return err
		}
	}
	for i := range r.SandboxRuns {
		if err := insertSandboxRun(ctx, tx, r.SandboxRuns[i]); err != nil {
			return err
		}
	}
	for i := range r.Permissions {
		if err := insertPermissionDecision(ctx, tx, r.Permissions[i]); err != nil {
			return err
		}
	}
	for i := range r.Artifacts {
		if err := insertArtifact(ctx, tx, r.Artifacts[i]); err != nil {
			return err
		}
	}
	if err := insertReport(ctx, tx, r.Report); err != nil {
		return err
	}
	if err := insertMetrics(ctx, tx, r.Metrics); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	committed = true
	return nil
}

// insertReviewTask inserts the top-level review_task row.
func insertReviewTask(ctx context.Context, tx *sql.Tx, t ReviewTask) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO review_task
(task_id, created_at, repo_path, diff_source, status, conclusion,
 total_duration_ms, sandbox_duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
		t.TaskID, t.CreatedAt, t.RepoPath, t.DiffSource, t.Status, t.Conclusion,
		t.TotalDurationMs, t.SandboxDurationMs)
	if err != nil {
		return fmt.Errorf("store: insert review_task: %w", err)
	}
	return nil
}

// insertFinding inserts a single finding. INSERT OR IGNORE deduplicates on the
// UNIQUE(fingerprint) constraint so repeated detections do not fail the save.
func insertFinding(ctx context.Context, tx *sql.Tx, f Finding) error {
	_, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO finding
(task_id, severity, category, file, line, title, evidence, recommendation,
 confidence, source, rule_id, fingerprint, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		f.TaskID, f.Severity, f.Category, f.File, f.Line, f.Title, f.Evidence,
		f.Recommendation, f.Confidence, f.Source, f.RuleID, f.Fingerprint, f.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: insert finding: %w", err)
	}
	return nil
}

// insertSandboxRun inserts a single sandbox_run row. exit_code/stdout/stderr
// are nullable and are passed through as sql.Null* values.
func insertSandboxRun(ctx context.Context, tx *sql.Tx, sr SandboxRun) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO sandbox_run
(task_id, command, status, exit_code, duration_ms, timed_out, truncated,
 stdout, stderr, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		sr.TaskID, sr.Command, sr.Status, sr.ExitCode, sr.DurationMs,
		boolToInt(sr.TimedOut), boolToInt(sr.Truncated), sr.Stdout, sr.Stderr,
		sr.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: insert sandbox_run: %w", err)
	}
	return nil
}

// insertPermissionDecision inserts a single permission_decision row.
func insertPermissionDecision(ctx context.Context, tx *sql.Tx, p PermissionDecision) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO permission_decision
(task_id, command, action, reason, created_at)
VALUES (?, ?, ?, ?, ?);`,
		p.TaskID, p.Command, p.Action, p.Reason, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: insert permission_decision: %w", err)
	}
	return nil
}

// insertArtifact inserts a single artifact row.
func insertArtifact(ctx context.Context, tx *sql.Tx, a Artifact) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO artifact
(task_id, name, path, size_bytes, created_at)
VALUES (?, ?, ?, ?, ?);`,
		a.TaskID, a.Name, a.Path, a.SizeBytes, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: insert artifact: %w", err)
	}
	return nil
}

// insertReport inserts the report row. The UNIQUE(task_id) constraint means a
// task can have at most one report.
func insertReport(ctx context.Context, tx *sql.Tx, r ReportRow) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO report
(task_id, json_path, markdown_path, created_at)
VALUES (?, ?, ?, ?);`,
		r.TaskID, r.JSONPath, r.MarkdownPath, r.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: insert report: %w", err)
	}
	return nil
}

// insertMetrics inserts the telemetry_metrics row. The UNIQUE(task_id)
// constraint means a task can have at most one metrics row.
func insertMetrics(ctx context.Context, tx *sql.Tx, m TelemetryMetrics) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO telemetry_metrics
(task_id, total_duration_ms, sandbox_duration_ms, tool_calls,
 permission_blocked_count, finding_count, severity_critical, severity_high,
 severity_medium, severity_low, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		m.TaskID, m.TotalDurationMs, m.SandboxDurationMs, m.ToolCalls,
		m.PermissionBlockedCount, m.FindingCount, m.SeverityCritical,
		m.SeverityHigh, m.SeverityMedium, m.SeverityLow, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: insert telemetry_metrics: %w", err)
	}
	return nil
}

// LoadTaskReport reassembles a TaskReport for taskID. It returns ErrTaskNotFound
// if the review_task row is missing, or an error if any child row references a
// mismatched task_id (a defensive check against data corruption).
func (s *sqliteStore) LoadTaskReport(ctx context.Context, taskID string) (*TaskReport, error) {
	if s.db == nil {
		return nil, errors.New("store: not initialised")
	}
	task, err := loadReviewTask(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	findings, err := loadFindings(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	runs, err := loadSandboxRuns(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	perms, err := loadPermissions(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	arts, err := loadArtifacts(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	rep, err := loadReport(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	metrics, err := loadMetrics(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	return &TaskReport{
		Task:        *task,
		Findings:    findings,
		SandboxRuns: runs,
		Permissions: perms,
		Artifacts:   arts,
		Report:      rep,
		Metrics:     metrics,
	}, nil
}

// loadReviewTask loads the top-level review_task row.
func loadReviewTask(ctx context.Context, db *sql.DB, taskID string) (*ReviewTask, error) {
	row := db.QueryRowContext(ctx, `
SELECT task_id, created_at, repo_path, diff_source, status, conclusion,
       total_duration_ms, sandbox_duration_ms
FROM review_task WHERE task_id = ?;`, taskID)
	var t ReviewTask
	err := row.Scan(&t.TaskID, &t.CreatedAt, &t.RepoPath, &t.DiffSource, &t.Status,
		&t.Conclusion, &t.TotalDurationMs, &t.SandboxDurationMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan review_task: %w", err)
	}
	return &t, nil
}

// loadFindings loads all findings for taskID, ordered by id for stable output.
func loadFindings(ctx context.Context, db *sql.DB, taskID string) ([]Finding, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, task_id, severity, category, file, line, title, evidence,
       recommendation, confidence, source, rule_id, fingerprint, created_at
FROM finding WHERE task_id = ? ORDER BY id;`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: query finding: %w", err)
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.ID, &f.TaskID, &f.Severity, &f.Category, &f.File,
			&f.Line, &f.Title, &f.Evidence, &f.Recommendation, &f.Confidence,
			&f.Source, &f.RuleID, &f.Fingerprint, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan finding: %w", err)
		}
		if f.TaskID != taskID {
			return nil, fmt.Errorf("store: finding task_id mismatch: got %q want %q", f.TaskID, taskID)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate finding: %w", err)
	}
	return out, nil
}

// loadSandboxRuns loads all sandbox_runs for taskID.
func loadSandboxRuns(ctx context.Context, db *sql.DB, taskID string) ([]SandboxRun, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, task_id, command, status, exit_code, duration_ms, timed_out,
       truncated, stdout, stderr, created_at
FROM sandbox_run WHERE task_id = ? ORDER BY id;`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: query sandbox_run: %w", err)
	}
	defer rows.Close()
	var out []SandboxRun
	for rows.Next() {
		var sr SandboxRun
		var timedOut, truncated int
		if err := rows.Scan(&sr.ID, &sr.TaskID, &sr.Command, &sr.Status, &sr.ExitCode,
			&sr.DurationMs, &timedOut, &truncated, &sr.Stdout, &sr.Stderr,
			&sr.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan sandbox_run: %w", err)
		}
		if sr.TaskID != taskID {
			return nil, fmt.Errorf("store: sandbox_run task_id mismatch: got %q want %q", sr.TaskID, taskID)
		}
		sr.TimedOut = timedOut != 0
		sr.Truncated = truncated != 0
		out = append(out, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate sandbox_run: %w", err)
	}
	return out, nil
}

// loadPermissions loads all permission_decisions for taskID.
func loadPermissions(ctx context.Context, db *sql.DB, taskID string) ([]PermissionDecision, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, task_id, command, action, reason, created_at
FROM permission_decision WHERE task_id = ? ORDER BY id;`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: query permission_decision: %w", err)
	}
	defer rows.Close()
	var out []PermissionDecision
	for rows.Next() {
		var p PermissionDecision
		if err := rows.Scan(&p.ID, &p.TaskID, &p.Command, &p.Action, &p.Reason,
			&p.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan permission_decision: %w", err)
		}
		if p.TaskID != taskID {
			return nil, fmt.Errorf("store: permission_decision task_id mismatch: got %q want %q", p.TaskID, taskID)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate permission_decision: %w", err)
	}
	return out, nil
}

// loadArtifacts loads all artifacts for taskID.
func loadArtifacts(ctx context.Context, db *sql.DB, taskID string) ([]Artifact, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, task_id, name, path, size_bytes, created_at
FROM artifact WHERE task_id = ? ORDER BY id;`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: query artifact: %w", err)
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Name, &a.Path, &a.SizeBytes,
			&a.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan artifact: %w", err)
		}
		if a.TaskID != taskID {
			return nil, fmt.Errorf("store: artifact task_id mismatch: got %q want %q", a.TaskID, taskID)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate artifact: %w", err)
	}
	return out, nil
}

// loadReport loads the report row for taskID. Missing rows yield a zero-value
// ReportRow, mirroring the optional nature of the report.
func loadReport(ctx context.Context, db *sql.DB, taskID string) (ReportRow, error) {
	row := db.QueryRowContext(ctx, `
SELECT id, task_id, json_path, markdown_path, created_at
FROM report WHERE task_id = ?;`, taskID)
	var r ReportRow
	err := row.Scan(&r.ID, &r.TaskID, &r.JSONPath, &r.MarkdownPath, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReportRow{}, nil
	}
	if err != nil {
		return ReportRow{}, fmt.Errorf("store: scan report: %w", err)
	}
	if r.TaskID != taskID {
		return ReportRow{}, fmt.Errorf("store: report task_id mismatch: got %q want %q", r.TaskID, taskID)
	}
	return r, nil
}

// loadMetrics loads the telemetry_metrics row for taskID. Missing rows yield a
// zero-value TelemetryMetrics.
func loadMetrics(ctx context.Context, db *sql.DB, taskID string) (TelemetryMetrics, error) {
	row := db.QueryRowContext(ctx, `
SELECT id, task_id, total_duration_ms, sandbox_duration_ms, tool_calls,
       permission_blocked_count, finding_count, severity_critical,
       severity_high, severity_medium, severity_low, created_at
FROM telemetry_metrics WHERE task_id = ?;`, taskID)
	var m TelemetryMetrics
	err := row.Scan(&m.ID, &m.TaskID, &m.TotalDurationMs, &m.SandboxDurationMs,
		&m.ToolCalls, &m.PermissionBlockedCount, &m.FindingCount,
		&m.SeverityCritical, &m.SeverityHigh, &m.SeverityMedium, &m.SeverityLow,
		&m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TelemetryMetrics{}, nil
	}
	if err != nil {
		return TelemetryMetrics{}, fmt.Errorf("store: scan telemetry_metrics: %w", err)
	}
	if m.TaskID != taskID {
		return TelemetryMetrics{}, fmt.Errorf("store: telemetry_metrics task_id mismatch: got %q want %q", m.TaskID, taskID)
	}
	return m, nil
}

// ListTasks returns the most recent task summaries, newest first. limit is the
// maximum number of rows to return; a non-positive limit is treated as 1.
func (s *sqliteStore) ListTasks(ctx context.Context, limit int) ([]TaskSummary, error) {
	if s.db == nil {
		return nil, errors.New("store: not initialised")
	}
	if limit < 1 {
		limit = 1
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.task_id, t.created_at, t.status, t.conclusion,
       (SELECT count(*) FROM finding f WHERE f.task_id = t.task_id) AS finding_count
FROM review_task t
ORDER BY t.created_at DESC
LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: query tasks: %w", err)
	}
	defer rows.Close()
	var out []TaskSummary
	for rows.Next() {
		var ts TaskSummary
		if err := rows.Scan(&ts.TaskID, &ts.CreatedAt, &ts.Status, &ts.Conclusion,
			&ts.FindingCount); err != nil {
			return nil, fmt.Errorf("store: scan task summary: %w", err)
		}
		out = append(out, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate task summary: %w", err)
	}
	return out, nil
}

// NewTaskID returns a run-unique task id of the form
// "cr-<UTC timestamp>-<repoPath short hash>-<4 hex nonce>". The timestamp is
// formatted as "20060102-150405" (UTC), the short hash is the first n hex
// characters of a stable FNV-1a hash of repoPath, and the nonce is 4 random hex
// characters drawn from crypto/rand. The nonce guarantees uniqueness across
// multiple runs that share the same day and repository.
func NewTaskID(repoPath string) string {
	ts := time.Now().UTC().Format("20060102-150405")
	return fmt.Sprintf("cr-%s-%s-%s", ts, shortHash(repoPath, 8), nonce(4))
}

// shortHash returns the first n hex characters of the FNV-1a hash of s. It is
// deterministic for a given input, which makes task ids for the same repo
// visually similar across runs while still being collision-resistant.
func shortHash(s string, n int) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	hex := fmt.Sprintf("%08x", h.Sum32())
	if n > len(hex) {
		n = len(hex)
	}
	return hex[:n]
}

// nonce returns n random lowercase hex characters. If crypto/rand fails (which
// is extraordinarily rare), the function falls back to a time-derived value so
// that NewTaskID never panics.
func nonce(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// Fall back to a time-derived value so callers never see a panic.
		now := time.Now().UnixNano()
		const hexdigits = "0123456789abcdef"
		for i := 0; i < n; i++ {
			buf[i] = hexdigits[(now>>(uint(i)*4))&0xf]
		}
		return string(buf)
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = hexdigits[b&0xf]
	}
	return string(out)
}

// boolToInt converts a bool to the 0/1 integer SQLite stores for boolean
// columns. Keeping this in one place makes the encoding uniform.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
