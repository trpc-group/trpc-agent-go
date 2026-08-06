//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlite provides a CGO-free implementation of the storage.Store
// interface built on the pure-Go SQLite driver modernc.org/sqlite.
//
// Using a pure-Go driver avoids the C compiler and libsqlite3 link-time
// dependency that github.com/mattn/go-sqlite3 requires, which matters for
// the CR agent example because it is intended to run out of the box on
// machines without a C toolchain.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// Compile-time interface check: changing the storage.Store signature
// without updating this file will fail the build.
var _ storage.Store = (*Store)(nil)

const (
	// defaultDSN is the connection string used when New is called with
	// an empty DSN. The WAL journal mode is chosen because it permits
	// concurrent readers with a single writer, which matches the CR
	// agent's typical read-heavy dashboard workload.
	defaultDSN = "file:./cr_agent.db?_pragma=journal_mode(WAL)"

	// severityRank maps severity strings to a numeric rank used to
	// order findings in GetFindings. Higher rank = more severe.
	severityRankCritical = 5
	severityRankHigh     = 4
	severityRankMedium   = 3
	severityRankLow      = 2
	severityRankWarning  = 1
	severityRankDefault  = 0
)

// sql schema templates. The schema is intentionally flat — one row per
// top-level entity, with JSON-free columns so operators can query the
// store directly with standard SQL tooling.
const (
	sqlCreateTasksTable = `
CREATE TABLE IF NOT EXISTS cr_tasks (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  diff_added INTEGER NOT NULL DEFAULT 0,
  diff_deleted INTEGER NOT NULL DEFAULT 0,
  diff_files INTEGER NOT NULL DEFAULT 0,
  diff_hash TEXT NOT NULL DEFAULT '',
  diff_preview TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL DEFAULT 0,
  completed_at INTEGER,
  total_duration_ms INTEGER NOT NULL DEFAULT 0,
  sandbox_duration_ms INTEGER NOT NULL DEFAULT 0,
  tool_calls INTEGER NOT NULL DEFAULT 0,
  permission_denials INTEGER NOT NULL DEFAULT 0,
  findings_count INTEGER NOT NULL DEFAULT 0,
  warnings_count INTEGER NOT NULL DEFAULT 0,
  error_msg TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);`

	sqlCreateTasksTaskIDIndex = `
CREATE INDEX IF NOT EXISTS idx_cr_tasks_created_at
ON cr_tasks(created_at DESC);`

	sqlCreateSandboxRunsTable = `
CREATE TABLE IF NOT EXISTS cr_sandbox_runs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  tool_name TEXT NOT NULL DEFAULT '',
  command TEXT NOT NULL DEFAULT '',
  stdout_trunc TEXT NOT NULL DEFAULT '',
  stderr_trunc TEXT NOT NULL DEFAULT '',
  exit_code INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  timed_out INTEGER NOT NULL DEFAULT 0,
  output_bytes INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);`

	sqlCreateSandboxRunsTaskIDIndex = `
CREATE INDEX IF NOT EXISTS idx_cr_sandbox_runs_task_id
ON cr_sandbox_runs(task_id, created_at);`

	sqlCreateFindingsTable = `
CREATE TABLE IF NOT EXISTS cr_findings (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  severity TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT '',
  file TEXT NOT NULL DEFAULT '',
  line INTEGER NOT NULL DEFAULT 0,
  title TEXT NOT NULL DEFAULT '',
  evidence TEXT NOT NULL DEFAULT '',
  recommendation TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  source TEXT NOT NULL DEFAULT '',
  rule_id TEXT NOT NULL DEFAULT '',
  needs_human_review INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);`

	sqlCreateFindingsTaskIDIndex = `
CREATE INDEX IF NOT EXISTS idx_cr_findings_task_id
ON cr_findings(task_id);`
)

// Store is the SQLite-backed implementation of storage.Store.
//
// Callers should construct via New and then call Init. After Init
// returns successfully, the Store is safe for concurrent use from
// multiple goroutines (database/sql pools handles internally).
type Store struct {
	// db is the underlying connection pool. Nil until Init runs.
	db *sql.DB

	// dsn is the SQLite connection string used in Init. Stored so
	// callers that construct via New("") can still inspect which DSN
	// was resolved.
	DSN string
}

// New returns a Store configured with the given SQLite DSN.
//
// If dsn is empty, New falls back to defaultDSN which targets a local
// cr_agent.db file in the current working directory with WAL journal
// mode enabled.
//
// New does not open or validate the connection; call Init to perform
// schema creation and connectivity checks.
func New(dsn string) *Store {
	if dsn == "" {
		dsn = defaultDSN
	}
	return &Store{DSN: dsn}
}

// Init opens the underlying SQLite database, applies the WAL pragmas
// required for concurrent workloads, and creates any missing tables
// and indexes.
//
// Init is not safe to call concurrently and must be called exactly once
// before any other Store method.
func (s *Store) Init(ctx context.Context) error {
	if s.db != nil {
		return fmt.Errorf("store already initialized")
	}

	db, err := sql.Open("sqlite", s.DSN)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	s.db = db

	// Configure the connection pool for a local SQLite store. A single
	// writer is the SQLite limit anyway, so we cap conns low to avoid
	// pointless overhead.
	s.db.SetMaxOpenConns(8)
	s.db.SetMaxIdleConns(4)
	s.db.SetConnMaxLifetime(time.Hour)

	// Verify the connection works under ctx.
	if err := s.db.PingContext(ctx); err != nil {
		_ = s.db.Close()
		s.db = nil
		return fmt.Errorf("ping sqlite: %w", err)
	}

	statements := []string{
		sqlCreateTasksTable,
		sqlCreateTasksTaskIDIndex,
		sqlCreateSandboxRunsTable,
		sqlCreateSandboxRunsTaskIDIndex,
		sqlCreateFindingsTable,
		sqlCreateFindingsTaskIDIndex,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec schema statement: %w", err)
		}
	}

	return nil
}

// Close closes the underlying *sql.DB. Safe to call after Init even
// when Init returned an error; multiple Close calls are no-ops.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// SaveTask upserts a ReviewTask row. The task's ID must be non-empty.
func (s *Store) SaveTask(ctx context.Context, task *types.ReviewTask) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if task.ID == "" {
		return fmt.Errorf("task id is empty")
	}
	if err := s.ready(); err != nil {
		return err
	}

	const stmt = `
INSERT INTO cr_tasks (
  id, status,
  diff_added, diff_deleted, diff_files, diff_hash, diff_preview,
  started_at, completed_at,
  total_duration_ms, sandbox_duration_ms,
  tool_calls, permission_denials, findings_count, warnings_count,
  error_msg, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  status = excluded.status,
  diff_added = excluded.diff_added,
  diff_deleted = excluded.diff_deleted,
  diff_files = excluded.diff_files,
  diff_hash = excluded.diff_hash,
  diff_preview = excluded.diff_preview,
  started_at = excluded.started_at,
  completed_at = excluded.completed_at,
  total_duration_ms = excluded.total_duration_ms,
  sandbox_duration_ms = excluded.sandbox_duration_ms,
  tool_calls = excluded.tool_calls,
  permission_denials = excluded.permission_denials,
  findings_count = excluded.findings_count,
  warnings_count = excluded.warnings_count,
  error_msg = excluded.error_msg;
`
	var completedAt *int64
	if task.CompletedAt != nil {
		ts := task.CompletedAt.UnixMilli()
		completedAt = &ts
	}

	_, err := s.db.ExecContext(ctx, stmt,
		task.ID,
		string(task.Status),
		task.Input.AddedLines,
		task.Input.DeletedLines,
		task.Input.FilesChanged,
		task.Input.DiffHash,
		task.Input.DiffPreview,
		task.StartedAt.UnixMilli(),
		completedAt,
		task.TotalDurationMs,
		task.SandboxDurationMs,
		task.ToolCalls,
		task.PermissionDenials,
		task.FindingsCount,
		task.WarningsCount,
		task.ErrorMsg,
		task.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save task %s: %w", task.ID, err)
	}
	return nil
}

// GetTask retrieves a single task by ID. Returns a not-found error
// wrapping sql.ErrNoRows when the ID is unknown.
func (s *Store) GetTask(ctx context.Context, id string) (*types.ReviewTask, error) {
	if id == "" {
		return nil, fmt.Errorf("task id is empty")
	}
	if err := s.ready(); err != nil {
		return nil, err
	}

	const stmt = `
SELECT
  id, status,
  diff_added, diff_deleted, diff_files, diff_hash, diff_preview,
  started_at, completed_at,
  total_duration_ms, sandbox_duration_ms,
  tool_calls, permission_denials, findings_count, warnings_count,
  error_msg, created_at
FROM cr_tasks
WHERE id = ?;`

	row := s.db.QueryRowContext(ctx, stmt, id)
	var (
		statusStr   string
		startedAtMs int64
		createdAtMs int64
		completedAt sql.NullInt64

		addedLines   int
		deletedLines int
		filesChanged int
		diffHash     string
		diffPreview  string
	)
	task := &types.ReviewTask{}
	err := row.Scan(
		&task.ID,
		&statusStr,
		&addedLines,
		&deletedLines,
		&filesChanged,
		&diffHash,
		&diffPreview,
		&startedAtMs,
		&completedAt,
		&task.TotalDurationMs,
		&task.SandboxDurationMs,
		&task.ToolCalls,
		&task.PermissionDenials,
		&task.FindingsCount,
		&task.WarningsCount,
		&task.ErrorMsg,
		&createdAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("task %s not found: %w", id, err)
		}
		return nil, fmt.Errorf("scan task %s: %w", id, err)
	}
	task.Status = types.ReviewTaskStatus(statusStr)
	task.StartedAt = time.UnixMilli(startedAtMs)
	task.CreatedAt = time.UnixMilli(createdAtMs)
	if completedAt.Valid {
		t := time.UnixMilli(completedAt.Int64)
		task.CompletedAt = &t
	}
	task.Input = types.DiffSummary{
		AddedLines:   addedLines,
		DeletedLines: deletedLines,
		FilesChanged: filesChanged,
		DiffHash:     diffHash,
		DiffPreview:  diffPreview,
	}
	return task, nil
}

// ListTasks returns paginated tasks ordered by created_at DESC (newest
// first). limit is clamped to at least 1; offset must be >= 0.
func (s *Store) ListTasks(ctx context.Context, limit, offset int) ([]*types.ReviewTask, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = 1
	}
	if offset < 0 {
		offset = 0
	}

	const stmt = `
SELECT
  id, status,
  diff_added, diff_deleted, diff_files, diff_hash, diff_preview,
  started_at, completed_at,
  total_duration_ms, sandbox_duration_ms,
  tool_calls, permission_denials, findings_count, warnings_count,
  error_msg, created_at
FROM cr_tasks
ORDER BY created_at DESC
LIMIT ? OFFSET ?;`

	rows, err := s.db.QueryContext(ctx, stmt, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]*types.ReviewTask, 0, limit)
	for rows.Next() {
		var (
			statusStr   string
			startedAtMs int64
			createdAtMs int64
			completedAt sql.NullInt64

			addedLines   int
			deletedLines int
			filesChanged int
			diffHash     string
			diffPreview  string
		)
		task := &types.ReviewTask{}
		if err := rows.Scan(
			&task.ID,
			&statusStr,
			&addedLines,
			&deletedLines,
			&filesChanged,
			&diffHash,
			&diffPreview,
			&startedAtMs,
			&completedAt,
			&task.TotalDurationMs,
			&task.SandboxDurationMs,
			&task.ToolCalls,
			&task.PermissionDenials,
			&task.FindingsCount,
			&task.WarningsCount,
			&task.ErrorMsg,
			&createdAtMs,
		); err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}
		task.Status = types.ReviewTaskStatus(statusStr)
		task.StartedAt = time.UnixMilli(startedAtMs)
		task.CreatedAt = time.UnixMilli(createdAtMs)
		if completedAt.Valid {
			t := time.UnixMilli(completedAt.Int64)
			task.CompletedAt = &t
		}
		task.Input = types.DiffSummary{
			AddedLines:   addedLines,
			DeletedLines: deletedLines,
			FilesChanged: filesChanged,
			DiffHash:     diffHash,
			DiffPreview:  diffPreview,
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task rows: %w", err)
	}
	return tasks, nil
}

// SaveSandboxRun inserts a sandbox run. Generates a UUID for run.ID if
// it is empty on input.
func (s *Store) SaveSandboxRun(ctx context.Context, run *types.SandboxRun) error {
	if run == nil {
		return fmt.Errorf("sandbox run is nil")
	}
	if run.TaskID == "" {
		return fmt.Errorf("sandbox run task_id is empty")
	}
	if err := s.ready(); err != nil {
		return err
	}
	if run.ID == "" {
		run.ID = uuid.NewString()
	}

	timedOutInt := 0
	if run.TimedOut {
		timedOutInt = 1
	}

	const stmt = `
INSERT INTO cr_sandbox_runs (
  id, task_id, tool_name, command,
  stdout_trunc, stderr_trunc, exit_code, duration_ms,
  timed_out, output_bytes, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`

	_, err := s.db.ExecContext(ctx, stmt,
		run.ID,
		run.TaskID,
		run.ToolName,
		run.Command,
		run.StdoutTruncated,
		run.StderrTruncated,
		run.ExitCode,
		run.DurationMs,
		timedOutInt,
		run.OutputBytes,
		run.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save sandbox run %s: %w", run.ID, err)
	}
	return nil
}

// GetSandboxRuns returns every sandbox run for a task, ordered by
// created_at ASC so callers can replay chronologically.
func (s *Store) GetSandboxRuns(ctx context.Context, taskID string) ([]*types.SandboxRun, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task id is empty")
	}
	if err := s.ready(); err != nil {
		return nil, err
	}

	const stmt = `
SELECT
  id, task_id, tool_name, command,
  stdout_trunc, stderr_trunc, exit_code, duration_ms,
  timed_out, output_bytes, created_at
FROM cr_sandbox_runs
WHERE task_id = ?
ORDER BY created_at ASC;`

	rows, err := s.db.QueryContext(ctx, stmt, taskID)
	if err != nil {
		return nil, fmt.Errorf("query sandbox runs for %s: %w", taskID, err)
	}
	defer rows.Close()

	runs := make([]*types.SandboxRun, 0, 8)
	for rows.Next() {
		var (
			createdAtMs int64
			timedOutInt int
		)
		run := &types.SandboxRun{}
		if err := rows.Scan(
			&run.ID,
			&run.TaskID,
			&run.ToolName,
			&run.Command,
			&run.StdoutTruncated,
			&run.StderrTruncated,
			&run.ExitCode,
			&run.DurationMs,
			&timedOutInt,
			&run.OutputBytes,
			&createdAtMs,
		); err != nil {
			return nil, fmt.Errorf("scan sandbox run row: %w", err)
		}
		run.TimedOut = timedOutInt != 0
		run.CreatedAt = time.UnixMilli(createdAtMs)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandbox run rows: %w", err)
	}
	return runs, nil
}

// SaveFinding inserts a finding attached to the given taskID. Generates
// a UUID for f.ID if it is empty on input.
func (s *Store) SaveFinding(ctx context.Context, taskID string, f *types.Finding) error {
	if taskID == "" {
		return fmt.Errorf("task id is empty")
	}
	if f == nil {
		return fmt.Errorf("finding is nil")
	}
	if err := s.ready(); err != nil {
		return err
	}
	if f.ID == "" {
		f.ID = uuid.NewString()
	}

	needsReviewInt := 0
	if f.NeedsHumanReview {
		needsReviewInt = 1
	}

	const stmt = `
INSERT INTO cr_findings (
  id, task_id, severity, category, file, line,
  title, evidence, recommendation, confidence,
  source, rule_id, needs_human_review, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`

	_, err := s.db.ExecContext(ctx, stmt,
		f.ID,
		taskID,
		string(f.Severity),
		string(f.Category),
		f.File,
		f.Line,
		f.Title,
		f.Evidence,
		f.Recommendation,
		f.Confidence,
		f.Source,
		f.RuleID,
		needsReviewInt,
		f.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save finding %s for task %s: %w", f.ID, taskID, err)
	}
	return nil
}

// GetFindings returns findings for a task, ordered by severity DESC
// (critical first) then created_at ASC. The ordering is computed in
// SQL via a CASE expression so callers get dashboard-friendly order
// directly.
func (s *Store) GetFindings(ctx context.Context, taskID string) ([]*types.Finding, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task id is empty")
	}
	if err := s.ready(); err != nil {
		return nil, err
	}

	stmt := fmt.Sprintf(`
SELECT
  id, task_id, severity, category, file, line,
  title, evidence, recommendation, confidence,
  source, rule_id, needs_human_review, created_at
FROM cr_findings
WHERE task_id = ?
ORDER BY
  CASE severity
    WHEN 'critical' THEN %d
    WHEN 'high'     THEN %d
    WHEN 'medium'   THEN %d
    WHEN 'low'      THEN %d
    WHEN 'warning'  THEN %d
    ELSE %d
  END DESC,
  created_at ASC;`,
		severityRankCritical,
		severityRankHigh,
		severityRankMedium,
		severityRankLow,
		severityRankWarning,
		severityRankDefault,
	)

	rows, err := s.db.QueryContext(ctx, stmt, taskID)
	if err != nil {
		return nil, fmt.Errorf("query findings for %s: %w", taskID, err)
	}
	defer rows.Close()

	findings := make([]*types.Finding, 0, 8)
	for rows.Next() {
		var (
			severityStr    string
			categoryStr    string
			createdAtMs    int64
			needsReviewInt int
		)
		f := &types.Finding{}
		if err := rows.Scan(
			&f.ID,
			&taskID,
			&severityStr,
			&categoryStr,
			&f.File,
			&f.Line,
			&f.Title,
			&f.Evidence,
			&f.Recommendation,
			&f.Confidence,
			&f.Source,
			&f.RuleID,
			&needsReviewInt,
			&createdAtMs,
		); err != nil {
			return nil, fmt.Errorf("scan finding row: %w", err)
		}
		f.Severity = types.Severity(severityStr)
		f.Category = types.Category(categoryStr)
		f.NeedsHumanReview = needsReviewInt != 0
		f.CreatedAt = time.UnixMilli(createdAtMs)
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate finding rows: %w", err)
	}
	return findings, nil
}

// ready returns a descriptive error if Init has not been called yet,
// so every public method fails fast with a useful message instead of
// a nil-db panic.
func (s *Store) ready() error {
	if s.db == nil {
		return fmt.Errorf("store not initialized: call Init before other methods")
	}
	return nil
}
