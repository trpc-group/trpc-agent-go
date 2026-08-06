// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package storage 提供审查任务和结果的持久化存储。
//
// 基于 SQLite 实现，保留接口以便后续切换 SQL 后端。
// 使用 trpc-agent-go 的 session/sqlite 作为底层数据库基础设施。
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// ========== 接口定义 ==========

// Store 是审查存储的接口。
// 定义了所有需要持久化的操作，方便后续替换实现。
type Store interface {
	// 任务管理
	CreateTask(task *ReviewTask) error
	GetTask(taskID string) (*ReviewTask, error)
	UpdateTaskStatus(taskID string, status TaskStatus) error
	ListTasks(limit int) ([]*ReviewTask, error)

	// 审查发现
	SaveFindings(taskID string, findings []findings.Finding) error
	GetFindings(taskID string) ([]findings.Finding, error)

	// 沙箱执行记录
	SaveSandboxRun(run *SandboxRun) error
	GetSandboxRuns(taskID string) ([]*SandboxRun, error)

	// 权限决策记录
	SavePermissionDecision(decision *PermissionDecision) error
	GetPermissionDecisions(taskID string) ([]*PermissionDecision, error)

	// 报告
	SaveReport(taskID string, jsonReport, mdReport string) error
	GetReport(taskID string) (jsonReport, mdReport string, err error)

	// 产物
	SaveArtifact(artifact *Artifact) error
	GetArtifacts(taskID string) ([]*Artifact, error)

	// 生命周期
	Close() error
}

// ========== 数据模型 ==========

// TaskStatus 任务状态。
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// ReviewTask 表示一次审查任务。
type ReviewTask struct {
	TaskID       string     `json:"task_id"`
	Status       TaskStatus `json:"status"`
	InputType    string     `json:"input_type"` // diff_file / repo_path / fixture
	InputPath    string     `json:"input_path"`
	FilesCount   int        `json:"files_count"`
	GoFilesCount int        `json:"go_files_count"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	Duration     string     `json:"duration,omitempty"`
	ErrorMsg     string     `json:"error_msg,omitempty"`
}

// SandboxRun 表示一次沙箱执行记录。
type SandboxRun struct {
	TaskID    string    `json:"task_id"`
	Command   string    `json:"command"`
	Backend   string    `json:"backend"` // local / container / e2b
	ExitCode  int       `json:"exit_code"`
	Output    string    `json:"output"`
	Truncated bool      `json:"truncated"`
	Duration  string    `json:"duration"`
	StartedAt time.Time `json:"started_at"`
}

// PermissionDecision 表示一次权限决策记录。
type PermissionDecision struct {
	TaskID    string    `json:"task_id"`
	ToolName  string    `json:"tool_name"`
	Command   string    `json:"command"`
	Action    string    `json:"action"` // allow / deny / ask
	Reason    string    `json:"reason"`
	DecidedAt time.Time `json:"decided_at"`
}

// Artifact 表示审查过程中产生的产物。
type Artifact struct {
	TaskID       string    `json:"task_id"`
	ArtifactType string    `json:"artifact_type"` // report / log / diff / sandbox_output
	FilePath     string    `json:"file_path"`
	Content      string    `json:"content"`
	Size         int       `json:"size"`
	CreatedAt    time.Time `json:"created_at"`
}

// ========== 实现 ==========

// SQLiteStore 基于 SQLite 的存储实现。
type SQLiteStore struct {
	db  *sql.DB
	svc *sqlite.Service // 框架的 session service（用于初始化和基础设施）
}

// NewSQLiteStore 创建一个新的 SQLite 存储实例。
//
// 参数：
//   - dbPath: SQLite 数据库文件路径，如 "./review.db"
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 使用框架的 session/sqlite 初始化数据库基础设施
	svc, err := sqlite.NewService(db,
		sqlite.WithTablePrefix("cr_"),
	)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 session service 失败: %w", err)
	}

	store := &SQLiteStore{db: db, svc: svc}

	// 创建审查专用的表
	if err := store.initTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化审查表失败: %w", err)
	}

	return store, nil
}

// initTables 创建审查专用的表。
func (s *SQLiteStore) initTables() error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS cr_review_tasks (
			task_id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'pending',
			input_type TEXT NOT NULL,
			input_path TEXT NOT NULL,
			files_count INTEGER DEFAULT 0,
			go_files_count INTEGER DEFAULT 0,
			started_at DATETIME NOT NULL,
			completed_at DATETIME,
			duration TEXT,
			error_msg TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS cr_findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			severity TEXT NOT NULL,
			category TEXT NOT NULL,
			rule_id TEXT NOT NULL,
			title TEXT NOT NULL,
			file_path TEXT NOT NULL,
			line INTEGER NOT NULL,
			evidence TEXT,
			recommendation TEXT,
			confidence REAL,
			source TEXT,
			FOREIGN KEY (task_id) REFERENCES cr_review_tasks(task_id)
		)`,
		`CREATE TABLE IF NOT EXISTS cr_sandbox_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			command TEXT NOT NULL,
			backend TEXT DEFAULT 'local',
			exit_code INTEGER DEFAULT 0,
			output TEXT,
			truncated BOOLEAN DEFAULT 0,
			duration TEXT,
			started_at DATETIME NOT NULL,
			FOREIGN KEY (task_id) REFERENCES cr_review_tasks(task_id)
		)`,
		`CREATE TABLE IF NOT EXISTS cr_permission_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			tool_name TEXT,
			command TEXT,
			action TEXT NOT NULL,
			reason TEXT,
			decided_at DATETIME NOT NULL,
			FOREIGN KEY (task_id) REFERENCES cr_review_tasks(task_id)
		)`,
		`CREATE TABLE IF NOT EXISTS cr_reports (
			task_id TEXT PRIMARY KEY,
			json_report TEXT,
			md_report TEXT,
			FOREIGN KEY (task_id) REFERENCES cr_review_tasks(task_id)
		)`,
		`CREATE TABLE IF NOT EXISTS cr_artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			artifact_type TEXT NOT NULL,
			file_path TEXT,
			content TEXT,
			size INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			FOREIGN KEY (task_id) REFERENCES cr_review_tasks(task_id)
		)`,
	}

	for _, table := range tables {
		if _, err := s.db.Exec(table); err != nil {
			return fmt.Errorf("创建表失败: %w", err)
		}
	}

	// 创建索引
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_findings_task ON cr_findings(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sandbox_task ON cr_sandbox_runs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_permission_task ON cr_permission_decisions(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON cr_review_tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_task ON cr_artifacts(task_id)`,
	}
	for _, idx := range indexes {
		if _, err := s.db.Exec(idx); err != nil {
			return fmt.Errorf("创建索引失败: %w", err)
		}
	}

	return nil
}

// Close 关闭存储连接。
func (s *SQLiteStore) Close() error {
	if s.svc != nil {
		return s.svc.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// ========== 任务管理 ==========

// CreateTask 创建一个新的审查任务。
func (s *SQLiteStore) CreateTask(task *ReviewTask) error {
	_, err := s.db.Exec(
		`INSERT INTO cr_review_tasks (task_id, status, input_type, input_path, files_count, go_files_count, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		task.TaskID, task.Status, task.InputType, task.InputPath,
		task.FilesCount, task.GoFilesCount, task.StartedAt,
	)
	return err
}

// GetTask 获取审查任务。
func (s *SQLiteStore) GetTask(taskID string) (*ReviewTask, error) {
	row := s.db.QueryRow(
		`SELECT task_id, status, input_type, input_path, files_count, go_files_count,
		        started_at, completed_at, duration, error_msg
		 FROM cr_review_tasks WHERE task_id = ?`, taskID,
	)

	var task ReviewTask
	var completedAt sql.NullTime
	var duration, errorMsg sql.NullString
	err := row.Scan(
		&task.TaskID, &task.Status, &task.InputType, &task.InputPath,
		&task.FilesCount, &task.GoFilesCount,
		&task.StartedAt, &completedAt, &duration, &errorMsg,
	)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}
	if duration.Valid {
		task.Duration = duration.String
	}
	if errorMsg.Valid {
		task.ErrorMsg = errorMsg.String
	}
	return &task, nil
}

// UpdateTaskStatus 更新任务状态。
func (s *SQLiteStore) UpdateTaskStatus(taskID string, status TaskStatus) error {
	var completedAt *time.Time
	duration := ""
	if status == TaskStatusCompleted || status == TaskStatusFailed {
		now := time.Now()
		completedAt = &now
		// 计算耗时
		task, err := s.GetTask(taskID)
		if err == nil {
			duration = now.Sub(task.StartedAt).Round(time.Millisecond).String()
		}
	}

	_, err := s.db.Exec(
		`UPDATE cr_review_tasks SET status = ?, completed_at = ?, duration = ? WHERE task_id = ?`,
		status, completedAt, duration, taskID,
	)
	return err
}

// ListTasks 列出最近的审查任务。
func (s *SQLiteStore) ListTasks(limit int) ([]*ReviewTask, error) {
	rows, err := s.db.Query(
		`SELECT task_id, status, input_type, input_path, files_count, go_files_count,
		        started_at, completed_at, duration, error_msg
		 FROM cr_review_tasks ORDER BY started_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*ReviewTask
	for rows.Next() {
		var task ReviewTask
		var completedAt sql.NullTime
		var duration, errorMsg sql.NullString
		err := rows.Scan(
			&task.TaskID, &task.Status, &task.InputType, &task.InputPath,
			&task.FilesCount, &task.GoFilesCount,
			&task.StartedAt, &completedAt, &duration, &errorMsg,
		)
		if err != nil {
			return nil, err
		}
		if completedAt.Valid {
			task.CompletedAt = &completedAt.Time
		}
		if duration.Valid {
			task.Duration = duration.String
		}
		if errorMsg.Valid {
			task.ErrorMsg = errorMsg.String
		}
		tasks = append(tasks, &task)
	}
	return tasks, nil
}

// ========== 审查发现 ==========

// SaveFindings 保存审查发现（批量插入）。
func (s *SQLiteStore) SaveFindings(taskID string, findingsList []findings.Finding) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO cr_findings (task_id, severity, category, rule_id, title, file_path, line, evidence, recommendation, confidence, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range findingsList {
		_, err := stmt.Exec(
			taskID, f.Severity, f.Category, f.RuleID, f.Title,
			f.File, f.Line, f.Evidence, f.Recommendation, f.Confidence, f.Source,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetFindings 获取任务的所有审查发现。
func (s *SQLiteStore) GetFindings(taskID string) ([]findings.Finding, error) {
	rows, err := s.db.Query(
		`SELECT severity, category, rule_id, title, file_path, line, evidence, recommendation, confidence, source
		 FROM cr_findings WHERE task_id = ? ORDER BY
		 CASE severity WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,
		 file_path, line`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []findings.Finding
	for rows.Next() {
		var f findings.Finding
		err := rows.Scan(
			&f.Severity, &f.Category, &f.RuleID, &f.Title,
			&f.File, &f.Line, &f.Evidence, &f.Recommendation, &f.Confidence, &f.Source,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, nil
}

// ========== 沙箱执行记录 ==========

// SaveSandboxRun 保存沙箱执行记录。
func (s *SQLiteStore) SaveSandboxRun(run *SandboxRun) error {
	_, err := s.db.Exec(
		`INSERT INTO cr_sandbox_runs (task_id, command, backend, exit_code, output, truncated, duration, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.TaskID, run.Command, run.Backend, run.ExitCode,
		run.Output, run.Truncated, run.Duration, run.StartedAt,
	)
	return err
}

// GetSandboxRuns 获取任务的所有沙箱执行记录。
func (s *SQLiteStore) GetSandboxRuns(taskID string) ([]*SandboxRun, error) {
	rows, err := s.db.Query(
		`SELECT task_id, command, backend, exit_code, output, truncated, duration, started_at
		 FROM cr_sandbox_runs WHERE task_id = ? ORDER BY started_at`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*SandboxRun
	for rows.Next() {
		var run SandboxRun
		err := rows.Scan(
			&run.TaskID, &run.Command, &run.Backend, &run.ExitCode,
			&run.Output, &run.Truncated, &run.Duration, &run.StartedAt,
		)
		if err != nil {
			return nil, err
		}
		runs = append(runs, &run)
	}
	return runs, nil
}

// ========== 权限决策记录 ==========

// SavePermissionDecision 保存权限决策记录。
func (s *SQLiteStore) SavePermissionDecision(decision *PermissionDecision) error {
	_, err := s.db.Exec(
		`INSERT INTO cr_permission_decisions (task_id, tool_name, command, action, reason, decided_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		decision.TaskID, decision.ToolName, decision.Command,
		decision.Action, decision.Reason, decision.DecidedAt,
	)
	return err
}

// GetPermissionDecisions 获取任务的所有权限决策记录。
func (s *SQLiteStore) GetPermissionDecisions(taskID string) ([]*PermissionDecision, error) {
	rows, err := s.db.Query(
		`SELECT task_id, tool_name, command, action, reason, decided_at
		 FROM cr_permission_decisions WHERE task_id = ? ORDER BY decided_at`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var decisions []*PermissionDecision
	for rows.Next() {
		var d PermissionDecision
		err := rows.Scan(
			&d.TaskID, &d.ToolName, &d.Command, &d.Action, &d.Reason, &d.DecidedAt,
		)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, &d)
	}
	return decisions, nil
}

// ========== 报告 ==========

// SaveReport 保存审查报告。
func (s *SQLiteStore) SaveReport(taskID string, jsonReport, mdReport string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO cr_reports (task_id, json_report, md_report) VALUES (?, ?, ?)`,
		taskID, jsonReport, mdReport,
	)
	return err
}

// GetReport 获取审查报告。
func (s *SQLiteStore) GetReport(taskID string) (jsonReport, mdReport string, err error) {
	row := s.db.QueryRow(
		`SELECT json_report, md_report FROM cr_reports WHERE task_id = ?`, taskID,
	)
	err = row.Scan(&jsonReport, &mdReport)
	return
}

// ========== 辅助方法 ==========

// SaveFullResult 一次性保存完整的审查结果。
func (s *SQLiteStore) SaveFullResult(task *ReviewTask, findingsList []findings.Finding, report any) error {
	// 保存任务
	if err := s.CreateTask(task); err != nil {
		return fmt.Errorf("保存任务失败: %w", err)
	}

	// 保存 findings
	if len(findingsList) > 0 {
		if err := s.SaveFindings(task.TaskID, findingsList); err != nil {
			return fmt.Errorf("保存 findings 失败: %w", err)
		}
	}

	// 保存报告（如果是 map 或 struct，序列化为 JSON）
	if report != nil {
		jsonBytes, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("序列化报告失败: %w", err)
		}
		if err := s.SaveReport(task.TaskID, string(jsonBytes), ""); err != nil {
			return fmt.Errorf("保存报告失败: %w", err)
		}
	}

	return nil
}

// GetTaskSummary 获取任务摘要（任务 + findings 数量 + 状态）。
func (s *SQLiteStore) GetTaskSummary(taskID string) (map[string]any, error) {
	task, err := s.GetTask(taskID)
	if err != nil {
		return nil, err
	}

	// 统计 findings
	var high, medium, low, info int
	rows, err := s.db.Query(
		`SELECT severity, COUNT(*) FROM cr_findings WHERE task_id = ? GROUP BY severity`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sev string
		var count int
		if err := rows.Scan(&sev, &count); err != nil {
			return nil, err
		}
		switch sev {
		case "high":
			high = count
		case "medium":
			medium = count
		case "low":
			low = count
		case "info":
			info = count
		}
	}

	return map[string]any{
		"task":   task,
		"high":   high,
		"medium": medium,
		"low":    low,
		"info":   info,
		"total":  high + medium + low + info,
	}, nil
}

// ========== 产物管理 ==========

// SaveArtifact 保存审查产物。
func (s *SQLiteStore) SaveArtifact(artifact *Artifact) error {
	_, err := s.db.Exec(
		`INSERT INTO cr_artifacts (task_id, artifact_type, file_path, content, size, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		artifact.TaskID, artifact.ArtifactType, artifact.FilePath,
		artifact.Content, artifact.Size, artifact.CreatedAt,
	)
	return err
}

// GetArtifacts 获取任务的所有产物。
func (s *SQLiteStore) GetArtifacts(taskID string) ([]*Artifact, error) {
	rows, err := s.db.Query(
		`SELECT task_id, artifact_type, file_path, content, size, created_at
		 FROM cr_artifacts WHERE task_id = ? ORDER BY created_at`, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []*Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.TaskID, &a.ArtifactType, &a.FilePath,
			&a.Content, &a.Size, &a.CreatedAt); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, &a)
	}
	return artifacts, nil
}
