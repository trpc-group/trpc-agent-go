//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

// SQLiteStore implements the Store interface using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite store and initializes the schema.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	store := &SQLiteStore{db: db}
	if err := store.initSchema(); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return store, nil
}

// initSchema runs the DDL to create tables and indexes.
func (s *SQLiteStore) initSchema() error {
	_, err := s.db.Exec(schemaSQL)
	return err
}

// schemaSQL is embedded from schema.sql.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS cr_tasks (
    id TEXT PRIMARY KEY, diff_source TEXT NOT NULL, diff_summary TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending', changed_files TEXT NOT NULL DEFAULT '[]',
    finding_count INTEGER NOT NULL DEFAULT 0, high_risk_count INTEGER NOT NULL DEFAULT 0,
    medium_risk_count INTEGER NOT NULL DEFAULT 0, low_risk_count INTEGER NOT NULL DEFAULT 0,
    warning_count INTEGER NOT NULL DEFAULT 0, permission_denied INTEGER NOT NULL DEFAULT 0,
    permission_asked INTEGER NOT NULL DEFAULT 0, total_duration_ms INTEGER NOT NULL DEFAULT 0,
    sandbox_duration_ms INTEGER NOT NULL DEFAULT 0, tool_call_count INTEGER NOT NULL DEFAULT 0,
    dry_run INTEGER NOT NULL DEFAULT 0, report_json TEXT NOT NULL DEFAULT '',
    report_md TEXT NOT NULL DEFAULT '', error TEXT, created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now')));

CREATE TABLE IF NOT EXISTS cr_findings (
    id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES cr_tasks(id),
    sandbox_run_id TEXT, severity TEXT NOT NULL, category TEXT NOT NULL,
    file TEXT NOT NULL, line INTEGER NOT NULL DEFAULT 0, column INTEGER,
    title TEXT NOT NULL, evidence TEXT NOT NULL DEFAULT '', sanitized_evidence TEXT,
    recommendation TEXT NOT NULL DEFAULT '', confidence TEXT NOT NULL DEFAULT 'high',
    source TEXT NOT NULL DEFAULT 'custom_rule', rule_id TEXT NOT NULL DEFAULT '',
    hunk_id TEXT, is_duplicate INTEGER NOT NULL DEFAULT 0,
    is_warning INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT (datetime('now')));

CREATE TABLE IF NOT EXISTS cr_sandbox_runs (
    id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES cr_tasks(id),
    backend TEXT NOT NULL, command TEXT NOT NULL, sanitized_command TEXT NOT NULL DEFAULT '',
    exit_code INTEGER NOT NULL DEFAULT -1, stdout_summary TEXT NOT NULL DEFAULT '',
    stderr_summary TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
    timeout INTEGER NOT NULL DEFAULT 0, permission_action TEXT NOT NULL DEFAULT 'allow',
    error TEXT, created_at DATETIME NOT NULL DEFAULT (datetime('now')));

CREATE TABLE IF NOT EXISTS cr_permission_decisions (
    id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES cr_tasks(id),
    tool_name TEXT NOT NULL, command TEXT NOT NULL, sanitized_cmd TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')));

CREATE TABLE IF NOT EXISTS cr_reports (
    id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES cr_tasks(id),
    report_type TEXT NOT NULL, content TEXT NOT NULL, artifact_ref TEXT,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')));

CREATE INDEX IF NOT EXISTS idx_cr_findings_task ON cr_findings(task_id);
CREATE INDEX IF NOT EXISTS idx_cr_findings_dedup ON cr_findings(task_id, file, line, rule_id);
CREATE INDEX IF NOT EXISTS idx_cr_sandbox_runs_task ON cr_sandbox_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_cr_permission_decisions_task ON cr_permission_decisions(task_id);
`

// CreateTask inserts a new review task.
func (s *SQLiteStore) CreateTask(ctx context.Context, task *finding.ReviewTask) error {
	changedJSON, err := json.Marshal(task.ChangedFiles)
	if err != nil {
		return fmt.Errorf("marshal changed files: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO cr_tasks (id, diff_source, diff_summary, status, changed_files,
		 finding_count, high_risk_count, medium_risk_count, low_risk_count, warning_count,
		 permission_denied, permission_asked, total_duration_ms, sandbox_duration_ms,
		 tool_call_count, dry_run, report_json, report_md, error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.DiffSource, task.DiffSummary, task.Status, string(changedJSON),
		task.FindingCount, task.HighRiskCount, task.MediumRiskCount, task.LowRiskCount, task.WarningCount,
		task.PermissionDenied, task.PermissionAsked, task.TotalDurationMs, task.SandboxDurationMs,
		task.ToolCallCount, boolToInt(task.DryRun), task.ReportJSON, task.ReportMD, task.Error,
		time.Now(), time.Now())
	return err
}

// GetTask retrieves a review task by ID.
func (s *SQLiteStore) GetTask(ctx context.Context, taskID string) (*finding.ReviewTask, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, diff_source, diff_summary, status, changed_files,
		 finding_count, high_risk_count, medium_risk_count, low_risk_count, warning_count,
		 permission_denied, permission_asked, total_duration_ms, sandbox_duration_ms,
		 tool_call_count, dry_run, report_json, report_md, error, created_at, updated_at
		 FROM cr_tasks WHERE id = ?`, taskID)

	task := &finding.ReviewTask{}
	var changedJSON string
	var createdAt, updatedAt time.Time

	err := row.Scan(&task.ID, &task.DiffSource, &task.DiffSummary, &task.Status, &changedJSON,
		&task.FindingCount, &task.HighRiskCount, &task.MediumRiskCount, &task.LowRiskCount, &task.WarningCount,
		&task.PermissionDenied, &task.PermissionAsked, &task.TotalDurationMs, &task.SandboxDurationMs,
		&task.ToolCallCount, &task.DryRun, &task.ReportJSON, &task.ReportMD, &task.Error,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	task.CreatedAt = createdAt
	task.UpdatedAt = updatedAt
	if changedJSON != "" {
		_ = json.Unmarshal([]byte(changedJSON), &task.ChangedFiles)
	}
	return task, nil
}

// UpdateTaskStatus updates the task status and optional error message.
func (s *SQLiteStore) UpdateTaskStatus(ctx context.Context, taskID string, status string, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cr_tasks SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
		status, errMsg, time.Now(), taskID)
	return err
}

// UpdateTaskStats updates the task statistics.
func (s *SQLiteStore) UpdateTaskStats(ctx context.Context, taskID string, stats TaskStats) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cr_tasks SET finding_count=?, high_risk_count=?, medium_risk_count=?,
		 low_risk_count=?, warning_count=?, permission_denied=?, permission_asked=?,
		 total_duration_ms=?, sandbox_duration_ms=?, tool_call_count=?, updated_at=?
		 WHERE id=?`,
		stats.FindingCount, stats.HighRiskCount, stats.MediumRiskCount,
		stats.LowRiskCount, stats.WarningCount, stats.PermissionDenied, stats.PermissionAsked,
		stats.TotalDurationMs, stats.SandboxDurationMs, stats.ToolCallCount,
		time.Now(), taskID)
	return err
}

// ListTasks returns a page of tasks ordered by creation time.
func (s *SQLiteStore) ListTasks(ctx context.Context, limit, offset int) ([]*finding.ReviewTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, diff_source, diff_summary, status, finding_count, high_risk_count,
		 medium_risk_count, low_risk_count, warning_count, created_at
		 FROM cr_tasks ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*finding.ReviewTask
	for rows.Next() {
		t := &finding.ReviewTask{}
		var createdAt time.Time
		if err := rows.Scan(&t.ID, &t.DiffSource, &t.DiffSummary, &t.Status,
			&t.FindingCount, &t.HighRiskCount, &t.MediumRiskCount,
			&t.LowRiskCount, &t.WarningCount, &createdAt); err != nil {
			return nil, err
		}
		t.CreatedAt = createdAt
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// CreateFindings inserts findings in batch.
func (s *SQLiteStore) CreateFindings(ctx context.Context, findings []*finding.Finding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO cr_findings (id, task_id, sandbox_run_id, severity, category,
		 file, line, column, title, evidence, sanitized_evidence, recommendation,
		 confidence, source, rule_id, hunk_id, is_duplicate, is_warning, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now()
	for _, f := range findings {
		if _, err := stmt.ExecContext(ctx,
			f.ID, "", "", string(f.Severity), string(f.Category),
			f.File, f.Line, nullableInt(f.Column), f.Title, f.Evidence, "",
			f.Recommendation, string(f.Confidence), string(f.Source), f.RuleID,
			f.HunkID, boolToInt(f.IsDuplicate), boolToInt(f.Confidence == finding.ConfidenceLow), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetFindings retrieves findings for a task, optionally filtered by severity.
func (s *SQLiteStore) GetFindings(ctx context.Context, taskID string, severities ...finding.Severity) ([]*finding.Finding, error) {
	query := `SELECT id, severity, category, file, line, column, title, evidence,
		 sanitized_evidence, recommendation, confidence, source, rule_id, hunk_id,
		 is_duplicate, is_warning FROM cr_findings WHERE task_id = ?`
	args := []any{taskID}

	if len(severities) > 0 {
		placeholders := ""
		for i, sev := range severities {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
			args = append(args, string(sev))
		}
		query += " AND severity IN (" + placeholders + ")"
	}
	query += " ORDER BY line ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*finding.Finding
	for rows.Next() {
		f := &finding.Finding{}
		var nullCol sql.NullInt64
		var sanitizedEvidence string
		if err := rows.Scan(&f.ID, &f.Severity, &f.Category, &f.File, &f.Line, &nullCol,
			&f.Title, &f.Evidence, &sanitizedEvidence, &f.Recommendation, &f.Confidence,
			&f.Source, &f.RuleID, &f.HunkID, &f.IsDuplicate, &f.IsWarning); err != nil {
			return nil, err
		}
		if nullCol.Valid {
			f.Column = int(nullCol.Int64)
		}
		f.Sanitized = sanitizedEvidence != ""
		result = append(result, f)
	}
	return result, nil
}

// CountFindings counts total findings for a task.
func (s *SQLiteStore) CountFindings(ctx context.Context, taskID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cr_findings WHERE task_id = ?`, taskID).Scan(&count)
	return count, err
}

// CheckDuplicate checks if a finding already exists for the same file/line/rule.
func (s *SQLiteStore) CheckDuplicate(ctx context.Context, taskID, file string, line int, ruleID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cr_findings WHERE task_id = ? AND file = ? AND line = ? AND rule_id = ?`,
		taskID, file, line, ruleID).Scan(&count)
	return count > 0, err
}

// CreateSandboxRun inserts a sandbox run record.
func (s *SQLiteStore) CreateSandboxRun(ctx context.Context, run *finding.SandboxRun) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cr_sandbox_runs (id, task_id, backend, command, sanitized_command,
		 exit_code, stdout_summary, stderr_summary, duration_ms, timeout,
		 permission_action, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, run.Backend, run.Command, "",
		run.ExitCode, run.StdoutSummary, run.StderrSummary, run.DurationMs,
		boolToInt(run.Timeout), run.PermissionAction, run.Error, time.Now())
	return err
}

// GetSandboxRuns retrieves all sandbox runs for a task.
func (s *SQLiteStore) GetSandboxRuns(ctx context.Context, taskID string) ([]*finding.SandboxRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, backend, command, exit_code, stdout_summary,
		 stderr_summary, duration_ms, timeout, permission_action, error, created_at
		 FROM cr_sandbox_runs WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*finding.SandboxRun
	for rows.Next() {
		r := &finding.SandboxRun{}
		var createdAt time.Time
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Backend, &r.Command, &r.ExitCode,
			&r.StdoutSummary, &r.StderrSummary, &r.DurationMs, &r.Timeout,
			&r.PermissionAction, &r.Error, &createdAt); err != nil {
			return nil, err
		}
		r.CreatedAt = createdAt
		runs = append(runs, r)
	}
	return runs, nil
}

// CreatePermissionDecision inserts a permission decision record.
func (s *SQLiteStore) CreatePermissionDecision(ctx context.Context, pd *finding.PermissionDecision) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cr_permission_decisions (id, task_id, tool_name, command,
		 sanitized_cmd, decision, reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		pd.ID, pd.TaskID, pd.ToolName, pd.Command, pd.SanitizedCmd,
		pd.Decision, pd.Reason, time.Now())
	return err
}

// GetPermissionDecisions retrieves permission decisions for a task.
func (s *SQLiteStore) GetPermissionDecisions(ctx context.Context, taskID string) ([]*finding.PermissionDecision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, tool_name, command, sanitized_cmd, decision, reason, created_at
		 FROM cr_permission_decisions WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var decisions []*finding.PermissionDecision
	for rows.Next() {
		d := &finding.PermissionDecision{}
		var createdAt time.Time
		if err := rows.Scan(&d.ID, &d.TaskID, &d.ToolName, &d.Command, &d.SanitizedCmd,
			&d.Decision, &d.Reason, &createdAt); err != nil {
			return nil, err
		}
		d.CreatedAt = createdAt
		decisions = append(decisions, d)
	}
	return decisions, nil
}

// SaveReport saves a report for a task.
func (s *SQLiteStore) SaveReport(ctx context.Context, taskID, reportType, content string) error {
	id := taskID + "-" + reportType
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO cr_reports (id, task_id, report_type, content, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, taskID, reportType, content, time.Now())
	return err
}

// GetReport retrieves a report by task ID and report type.
func (s *SQLiteStore) GetReport(ctx context.Context, taskID, reportType string) (string, error) {
	var content string
	err := s.db.QueryRowContext(ctx,
		`SELECT content FROM cr_reports WHERE task_id = ? AND report_type = ?`,
		taskID, reportType).Scan(&content)
	return content, err
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// -- helpers --

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(i int) bool {
	return i != 0
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
