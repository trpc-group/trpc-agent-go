//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SandboxRun 表示一次沙箱执行记录。
type SandboxRun struct {
	ID         int64  `json:"id"`
	TaskID     string `json:"task_id"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
	CreatedAt  int64  `json:"created_at"`
}

// PermissionDecision 表示一次安全决策记录。
type PermissionDecision struct {
	ID          int64  `json:"id"`
	TaskID      string `json:"task_id"`
	Command     string `json:"command"`
	Decision    string `json:"decision"`
	RuleID      string `json:"rule_id"`
	RiskLevel   string `json:"risk_level"`
	Reason      string `json:"reason"`
	Intercepted bool   `json:"intercepted"`
	CreatedAt   int64  `json:"created_at"`
}

// MonitoringSummary 表示一次审查的监控摘要。
type MonitoringSummary struct {
	TaskID               string `json:"task_id"`
	TotalDurationMs      int64  `json:"total_duration_ms"`
	SandboxDurationMs    int64  `json:"sandbox_duration_ms"`
	ToolCallsCount       int    `json:"tool_calls_count"`
	PermissionIntercepts int    `json:"permission_intercepts"`
	FindingCount         int    `json:"finding_count"`
}

// Store SQLite 持久化存储。
type Store struct {
	db *sql.DB
}

// NewStore 创建或打开 SQLite 数据库。
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(time.Hour)

	s := &Store{db: db}
	if err := s.initDB(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库失败: %w", err)
	}

	return s, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	return s.db.Close()
}

// initDB 创建所有表。
func (s *Store) initDB(ctx context.Context) error {
	schemas := []string{
		`CREATE TABLE IF NOT EXISTS review_tasks (
			id TEXT PRIMARY KEY,
			input_type TEXT NOT NULL DEFAULT '',
			input_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			created_at INTEGER NOT NULL,
			completed_at INTEGER DEFAULT 0,
			total_files INTEGER DEFAULT 0,
			total_findings INTEGER DEFAULT 0,
			critical_count INTEGER DEFAULT 0,
			high_count INTEGER DEFAULT 0,
			medium_count INTEGER DEFAULT 0,
			low_count INTEGER DEFAULT 0,
			warning_count INTEGER DEFAULT 0,
			duration_ms INTEGER DEFAULT 0,
			error_message TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			severity TEXT NOT NULL,
			category TEXT NOT NULL,
			file TEXT NOT NULL DEFAULT '',
			line INTEGER DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',
			evidence TEXT DEFAULT '',
			recommendation TEXT DEFAULT '',
			confidence REAL DEFAULT 1.0,
			source TEXT DEFAULT 'rule',
			rule_id TEXT DEFAULT '',
			dedup_key TEXT NOT NULL DEFAULT '',
			is_duplicate INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sandbox_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			command TEXT NOT NULL DEFAULT '',
			exit_code INTEGER DEFAULT 0,
			stdout TEXT DEFAULT '',
			stderr TEXT DEFAULT '',
			duration_ms INTEGER DEFAULT 0,
			timed_out INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS permission_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			command TEXT NOT NULL DEFAULT '',
			decision TEXT NOT NULL DEFAULT '',
			rule_id TEXT DEFAULT '',
			risk_level TEXT DEFAULT '',
			reason TEXT DEFAULT '',
			intercepted INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS monitoring_summary (
			task_id TEXT PRIMARY KEY,
			total_duration_ms INTEGER DEFAULT 0,
			sandbox_duration_ms INTEGER DEFAULT 0,
			tool_calls_count INTEGER DEFAULT 0,
			permission_intercepts INTEGER DEFAULT 0,
			finding_count INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_findings_task ON findings(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_findings_dedup ON findings(task_id, dedup_key)`,
		`CREATE INDEX IF NOT EXISTS idx_sandbox_task ON sandbox_runs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_permissions_task ON permission_decisions(task_id)`,
	}

	for _, schema := range schemas {
		if _, err := s.db.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("执行 schema 失败: %w\nSQL: %s", err, schema)
		}
	}

	return nil
}

// SaveTask 保存或更新审查任务。
func (s *Store) SaveTask(ctx context.Context, task *ReviewTask) error {
	query := `INSERT OR REPLACE INTO review_tasks
		(id, input_type, input_hash, status, created_at, completed_at,
		 total_files, total_findings, critical_count, high_count,
		 medium_count, low_count, warning_count, duration_ms, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query,
		task.ID, task.InputType, task.InputHash, task.Status,
		task.CreatedAt, task.CompletedAt,
		task.TotalFiles, task.TotalFindings,
		task.Summary.Critical, task.Summary.High,
		task.Summary.Medium, task.Summary.Low, task.Summary.Warning,
		task.DurationMs, task.ErrorMessage,
	)
	return err
}

// InsertFinding 插入一条 finding。
func (s *Store) InsertFinding(ctx context.Context, taskID string, f Finding) error {
	query := `INSERT INTO findings
		(task_id, severity, category, file, line, title, evidence,
		 recommendation, confidence, source, rule_id, dedup_key,
		 is_duplicate, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	dup := 0
	if f.IsDuplicate {
		dup = 1
	}

	_, err := s.db.ExecContext(ctx, query,
		taskID, f.Severity, f.Category, f.File, f.Line, f.Title,
		f.Evidence, f.Recommendation, f.Confidence, f.Source,
		f.RuleID, f.DedupKey, dup, f.Timestamp,
	)
	return err
}

// InsertFindingsBatch 批量插入 findings（使用事务）。
func (s *Store) InsertFindingsBatch(ctx context.Context, taskID string, findings []Finding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer tx.Rollback()

	for _, f := range findings {
		if f.IsDuplicate {
			continue
		}
		if err := s.insertFindingTx(ctx, tx, taskID, f); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) insertFindingTx(ctx context.Context, tx *sql.Tx, taskID string, f Finding) error {
	query := `INSERT INTO findings
		(task_id, severity, category, file, line, title, evidence,
		 recommendation, confidence, source, rule_id, dedup_key,
		 is_duplicate, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	dup := 0
	if f.IsDuplicate {
		dup = 1
	}

	_, err := tx.ExecContext(ctx, query,
		taskID, f.Severity, f.Category, f.File, f.Line, f.Title,
		f.Evidence, f.Recommendation, f.Confidence, f.Source,
		f.RuleID, f.DedupKey, dup, f.Timestamp,
	)
	return err
}

// InsertSandboxRun 插入沙箱执行记录。
func (s *Store) InsertSandboxRun(ctx context.Context, run SandboxRun) error {
	query := `INSERT INTO sandbox_runs
		(task_id, command, exit_code, stdout, stderr, duration_ms, timed_out, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	timedOut := 0
	if run.TimedOut {
		timedOut = 1
	}

	_, err := s.db.ExecContext(ctx, query,
		run.TaskID, run.Command, run.ExitCode, run.Stdout, run.Stderr,
		run.DurationMs, timedOut, run.CreatedAt,
	)
	return err
}

// InsertPermissionDecision 插入安全决策记录。
func (s *Store) InsertPermissionDecision(ctx context.Context, d PermissionDecision) error {
	query := `INSERT INTO permission_decisions
		(task_id, command, decision, rule_id, risk_level, reason, intercepted, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	intercepted := 0
	if d.Intercepted {
		intercepted = 1
	}

	_, err := s.db.ExecContext(ctx, query,
		d.TaskID, d.Command, d.Decision, d.RuleID, d.RiskLevel,
		d.Reason, intercepted, d.CreatedAt,
	)
	return err
}

// SaveMonitoringSummary 保存监控摘要。
func (s *Store) SaveMonitoringSummary(ctx context.Context, m MonitoringSummary) error {
	query := `INSERT OR REPLACE INTO monitoring_summary
		(task_id, total_duration_ms, sandbox_duration_ms, tool_calls_count,
		 permission_intercepts, finding_count)
		VALUES (?, ?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query,
		m.TaskID, m.TotalDurationMs, m.SandboxDurationMs,
		m.ToolCallsCount, m.PermissionIntercepts, m.FindingCount,
	)
	return err
}

// GetTask 根据 ID 查询审查任务。
func (s *Store) GetTask(ctx context.Context, taskID string) (*ReviewTask, error) {
	query := `SELECT id, input_type, input_hash, status, created_at, completed_at,
		total_files, total_findings, critical_count, high_count,
		medium_count, low_count, warning_count, duration_ms, error_message
		FROM review_tasks WHERE id = ?`

	var t ReviewTask
	err := s.db.QueryRowContext(ctx, query, taskID).Scan(
		&t.ID, &t.InputType, &t.InputHash, &t.Status,
		&t.CreatedAt, &t.CompletedAt,
		&t.TotalFiles, &t.TotalFindings,
		&t.Summary.Critical, &t.Summary.High,
		&t.Summary.Medium, &t.Summary.Low, &t.Summary.Warning,
		&t.DurationMs, &t.ErrorMessage,
	)
	if err != nil {
		return nil, err
	}
	t.Summary.Total = t.TotalFindings
	return &t, nil
}

// GetFindingsByTask 查询某个任务的所有 findings。
func (s *Store) GetFindingsByTask(ctx context.Context, taskID string) ([]Finding, error) {
	query := `SELECT severity, category, file, line, title, evidence,
		recommendation, confidence, source, rule_id, dedup_key, is_duplicate, created_at
		FROM findings WHERE task_id = ? ORDER BY
		CASE severity
			WHEN 'critical' THEN 0
			WHEN 'high' THEN 1
			WHEN 'medium' THEN 2
			WHEN 'low' THEN 3
			WHEN 'warning' THEN 4
			ELSE 5 END, file, line`

	rows, err := s.db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var f Finding
		var dup int
		if err := rows.Scan(&f.Severity, &f.Category, &f.File, &f.Line,
			&f.Title, &f.Evidence, &f.Recommendation, &f.Confidence,
			&f.Source, &f.RuleID, &f.DedupKey, &dup, &f.Timestamp); err != nil {
			return nil, err
		}
		f.IsDuplicate = dup == 1
		findings = append(findings, f)
	}

	return findings, rows.Err()
}
