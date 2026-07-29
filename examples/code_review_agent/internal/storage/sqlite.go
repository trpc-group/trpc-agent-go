// Package storage provides database persistence for review data.
// Default backend is SQLite; PostgreSQL/MySQL are supported via the Storage interface.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	// Use pure-Go SQLite driver — no CGO required, works on all platforms.
	// Swap to github.com/mattn/go-sqlite3 if CGO is available and performance matters.
	_ "modernc.org/sqlite"
)

// ── Row types (mirror types.Finding etc., decoupled for DB layer) ──

// TaskRow maps to review_task table.
type TaskRow struct {
	ID              string
	Status          string
	InputType       string
	InputSource     string
	InputDiffHash   string
	BaseRef         string
	TotalFiles      int
	TotalHunks      int
	ModelMode       string
	ErrorMessage    string
	CreatedAt       string
	StartedAt       string
	CompletedAt     string
	TotalDurationMs int64
}

// FindingRow maps to review_finding table.
type FindingRow struct {
	ID             string
	TaskID         string
	Severity       string
	Category       string
	File           string
	Line           int
	Title          string
	Evidence       string
	Recommendation string
	Confidence     float64
	Source         string
	DecisionKind   string
	RuleID         string
	CreatedAt      string
}

// SandboxRunRow maps to sandbox_run table.
type SandboxRunRow struct {
	ID              string
	TaskID          string
	ExecutorType    string
	CommandName     string
	Command         string
	ExitCode        int
	Stdout          string
	Stderr          string
	DurationMs      int64
	TimedOut        bool
	OutputTruncated bool
	ErrorType       string
	CreatedAt       string
}

// PermissionDecisionRow maps to permission_decision table.
type PermissionDecisionRow struct {
	ID        string
	TaskID    string
	Command   string
	RiskLevel string
	Decision  string
	Reason    string
	DecidedAt string
}

// ArtifactRow maps to review_artifact table.
type ArtifactRow struct {
	ID           string
	TaskID       string
	ArtifactType string
	FilePath     string
	SizeBytes    int64
	ContentHash  string
	CreatedAt    string
}

// ReportRow maps to review_report table.
type ReportRow struct {
	ID                    string
	TaskID                string
	FindingsCount         int
	WarningsCount         int
	SeverityDistribution  string // JSON
	CategoryDistribution  string // JSON
	JSONReportPath        string
	MDReportPath          string
	Summary               string
	CreatedAt             string
}

// MetricRow maps to monitor_metric table.
type MetricRow struct {
	ID                    string
	TaskID                string
	TotalDurationMs       int64
	DiffParseMs           int64
	PermissionFilterMs    int64
	SandboxTotalMs        int64
	RuleEngineMs          int64
	LLMAnalyzerMs         int64
	DedupMs               int64
	ReportGenMs           int64
	StorageMs             int64
	ToolCallsCount        int
	PermissionBlocksCount int
	FindingsCritical      int
	FindingsHigh          int
	FindingsMedium        int
	FindingsLow           int
	FindingsWarning       int
	LLMTokensPrompt       int
	LLMTokensCompletion   int
	LLMTokensTotal        int
	CreatedAt             string
}

// ExceptionRow maps to metrics_exception table.
type ExceptionRow struct {
	ID          string
	TaskID      string
	ErrorType   string
	ErrorCount  int
	ErrorDetail string
	CreatedAt   string
}

// ── Storage interface ──

// Storage is the database abstraction for review persistence.
type Storage interface {
	CreateTask(ctx context.Context, t TaskRow) error
	UpdateTask(ctx context.Context, id string, updates map[string]any) error
	GetTask(ctx context.Context, id string) (*TaskRow, error)

	InsertFindings(ctx context.Context, findings []FindingRow) error
	GetFindingsByTask(ctx context.Context, taskID string) ([]FindingRow, error)

	InsertSandboxRun(ctx context.Context, r SandboxRunRow) error
	GetSandboxRunsByTask(ctx context.Context, taskID string) ([]SandboxRunRow, error)

	InsertPermissionDecisions(ctx context.Context, ds []PermissionDecisionRow) error

	InsertArtifact(ctx context.Context, a ArtifactRow) error

	InsertReport(ctx context.Context, r ReportRow) error
	GetReport(ctx context.Context, taskID string) (*ReportRow, error)

	InsertMetric(ctx context.Context, m MetricRow) error
	InsertExceptions(ctx context.Context, es []ExceptionRow) error

	Close() error
	Ping(ctx context.Context) error
}

// ── SQLite Implementation ──

type sqliteStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewSQLite opens (or creates) a SQLite database and runs migrations.
func NewSQLite(dsn string) (Storage, error) {
	dir := filepath.Dir(dsn)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create data dir %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dsn+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dsn, err)
	}
	db.SetMaxOpenConns(1)

	s := &sqliteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *sqliteStore) migrate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(schema)
	return err
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}

func (s *sqliteStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *sqliteStore) CreateTask(ctx context.Context, t TaskRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO review_task (id, status, input_type, input_source, input_diff_hash, base_ref, total_files, total_hunks, model_mode, error_message, created_at, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Status, t.InputType, t.InputSource, t.InputDiffHash, t.BaseRef,
		t.TotalFiles, t.TotalHunks, t.ModelMode, t.ErrorMessage, t.CreatedAt, t.StartedAt,
	)
	return err
}

func (s *sqliteStore) UpdateTask(ctx context.Context, id string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	setClause := ""
	args := make([]any, 0, len(updates)+1)
	for col, val := range updates {
		if setClause != "" {
			setClause += ", "
		}
		setClause += col + " = ?"
		args = append(args, val)
	}
	args = append(args, id)
	_, err := s.db.ExecContext(ctx, "UPDATE review_task SET "+setClause+" WHERE id = ?", args...)
	return err
}

func (s *sqliteStore) GetTask(ctx context.Context, id string) (*TaskRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, status, input_type, input_source, input_diff_hash, base_ref, total_files, total_hunks, model_mode, error_message, created_at, started_at, completed_at, total_duration_ms
		 FROM review_task WHERE id = ?`, id)
	t := &TaskRow{}
	var startedAt, completedAt sql.NullString
	err := row.Scan(&t.ID, &t.Status, &t.InputType, &t.InputSource, &t.InputDiffHash,
		&t.BaseRef, &t.TotalFiles, &t.TotalHunks, &t.ModelMode, &t.ErrorMessage,
		&t.CreatedAt, &startedAt, &completedAt, &t.TotalDurationMs)
	if err != nil {
		return nil, err
	}
	t.StartedAt = startedAt.String
	t.CompletedAt = completedAt.String
	return t, nil
}

func (s *sqliteStore) InsertFindings(ctx context.Context, findings []FindingRow) error {
	if len(findings) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO review_finding (id, task_id, severity, category, file, line, title, evidence, recommendation, confidence, source, decision_kind, rule_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range findings {
		if _, err := stmt.ExecContext(ctx, f.ID, f.TaskID, f.Severity, f.Category,
			f.File, f.Line, f.Title, f.Evidence, f.Recommendation,
			f.Confidence, f.Source, f.DecisionKind, f.RuleID, f.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqliteStore) GetFindingsByTask(ctx context.Context, taskID string) ([]FindingRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, severity, category, file, line, title, evidence, recommendation, confidence, source, decision_kind, rule_id, created_at
		 FROM review_finding WHERE task_id = ? ORDER BY severity, file, line`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FindingRow
	for rows.Next() {
		var f FindingRow
		if err := rows.Scan(&f.ID, &f.TaskID, &f.Severity, &f.Category, &f.File, &f.Line,
			&f.Title, &f.Evidence, &f.Recommendation, &f.Confidence, &f.Source,
			&f.DecisionKind, &f.RuleID, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *sqliteStore) InsertSandboxRun(ctx context.Context, r SandboxRunRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sandbox_run (id, task_id, executor_type, command_name, command, exit_code, stdout, stderr, duration_ms, timed_out, output_truncated, error_type, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.ExecutorType, r.CommandName, r.Command, r.ExitCode,
		r.Stdout, r.Stderr, r.DurationMs, r.TimedOut, r.OutputTruncated, r.ErrorType, r.CreatedAt,
	)
	return err
}

func (s *sqliteStore) GetSandboxRunsByTask(ctx context.Context, taskID string) ([]SandboxRunRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, executor_type, command_name, command, exit_code, stdout, stderr, duration_ms, timed_out, output_truncated, error_type, created_at
		 FROM sandbox_run WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SandboxRunRow
	for rows.Next() {
		var r SandboxRunRow
		if err := rows.Scan(&r.ID, &r.TaskID, &r.ExecutorType, &r.CommandName, &r.Command,
			&r.ExitCode, &r.Stdout, &r.Stderr, &r.DurationMs, &r.TimedOut,
			&r.OutputTruncated, &r.ErrorType, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqliteStore) InsertPermissionDecisions(ctx context.Context, ds []PermissionDecisionRow) error {
	if len(ds) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO permission_decision (id, task_id, command, risk_level, decision, reason, decided_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, d := range ds {
		if _, err := stmt.ExecContext(ctx, d.ID, d.TaskID, d.Command, d.RiskLevel,
			d.Decision, d.Reason, d.DecidedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqliteStore) InsertArtifact(ctx context.Context, a ArtifactRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO review_artifact (id, task_id, artifact_type, file_path, size_bytes, content_hash, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TaskID, a.ArtifactType, a.FilePath, a.SizeBytes, a.ContentHash, a.CreatedAt,
	)
	return err
}

func (s *sqliteStore) InsertReport(ctx context.Context, r ReportRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO review_report (id, task_id, findings_count, warnings_count, severity_distribution, category_distribution, json_report_path, md_report_path, summary, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.FindingsCount, r.WarningsCount, r.SeverityDistribution,
		r.CategoryDistribution, r.JSONReportPath, r.MDReportPath, r.Summary, r.CreatedAt,
	)
	return err
}

func (s *sqliteStore) GetReport(ctx context.Context, taskID string) (*ReportRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, task_id, findings_count, warnings_count, severity_distribution, category_distribution, json_report_path, md_report_path, summary, created_at
		 FROM review_report WHERE task_id = ?`, taskID)
	r := &ReportRow{}
	err := row.Scan(&r.ID, &r.TaskID, &r.FindingsCount, &r.WarningsCount,
		&r.SeverityDistribution, &r.CategoryDistribution, &r.JSONReportPath,
		&r.MDReportPath, &r.Summary, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *sqliteStore) InsertMetric(ctx context.Context, m MetricRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO monitor_metric (id, task_id, total_duration_ms, diff_parse_ms, permission_filter_ms,
		 sandbox_total_ms, rule_engine_ms, llm_analyzer_ms, dedup_ms, report_gen_ms, storage_ms,
		 tool_calls_count, permission_blocks_count,
		 findings_critical, findings_high, findings_medium, findings_low, findings_warning,
		 llm_tokens_prompt, llm_tokens_completion, llm_tokens_total, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.TaskID, m.TotalDurationMs, m.DiffParseMs, m.PermissionFilterMs,
		m.SandboxTotalMs, m.RuleEngineMs, m.LLMAnalyzerMs, m.DedupMs, m.ReportGenMs, m.StorageMs,
		m.ToolCallsCount, m.PermissionBlocksCount,
		m.FindingsCritical, m.FindingsHigh, m.FindingsMedium, m.FindingsLow, m.FindingsWarning,
		m.LLMTokensPrompt, m.LLMTokensCompletion, m.LLMTokensTotal, m.CreatedAt,
	)
	return err
}

func (s *sqliteStore) InsertExceptions(ctx context.Context, es []ExceptionRow) error {
	if len(es) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO metrics_exception (id, task_id, error_type, error_count, error_detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range es {
		if _, err := stmt.ExecContext(ctx, e.ID, e.TaskID, e.ErrorType,
			e.ErrorCount, e.ErrorDetail, e.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// compileJSON validates JSON serialization for a value, used before writing to TEXT columns.
func compileJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// schema is the DDL embedded as a constant.
const schema = `
CREATE TABLE IF NOT EXISTS review_task (
    id              TEXT PRIMARY KEY,
    status          TEXT    NOT NULL DEFAULT 'pending',
    input_type      TEXT    NOT NULL,
    input_source    TEXT,
    input_diff_hash TEXT    NOT NULL,
    base_ref        TEXT    DEFAULT 'origin/main',
    total_files     INTEGER DEFAULT 0,
    total_hunks     INTEGER DEFAULT 0,
    model_mode      TEXT    NOT NULL DEFAULT 'live',
    error_message   TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    started_at      TEXT,
    completed_at    TEXT,
    total_duration_ms INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_task_status ON review_task(status);
CREATE INDEX IF NOT EXISTS idx_task_created ON review_task(created_at);

CREATE TABLE IF NOT EXISTS review_finding (
    id              TEXT PRIMARY KEY,
    task_id         TEXT    NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    severity        TEXT    NOT NULL,
    category        TEXT    NOT NULL,
    file            TEXT    NOT NULL,
    line            INTEGER NOT NULL DEFAULT 0,
    title           TEXT    NOT NULL,
    evidence        TEXT,
    recommendation  TEXT,
    confidence      REAL    NOT NULL DEFAULT 1.0,
    source          TEXT    NOT NULL,
    decision_kind   TEXT    NOT NULL DEFAULT 'heuristic',
    rule_id         TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_finding_task ON review_finding(task_id);
CREATE INDEX IF NOT EXISTS idx_finding_severity ON review_finding(severity);
CREATE INDEX IF NOT EXISTS idx_finding_category ON review_finding(category);
CREATE INDEX IF NOT EXISTS idx_finding_source ON review_finding(source);

CREATE TABLE IF NOT EXISTS sandbox_run (
    id              TEXT PRIMARY KEY,
    task_id         TEXT    NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    executor_type   TEXT    NOT NULL,
    command_name    TEXT    NOT NULL,
    command         TEXT    NOT NULL,
    exit_code       INTEGER NOT NULL DEFAULT -1,
    stdout          TEXT,
    stderr          TEXT,
    duration_ms     INTEGER DEFAULT 0,
    timed_out       INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    error_type      TEXT    DEFAULT '',
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_sandbox_task ON sandbox_run(task_id);

CREATE TABLE IF NOT EXISTS permission_decision (
    id              TEXT PRIMARY KEY,
    task_id         TEXT    NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    command         TEXT    NOT NULL,
    risk_level      TEXT    NOT NULL,
    decision        TEXT    NOT NULL,
    reason          TEXT,
    decided_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_perm_task ON permission_decision(task_id);

CREATE TABLE IF NOT EXISTS review_artifact (
    id              TEXT PRIMARY KEY,
    task_id         TEXT    NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    artifact_type   TEXT    NOT NULL,
    file_path       TEXT    NOT NULL,
    size_bytes      INTEGER DEFAULT 0,
    content_hash    TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_artifact_task ON review_artifact(task_id);

CREATE TABLE IF NOT EXISTS review_report (
    id                      TEXT PRIMARY KEY,
    task_id                 TEXT  NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    findings_count          INTEGER DEFAULT 0,
    warnings_count          INTEGER DEFAULT 0,
    severity_distribution   TEXT,
    category_distribution   TEXT,
    json_report_path        TEXT,
    md_report_path          TEXT,
    summary                 TEXT,
    created_at              TEXT  NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_report_task ON review_report(task_id);

CREATE TABLE IF NOT EXISTS monitor_metric (
    id                      TEXT PRIMARY KEY,
    task_id                 TEXT  NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    total_duration_ms       INTEGER DEFAULT 0,
    diff_parse_ms           INTEGER DEFAULT 0,
    permission_filter_ms    INTEGER DEFAULT 0,
    sandbox_total_ms        INTEGER DEFAULT 0,
    rule_engine_ms          INTEGER DEFAULT 0,
    llm_analyzer_ms         INTEGER DEFAULT 0,
    dedup_ms                INTEGER DEFAULT 0,
    report_gen_ms           INTEGER DEFAULT 0,
    storage_ms              INTEGER DEFAULT 0,
    tool_calls_count        INTEGER DEFAULT 0,
    permission_blocks_count INTEGER DEFAULT 0,
    findings_critical       INTEGER DEFAULT 0,
    findings_high           INTEGER DEFAULT 0,
    findings_medium         INTEGER DEFAULT 0,
    findings_low            INTEGER DEFAULT 0,
    findings_warning        INTEGER DEFAULT 0,
    llm_tokens_prompt       INTEGER DEFAULT 0,
    llm_tokens_completion   INTEGER DEFAULT 0,
    llm_tokens_total        INTEGER DEFAULT 0,
    created_at              TEXT  NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS metrics_exception (
    id              TEXT PRIMARY KEY,
    task_id         TEXT  NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    error_type      TEXT  NOT NULL,
    error_count     INTEGER NOT NULL DEFAULT 1,
    error_detail    TEXT,
    created_at      TEXT  NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_exception_task ON metrics_exception(task_id);
CREATE INDEX IF NOT EXISTS idx_exception_type ON metrics_exception(error_type);
`
