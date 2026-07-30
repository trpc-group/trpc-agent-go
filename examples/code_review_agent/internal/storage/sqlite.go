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
	ID              string // Unique identifier for the review task.
	Status          string // Current status: pending, running, completed, failed.
	InputType       string // Type of input: diff, pr, commit, etc.
	InputSource     string // Source of the input: file path, branch name, etc.
	InputDiffHash   string // SHA256 hash of the diff content for deduplication.
	BaseRef         string // Git base ref the diff is compared against.
	TotalFiles      int    // Number of files in the diff.
	TotalHunks      int    // Number of hunks (diff chunks) in the diff.
	ModelMode       string // LLM mode: live, mock, etc.
	ErrorMessage    string // Error message if the task failed.
	CreatedAt       string // ISO 8601 timestamp of creation.
	StartedAt       string // ISO 8601 timestamp when processing started.
	CompletedAt     string // ISO 8601 timestamp when processing completed.
	TotalDurationMs int64  // Total duration of the review in milliseconds.
}

// FindingRow maps to review_finding table.
type FindingRow struct {
	ID             string  // Unique identifier for the finding.
	TaskID         string  // Foreign key referencing review_task.id.
	Severity       string  // Severity level: critical, high, medium, low, warning.
	Category       string  // Category of the finding: security, performance, style, etc.
	File           string  // Source file path where the issue was found.
	Line           int     // Line number in the source file.
	Title          string  // Short title describing the issue.
	Evidence       string  // Evidence snippet from the code supporting the finding.
	Recommendation string  // Suggested fix or improvement.
	Confidence     float64 // Confidence score between 0.0 and 1.0.
	Source         string  // Origin of the finding: llm, rule, sandbox.
	DecisionKind   string  // Decision kind: heuristic, approved, rejected, etc.
	RuleID         string  // Rule identifier if the finding came from a static rule.
	CreatedAt      string  // ISO 8601 timestamp of creation.
}

// SandboxRunRow maps to sandbox_run table.
type SandboxRunRow struct {
	ID              string // Unique identifier for the sandbox run.
	TaskID          string // Foreign key referencing review_task.id.
	ExecutorType    string // Sandbox executor type: docker, e2b, etc.
	CommandName     string // Human-readable name for the command.
	Command         string // Full command string executed in the sandbox.
	ExitCode        int    // Exit code from the command execution.
	Stdout          string // Standard output captured from the command.
	Stderr          string // Standard error captured from the command.
	DurationMs      int64  // Execution duration in milliseconds.
	TimedOut        bool   // Whether the command exceeded the timeout.
	OutputTruncated bool   // Whether the output was truncated due to size limits.
	ErrorType       string // Classification of any execution error.
	CreatedAt       string // ISO 8601 timestamp of creation.
}

// PermissionDecisionRow maps to permission_decision table.
type PermissionDecisionRow struct {
	ID        string // Unique identifier for the decision.
	TaskID    string // Foreign key referencing review_task.id.
	Command   string // Command that was evaluated for permission.
	RiskLevel string // Assessed risk level: low, medium, high, critical.
	Decision  string // Permission decision: allow, deny, review.
	Reason    string // Rationale behind the permission decision.
	DecidedAt string // ISO 8601 timestamp of the decision.
}

// ArtifactRow maps to review_artifact table.
type ArtifactRow struct {
	ID           string // Unique identifier for the artifact.
	TaskID       string // Foreign key referencing review_task.id.
	ArtifactType string // Type of artifact: patch, log, report, etc.
	FilePath     string // File path where the artifact is stored.
	SizeBytes    int64  // Size of the artifact in bytes.
	ContentHash  string // SHA256 hash of the artifact content.
	CreatedAt    string // ISO 8601 timestamp of creation.
}

// ReportRow maps to review_report table.
type ReportRow struct {
	ID                   string // Unique identifier for the report.
	TaskID               string // Foreign key referencing review_task.id.
	FindingsCount        int    // Total number of findings in the report.
	WarningsCount        int    // Total number of warnings in the report.
	SeverityDistribution string // JSON-encoded map of severity to count.
	CategoryDistribution string // JSON-encoded map of category to count.
	JSONReportPath       string // File path to the JSON-formatted report.
	MDReportPath         string // File path to the Markdown-formatted report.
	Summary              string // Natural language summary of the review.
	CreatedAt            string // ISO 8601 timestamp of creation.
}

// MetricRow maps to monitor_metric table.
type MetricRow struct {
	ID                    string // Unique identifier for the metric record.
	TaskID                string // Foreign key referencing review_task.id.
	TotalDurationMs       int64  // Total review duration in milliseconds.
	DiffParseMs           int64  // Time spent parsing the diff in milliseconds.
	PermissionFilterMs    int64  // Time spent filtering permissions in milliseconds.
	SandboxTotalMs        int64  // Total time spent in sandbox execution in milliseconds.
	RuleEngineMs          int64  // Time spent in rule engine processing in milliseconds.
	LLMAnalyzerMs         int64  // Time spent in LLM analysis in milliseconds.
	DedupMs               int64  // Time spent deduplicating findings in milliseconds.
	ReportGenMs           int64  // Time spent generating reports in milliseconds.
	StorageMs             int64  // Time spent in storage operations in milliseconds.
	ToolCallsCount        int    // Number of tool calls made during the review.
	PermissionBlocksCount int    // Number of times permission was blocked.
	FindingsCritical      int    // Count of critical-severity findings.
	FindingsHigh          int    // Count of high-severity findings.
	FindingsMedium        int    // Count of medium-severity findings.
	FindingsLow           int    // Count of low-severity findings.
	FindingsWarning       int    // Count of warning-level findings.
	LLMTokensPrompt       int    // Number of prompt tokens consumed by the LLM.
	LLMTokensCompletion   int    // Number of completion tokens generated by the LLM.
	LLMTokensTotal        int    // Total number of LLM tokens consumed.
	CreatedAt             string // ISO 8601 timestamp of creation.
}

// ExceptionRow maps to metrics_exception table.
type ExceptionRow struct {
	ID          string // Unique identifier for the exception record.
	TaskID      string // Foreign key referencing review_task.id.
	ErrorType   string // Classification of the exception type.
	ErrorCount  int    // Number of times this exception occurred.
	ErrorDetail string // Detailed error message or stack trace.
	CreatedAt   string // ISO 8601 timestamp of creation.
}

// ── Storage interface ──

// Storage is the database abstraction for review persistence.
type Storage interface {
	// CreateTask inserts a new review task record.
	CreateTask(ctx context.Context, t TaskRow) error
	// UpdateTask updates specific columns of a task record identified by id.
	UpdateTask(ctx context.Context, id string, updates map[string]any) error
	// GetTask retrieves a single task record by its unique id.
	GetTask(ctx context.Context, id string) (*TaskRow, error)

	// InsertFindings batch-inserts finding records, ignoring duplicates on conflict.
	InsertFindings(ctx context.Context, findings []FindingRow) error
	// GetFindingsByTask retrieves all findings for a given task, ordered by severity, file, and line.
	GetFindingsByTask(ctx context.Context, taskID string) ([]FindingRow, error)

	// InsertSandboxRun records a single sandbox execution.
	InsertSandboxRun(ctx context.Context, r SandboxRunRow) error
	// GetSandboxRunsByTask retrieves all sandbox runs for a given task.
	GetSandboxRunsByTask(ctx context.Context, taskID string) ([]SandboxRunRow, error)

	// InsertPermissionDecisions batch-inserts permission decision records.
	InsertPermissionDecisions(ctx context.Context, ds []PermissionDecisionRow) error

	// InsertArtifact records a single review artifact.
	InsertArtifact(ctx context.Context, a ArtifactRow) error

	// InsertReport records a review report for a task.
	InsertReport(ctx context.Context, r ReportRow) error
	// GetReport retrieves the report associated with a task.
	GetReport(ctx context.Context, taskID string) (*ReportRow, error)

	// InsertMetric records performance metrics for a task.
	InsertMetric(ctx context.Context, m MetricRow) error
	// InsertExceptions batch-inserts exception records for a task.
	InsertExceptions(ctx context.Context, es []ExceptionRow) error

	// Close closes the database connection and releases resources.
	Close() error
	// Ping verifies the database connection is still alive.
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
