//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlite 提供 SQLite 存储实现。
package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
)

// Store 持有 SQLite 连接。
type Store struct {
	db *sql.DB
}

// Compatibility aliases keep existing SQLite callers source-compatible while
// record ownership remains in the storage domain package.
type Task = storage.Task
type Report = storage.ReportRecord
type DecisionRecord = storage.DecisionRecord
type FilterDecisionRecord = storage.FilterDecisionRecord
type SandboxRunRecord = storage.SandboxRunRecord
type ArtifactRecord = storage.ArtifactRecord
type MetricsRecord = storage.MetricsRecord
type MetricsSummary = storage.MetricsRecord

// Open 打开 SQLite 数据库。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite PRAGMA settings are connection-local. A single connection keeps
	// foreign-key enforcement consistent for all Store operations.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.Init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Init 创建表结构。
func (s *Store) Init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS review_tasks (
  task_id TEXT PRIMARY KEY,
  input_type TEXT NOT NULL,
  input_ref TEXT NOT NULL,
  input_digest TEXT NOT NULL,
  repo_path TEXT NOT NULL,
  status TEXT NOT NULL,
  mode TEXT NOT NULL,
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT
);
CREATE TABLE IF NOT EXISTS findings (
  finding_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  severity TEXT NOT NULL,
  category TEXT NOT NULL,
  file TEXT NOT NULL,
  line INTEGER NOT NULL,
  title TEXT NOT NULL,
  evidence TEXT,
  recommendation TEXT,
  confidence TEXT,
  source TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  dedupe_key TEXT NOT NULL,
  status TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(task_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS reports (
  task_id TEXT PRIMARY KEY,
  json_report BLOB NOT NULL,
  markdown_report BLOB NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(task_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS permission_decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  command TEXT NOT NULL,
  action TEXT NOT NULL,
  reason TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(task_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS filter_decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  target TEXT NOT NULL,
  action TEXT NOT NULL,
  reason TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(task_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS sandbox_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  command TEXT NOT NULL,
  runtime TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  timeout_ms INTEGER NOT NULL DEFAULT 0,
  output_limit_bytes INTEGER NOT NULL DEFAULT 0,
  env_whitelist TEXT NOT NULL DEFAULT '',
  exit_code INTEGER NOT NULL DEFAULT 0,
  stdout_digest TEXT NOT NULL DEFAULT '',
  stderr_digest TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  output TEXT,
  created_at TEXT NOT NULL,
  finished_at TEXT,
  artifact_count INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY(task_id) REFERENCES review_tasks(task_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS artifacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  path TEXT,
  digest TEXT,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(task_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS metrics (
  task_id TEXT PRIMARY KEY,
	mode TEXT,
	sandbox_requested INTEGER,
	sandbox_executed INTEGER,
	model_requested INTEGER,
	model_executed INTEGER,
  total_duration_ms INTEGER NOT NULL,
  sandbox_duration_ms INTEGER NOT NULL,
  model_duration_ms INTEGER NOT NULL DEFAULT 0,
  tool_call_count INTEGER NOT NULL,
  model_call_count INTEGER NOT NULL DEFAULT 0,
  model_provider TEXT NOT NULL DEFAULT '',
  model_name TEXT NOT NULL DEFAULT '',
  model_backend TEXT NOT NULL DEFAULT '',
  permission_block_count INTEGER NOT NULL,
  finding_count INTEGER NOT NULL,
  model_finding_count INTEGER NOT NULL DEFAULT 0,
  model_exception_count INTEGER NOT NULL DEFAULT 0,
  severity_counts_json TEXT NOT NULL,
  exception_counts_json TEXT NOT NULL,
  redaction_count INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(task_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_findings_task_file_line ON findings(task_id, file, line);
CREATE INDEX IF NOT EXISTS idx_permission_decisions_task ON permission_decisions(task_id);
CREATE INDEX IF NOT EXISTS idx_filter_decisions_task ON filter_decisions(task_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task ON sandbox_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_task ON artifacts(task_id);
`)
	if err != nil {
		return err
	}
	return s.migrate(ctx)
}

// migrate 补齐旧库缺失列。
func (s *Store) migrate(ctx context.Context) error {
	for _, stmt := range []string{
		`ALTER TABLE sandbox_runs ADD COLUMN finished_at TEXT`,
		`ALTER TABLE sandbox_runs ADD COLUMN artifact_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE artifacts ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE metrics ADD COLUMN model_duration_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE metrics ADD COLUMN model_call_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE metrics ADD COLUMN model_provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE metrics ADD COLUMN model_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE metrics ADD COLUMN model_backend TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE metrics ADD COLUMN model_finding_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE metrics ADD COLUMN model_exception_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE metrics ADD COLUMN mode TEXT`,
		`ALTER TABLE metrics ADD COLUMN sandbox_requested INTEGER`,
		`ALTER TABLE metrics ADD COLUMN sandbox_executed INTEGER`,
		`ALTER TABLE metrics ADD COLUMN model_requested INTEGER`,
		`ALTER TABLE metrics ADD COLUMN model_executed INTEGER`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}
	return nil
}

func isDuplicateColumnError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "duplicate column name") || strings.Contains(err.Error(), "duplicate column"))
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SaveTask 插入或更新任务。
func (s *Store) SaveTask(ctx context.Context, task Task) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO review_tasks(task_id, input_type, input_ref, input_digest, repo_path, status, mode, created_at, started_at, finished_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
input_type=excluded.input_type,
input_ref=excluded.input_ref,
input_digest=excluded.input_digest,
repo_path=excluded.repo_path,
status=excluded.status,
mode=excluded.mode,
created_at=excluded.created_at,
started_at=excluded.started_at,
finished_at=excluded.finished_at
`,
		task.ID, task.InputType, task.InputRef, task.InputDigest, task.RepoPath, task.Status, task.Mode,
		task.CreatedAt.UTC().Format(time.RFC3339Nano), nullableTime(task.StartedAt), nullableTime(task.FinishedAt))
	return err
}

// SaveFinding 写入 finding。
func (s *Store) SaveFinding(ctx context.Context, taskID string, finding review.Finding) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO findings(finding_id, task_id, severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id, dedupe_key, status)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		taskID+":"+finding.DedupeKey(), taskID, finding.Severity, finding.Category, finding.File, finding.Line, finding.Title,
		finding.Evidence, finding.Recommendation, finding.Confidence, finding.Source, finding.RuleID, finding.DedupeKey(), finding.Status)
	return err
}

// SaveReview 在一个事务中保存完整审查。任何子记录失败都会回滚任务及已写记录。
func (s *Store) SaveReview(ctx context.Context, rec storage.ReviewRecord) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `
INSERT INTO review_tasks(task_id, input_type, input_ref, input_digest, repo_path, status, mode, created_at, started_at, finished_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET input_type=excluded.input_type, input_ref=excluded.input_ref,
input_digest=excluded.input_digest, repo_path=excluded.repo_path, status=excluded.status, mode=excluded.mode,
created_at=excluded.created_at, started_at=excluded.started_at, finished_at=excluded.finished_at
`, rec.Task.ID, rec.Task.InputType, rec.Task.InputRef, rec.Task.InputDigest, rec.Task.RepoPath, rec.Task.Status, rec.Task.Mode,
		rec.Task.CreatedAt.UTC().Format(time.RFC3339Nano), nullableTime(rec.Task.StartedAt), nullableTime(rec.Task.FinishedAt)); err != nil {
		return err
	}
	for _, item := range rec.Decisions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO permission_decisions(task_id, command, action, reason, created_at) VALUES(?, ?, ?, ?, ?)`,
			item.TaskID, item.Command, item.Action, item.Reason, item.At.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	for _, item := range rec.FilterDecisions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO filter_decisions(task_id, target, action, reason, created_at) VALUES(?, ?, ?, ?, ?)`,
			item.TaskID, item.Target, item.Action, item.Reason, item.At.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	for _, item := range rec.SandboxRuns {
		if _, err = tx.ExecContext(ctx, `
INSERT INTO sandbox_runs(task_id, command, runtime, status, timeout_ms, output_limit_bytes, env_whitelist, exit_code, stdout_digest, stderr_digest, duration_ms, output, created_at, finished_at, artifact_count)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.TaskID, item.Command, item.Runtime, item.Status,
			item.TimeoutMS, item.OutputLimitBytes, item.EnvWhitelist, item.ExitCode, item.StdoutDigest, item.StderrDigest,
			item.DurationMS, item.Output, item.At.UTC().Format(time.RFC3339Nano), nullableTime(item.FinishedAt), item.ArtifactCount); err != nil {
			return err
		}
	}
	for _, item := range rec.Findings {
		key := item.DedupeKey()
		if _, err = tx.ExecContext(ctx, `
INSERT INTO findings(finding_id, task_id, severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id, dedupe_key, status)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, rec.Task.ID+":"+key, rec.Task.ID, item.Severity, item.Category,
			item.File, item.Line, item.Title, item.Evidence, item.Recommendation, item.Confidence, item.Source, item.RuleID, key, item.Status); err != nil {
			return err
		}
	}
	m := rec.Metrics
	if _, err = tx.ExecContext(ctx, `
INSERT INTO metrics(task_id, mode, sandbox_requested, sandbox_executed, model_requested, model_executed, total_duration_ms, sandbox_duration_ms, model_duration_ms, tool_call_count, model_call_count, model_provider, model_name, model_backend, permission_block_count, finding_count, model_finding_count, model_exception_count, severity_counts_json, exception_counts_json, redaction_count, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET total_duration_ms=excluded.total_duration_ms, sandbox_duration_ms=excluded.sandbox_duration_ms,
mode=excluded.mode, sandbox_requested=excluded.sandbox_requested, sandbox_executed=excluded.sandbox_executed,
model_requested=excluded.model_requested, model_executed=excluded.model_executed,
model_duration_ms=excluded.model_duration_ms, tool_call_count=excluded.tool_call_count, model_call_count=excluded.model_call_count,
model_provider=excluded.model_provider, model_name=excluded.model_name, model_backend=excluded.model_backend,
permission_block_count=excluded.permission_block_count, finding_count=excluded.finding_count,
model_finding_count=excluded.model_finding_count, model_exception_count=excluded.model_exception_count,
severity_counts_json=excluded.severity_counts_json, exception_counts_json=excluded.exception_counts_json,
redaction_count=excluded.redaction_count, created_at=excluded.created_at`, m.TaskID, m.Mode, m.SandboxRequested, m.SandboxExecuted, m.ModelRequested, m.ModelExecuted, m.TotalDurationMS, m.SandboxDurationMS,
		m.ModelDurationMS, m.ToolCallCount, m.ModelCallCount, m.ModelProvider, m.ModelName, m.ModelBackend,
		m.PermissionBlockCount, m.FindingCount, m.ModelFindingCount, m.ModelExceptionCount, m.SeverityCountsJSON,
		m.ExceptionCountsJSON, m.RedactionCount, m.At.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	for _, item := range rec.Artifacts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO artifacts(task_id, name, kind, path, digest, size_bytes, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
			item.TaskID, item.Name, item.Kind, item.Path, item.Digest, item.Size, item.At.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	reportAt := rec.Report.CreatedAt
	if reportAt.IsZero() {
		reportAt = time.Now()
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO reports(task_id, json_report, markdown_report, created_at) VALUES(?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET json_report=excluded.json_report, markdown_report=excluded.markdown_report, created_at=excluded.created_at`,
		rec.Task.ID, rec.Report.JSON, rec.Report.Markdown, reportAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveReport 写入最终报告。
func (s *Store) SaveReport(ctx context.Context, taskID string, jsonReport, markdownReport []byte) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO reports(task_id, json_report, markdown_report, created_at)
VALUES(?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
json_report=excluded.json_report,
markdown_report=excluded.markdown_report,
created_at=excluded.created_at
`,
		taskID, jsonReport, markdownReport, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// TaskByID 查询任务。
func (s *Store) TaskByID(ctx context.Context, id string) (Task, error) {
	var task Task
	var createdAt string
	var startedAt, finishedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT task_id, input_type, input_ref, input_digest, repo_path, status, mode, created_at, started_at, finished_at
FROM review_tasks WHERE task_id=?
`, id).Scan(&task.ID, &task.InputType, &task.InputRef, &task.InputDigest, &task.RepoPath, &task.Status, &task.Mode, &createdAt, &startedAt, &finishedAt)
	if err != nil {
		return Task{}, err
	}
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	task.StartedAt = parseNullableTime(startedAt)
	task.FinishedAt = parseNullableTime(finishedAt)
	return task, nil
}

// FindingsByTaskID 查询 findings。
func (s *Store) FindingsByTaskID(ctx context.Context, taskID string) ([]review.Finding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id, status
FROM findings WHERE task_id=?
ORDER BY file, line, rule_id
`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []review.Finding
	for rows.Next() {
		var f review.Finding
		if err := rows.Scan(&f.Severity, &f.Category, &f.File, &f.Line, &f.Title, &f.Evidence, &f.Recommendation, &f.Confidence, &f.Source, &f.RuleID, &f.Status); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ReportByTaskID 查询报告。
func (s *Store) ReportByTaskID(ctx context.Context, taskID string) (Report, error) {
	var rep Report
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
SELECT json_report, markdown_report, created_at FROM reports WHERE task_id=?
`, taskID).Scan(&rep.JSON, &rep.Markdown, &createdAt)
	if err != nil {
		return Report{}, err
	}
	rep.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return rep, nil
}

// SaveDecision 写入权限决策。
func (s *Store) SaveDecision(ctx context.Context, rec DecisionRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO permission_decisions(task_id, command, action, reason, created_at)
VALUES(?, ?, ?, ?, ?)
`, rec.TaskID, rec.Command, rec.Action, rec.Reason, rec.At.UTC().Format(time.RFC3339Nano))
	return err
}

// SaveSandboxRun 写入沙箱记录。
func (s *Store) SaveSandboxRun(ctx context.Context, rec SandboxRunRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sandbox_runs(task_id, command, runtime, status, timeout_ms, output_limit_bytes, env_whitelist, exit_code, stdout_digest, stderr_digest, duration_ms, output, created_at, finished_at, artifact_count)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, rec.TaskID, rec.Command, rec.Runtime, rec.Status, rec.TimeoutMS, rec.OutputLimitBytes, rec.EnvWhitelist, rec.ExitCode, rec.StdoutDigest, rec.StderrDigest, rec.DurationMS, rec.Output, rec.At.UTC().Format(time.RFC3339Nano), nullableTime(rec.FinishedAt), rec.ArtifactCount)
	return err
}

// SaveFilterDecision 写入过滤决策。
func (s *Store) SaveFilterDecision(ctx context.Context, rec FilterDecisionRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO filter_decisions(task_id, target, action, reason, created_at)
VALUES(?, ?, ?, ?, ?)
`, rec.TaskID, rec.Target, rec.Action, rec.Reason, rec.At.UTC().Format(time.RFC3339Nano))
	return err
}

// SaveArtifact 写入产物记录。
func (s *Store) SaveArtifact(ctx context.Context, rec ArtifactRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO artifacts(task_id, name, kind, path, digest, size_bytes, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
`, rec.TaskID, rec.Name, rec.Kind, rec.Path, rec.Digest, rec.Size, rec.At.UTC().Format(time.RFC3339Nano))
	return err
}

// DecisionsByTaskID 查询权限决策。
func (s *Store) DecisionsByTaskID(ctx context.Context, taskID string) ([]DecisionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, command, action, reason, created_at
FROM permission_decisions WHERE task_id=?
ORDER BY id
`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DecisionRecord
	for rows.Next() {
		var rec DecisionRecord
		var createdAt string
		if err := rows.Scan(&rec.TaskID, &rec.Command, &rec.Action, &rec.Reason, &createdAt); err != nil {
			return nil, err
		}
		rec.At, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// SandboxRunsByTaskID 查询沙箱记录。
func (s *Store) SandboxRunsByTaskID(ctx context.Context, taskID string) ([]SandboxRunRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, command, runtime, status, timeout_ms, output_limit_bytes, env_whitelist, exit_code, stdout_digest, stderr_digest, duration_ms, output, created_at, finished_at, artifact_count
FROM sandbox_runs WHERE task_id=?
ORDER BY id
`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SandboxRunRecord
	for rows.Next() {
		var rec SandboxRunRecord
		var createdAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&rec.TaskID, &rec.Command, &rec.Runtime, &rec.Status, &rec.TimeoutMS, &rec.OutputLimitBytes, &rec.EnvWhitelist, &rec.ExitCode, &rec.StdoutDigest, &rec.StderrDigest, &rec.DurationMS, &rec.Output, &createdAt, &finishedAt, &rec.ArtifactCount); err != nil {
			return nil, err
		}
		rec.At, _ = time.Parse(time.RFC3339Nano, createdAt)
		if finishedAt.Valid {
			rec.FinishedAt, _ = time.Parse(time.RFC3339Nano, finishedAt.String)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// FilterDecisionsByTaskID 查询过滤决策。
func (s *Store) FilterDecisionsByTaskID(ctx context.Context, taskID string) ([]FilterDecisionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, target, action, reason, created_at
FROM filter_decisions WHERE task_id=?
ORDER BY id
`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FilterDecisionRecord
	for rows.Next() {
		var rec FilterDecisionRecord
		var createdAt string
		if err := rows.Scan(&rec.TaskID, &rec.Target, &rec.Action, &rec.Reason, &createdAt); err != nil {
			return nil, err
		}
		rec.At, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ArtifactsByTaskID 查询产物记录。
func (s *Store) ArtifactsByTaskID(ctx context.Context, taskID string) ([]ArtifactRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, name, kind, path, digest, size_bytes, created_at
FROM artifacts WHERE task_id=?
ORDER BY id
`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ArtifactRecord
	for rows.Next() {
		var rec ArtifactRecord
		var createdAt string
		if err := rows.Scan(&rec.TaskID, &rec.Name, &rec.Kind, &rec.Path, &rec.Digest, &rec.Size, &createdAt); err != nil {
			return nil, err
		}
		rec.At, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// SaveMetrics 保存指标。
func (s *Store) SaveMetrics(ctx context.Context, rec MetricsRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO metrics(task_id, mode, sandbox_requested, sandbox_executed, model_requested, model_executed, total_duration_ms, sandbox_duration_ms, model_duration_ms, tool_call_count, model_call_count, model_provider, model_name, model_backend, permission_block_count, finding_count, model_finding_count, model_exception_count, severity_counts_json, exception_counts_json, redaction_count, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
mode=excluded.mode,
sandbox_requested=excluded.sandbox_requested,
sandbox_executed=excluded.sandbox_executed,
model_requested=excluded.model_requested,
model_executed=excluded.model_executed,
total_duration_ms=excluded.total_duration_ms,
sandbox_duration_ms=excluded.sandbox_duration_ms,
model_duration_ms=excluded.model_duration_ms,
tool_call_count=excluded.tool_call_count,
model_call_count=excluded.model_call_count,
model_provider=excluded.model_provider,
model_name=excluded.model_name,
model_backend=excluded.model_backend,
permission_block_count=excluded.permission_block_count,
finding_count=excluded.finding_count,
model_finding_count=excluded.model_finding_count,
model_exception_count=excluded.model_exception_count,
severity_counts_json=excluded.severity_counts_json,
exception_counts_json=excluded.exception_counts_json,
redaction_count=excluded.redaction_count,
created_at=excluded.created_at
`, rec.TaskID, rec.Mode, rec.SandboxRequested, rec.SandboxExecuted, rec.ModelRequested, rec.ModelExecuted, rec.TotalDurationMS, rec.SandboxDurationMS, rec.ModelDurationMS, rec.ToolCallCount, rec.ModelCallCount, rec.ModelProvider, rec.ModelName, rec.ModelBackend, rec.PermissionBlockCount, rec.FindingCount, rec.ModelFindingCount, rec.ModelExceptionCount, rec.SeverityCountsJSON, rec.ExceptionCountsJSON, rec.RedactionCount, rec.At.UTC().Format(time.RFC3339Nano))
	return err
}

// MetricsByTaskID 查询指标。
func (s *Store) MetricsByTaskID(ctx context.Context, taskID string) (MetricsSummary, error) {
	var out MetricsSummary
	var createdAt string
	var mode sql.NullString
	var sandboxRequested, sandboxExecuted, modelRequested, modelExecuted sql.NullBool
	err := s.db.QueryRowContext(ctx, `
SELECT task_id, mode, sandbox_requested, sandbox_executed, model_requested, model_executed, total_duration_ms, sandbox_duration_ms, model_duration_ms, tool_call_count, model_call_count, model_provider, model_name, model_backend, permission_block_count, finding_count, model_finding_count, model_exception_count, severity_counts_json, exception_counts_json, redaction_count, created_at
FROM metrics WHERE task_id=?
`, taskID).Scan(&out.TaskID, &mode, &sandboxRequested, &sandboxExecuted, &modelRequested, &modelExecuted, &out.TotalDurationMS, &out.SandboxDurationMS, &out.ModelDurationMS, &out.ToolCallCount, &out.ModelCallCount, &out.ModelProvider, &out.ModelName, &out.ModelBackend, &out.PermissionBlockCount, &out.FindingCount, &out.ModelFindingCount, &out.ModelExceptionCount, &out.SeverityCountsJSON, &out.ExceptionCountsJSON, &out.RedactionCount, &createdAt)
	if err != nil {
		return MetricsSummary{}, err
	}
	out.At, _ = time.Parse(time.RFC3339Nano, createdAt)
	out.Mode = nullableStringPointer(mode)
	out.SandboxRequested = nullableBoolPointer(sandboxRequested)
	out.SandboxExecuted = nullableBoolPointer(sandboxExecuted)
	out.ModelRequested = nullableBoolPointer(modelRequested)
	out.ModelExecuted = nullableBoolPointer(modelExecuted)
	return out, nil
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableBoolPointer(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	return &value.Bool
}

// nullableTime 转换可选时间。
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// parseNullableTime 解析可选时间。
func parseNullableTime(v sql.NullString) time.Time {
	if !v.Valid || v.String == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v.String)
	return t
}
