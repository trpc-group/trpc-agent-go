//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package store 提供 SQLite 数据库存储功能
package store

import (
	crypto_rand "crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ReviewTask 审查任务
type ReviewTask struct {
	ID           int       `json:"id"`
	TaskID       string    `json:"task_id"`
	RepoPath     string    `json:"repo_path"`
	DiffFile     string    `json:"diff_file"`
	Status       string    `json:"status"`
	TotalFiles   int       `json:"total_files"`
	TotalAdded   int       `json:"total_added"`
	TotalDeleted int       `json:"total_deleted"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Finding 审查发现
type Finding struct {
	ID             int       `json:"id"`
	TaskID         string    `json:"task_id"`
	Severity       string    `json:"severity"`
	Category       string    `json:"category"`
	File           string    `json:"file"`
	Line           int       `json:"line"`
	Title          string    `json:"title"`
	Description    string    `json:"description,omitempty"`
	Evidence       string    `json:"evidence,omitempty"`
	Recommendation string    `json:"recommendation,omitempty"`
	Confidence     float64   `json:"confidence"`
	Source         string    `json:"source"`
	RuleID         string    `json:"rule_id"`
	ReviewStatus   string    `json:"review_status,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Warning is a low-confidence finding retained for human review.
type Warning = Finding

// SandboxRun 沙箱执行记录
type SandboxRun struct {
	ID         int       `json:"id"`
	TaskID     string    `json:"task_id"`
	ScriptName string    `json:"script_name"`
	Command    string    `json:"command"`
	ExitCode   int       `json:"exit_code"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	DurationMs int       `json:"duration_ms"`
	Truncated  bool      `json:"truncated"`
	ErrorType  string    `json:"error_type,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// PermissionDecision 权限决策记录
type PermissionDecision struct {
	ID        int       `json:"id"`
	TaskID    string    `json:"task_id"`
	Command   string    `json:"command"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MonitoringSummary 监控摘要
type MonitoringSummary struct {
	ID                    int            `json:"id"`
	TaskID                string         `json:"task_id"`
	TotalDurationMs       int            `json:"total_duration_ms"`
	SandboxDurationMs     int            `json:"sandbox_duration_ms"`
	ToolCallsCount        int            `json:"tool_calls_count"`
	PermissionBlocksCount int            `json:"permission_blocks_count"`
	FindingsCount         int            `json:"findings_count"`
	SeverityDistribution  map[string]int `json:"severity_distribution"`
	ExceptionDistribution map[string]int `json:"exception_distribution"`
	CreatedAt             time.Time      `json:"created_at"`
}

// DiffSummary Diff 摘要
type DiffSummary struct {
	ID        int       `json:"id"`
	TaskID    string    `json:"task_id"`
	FilePath  string    `json:"file_path"`
	Status    string    `json:"status"`
	Additions int       `json:"additions"`
	Deletions int       `json:"deletions"`
	CreatedAt time.Time `json:"created_at"`
}

// InitDB 初始化数据库
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// SQLite foreign keys are connection-local; keep one connection so every
	// operation observes the same constraint setting.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS review_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT UNIQUE NOT NULL,
		repo_path TEXT,
		diff_file TEXT,
		status TEXT DEFAULT 'pending',
		total_files INTEGER DEFAULT 0,
		total_additions INTEGER DEFAULT 0,
		total_deletions INTEGER DEFAULT 0,
		error_message TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS diff_summaries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		file_path TEXT NOT NULL,
		status TEXT,
		additions INTEGER DEFAULT 0,
		deletions INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES review_tasks(task_id)
	);

	CREATE TABLE IF NOT EXISTS findings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		severity TEXT NOT NULL,
		category TEXT NOT NULL,
		file_path TEXT NOT NULL,
		line_number INTEGER,
		title TEXT NOT NULL,
		description TEXT,
		evidence TEXT,
		recommendation TEXT,
		confidence REAL DEFAULT 1.0,
		source TEXT DEFAULT 'rule',
		rule_id TEXT,
			review_status TEXT NOT NULL DEFAULT 'finding',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES review_tasks(task_id)
	);

	CREATE TABLE IF NOT EXISTS sandbox_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		script_name TEXT NOT NULL,
		command TEXT,
		exit_code INTEGER,
		stdout TEXT,
		stderr TEXT,
		duration_ms INTEGER,
		truncated BOOLEAN DEFAULT FALSE,
			error_type TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES review_tasks(task_id)
	);

	CREATE TABLE IF NOT EXISTS permission_decisions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		command TEXT NOT NULL,
		decision TEXT NOT NULL,
		reason TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES review_tasks(task_id)
	);

	CREATE TABLE IF NOT EXISTS monitoring_summaries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		total_duration_ms INTEGER,
		sandbox_duration_ms INTEGER,
		tool_calls_count INTEGER DEFAULT 0,
		permission_blocks_count INTEGER DEFAULT 0,
		findings_count INTEGER DEFAULT 0,
		severity_distribution TEXT,
		exception_distribution TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES review_tasks(task_id)
	);

	CREATE TABLE IF NOT EXISTS artifacts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		artifact_type TEXT NOT NULL,
		file_path TEXT,
		content TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES review_tasks(task_id)
	);

	CREATE TABLE IF NOT EXISTS review_reports (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		report_json TEXT,
		report_md TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES review_tasks(task_id)
	);

	CREATE INDEX IF NOT EXISTS idx_findings_task_id ON findings(task_id);
	CREATE INDEX IF NOT EXISTS idx_findings_severity ON findings(severity);
	CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task_id ON sandbox_runs(task_id);
	CREATE INDEX IF NOT EXISTS idx_permission_decisions_task_id ON permission_decisions(task_id);
	CREATE INDEX IF NOT EXISTS idx_artifacts_task_id ON artifacts(task_id);
	CREATE INDEX IF NOT EXISTS idx_review_reports_task_id ON review_reports(task_id);
	`

	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Keep existing SQLite databases usable after adding audit columns.
	for _, migration := range []string{
		`ALTER TABLE findings ADD COLUMN review_status TEXT NOT NULL DEFAULT 'finding'`,
		`ALTER TABLE sandbox_runs ADD COLUMN error_type TEXT`,
	} {
		if _, err := db.Exec(migration); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate schema: %w", err)
		}
	}
	return db, nil
}

// GenerateTaskID 生成任务 ID
func GenerateTaskID() string {
	return fmt.Sprintf("task_%s-%s", time.Now().Format("20060102-150405"), randomHex(6))
}

func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := crypto_rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("%x", b)[:n]
}

// SaveTask 保存任务
func SaveTask(db *sql.DB, task *ReviewTask) error {
	_, err := db.Exec(
		"INSERT INTO review_tasks (task_id, repo_path, diff_file, status, total_files, total_additions, total_deletions) VALUES (?, ?, ?, ?, ?, ?, ?)",
		task.TaskID, task.RepoPath, task.DiffFile, task.Status, task.TotalFiles, task.TotalAdded, task.TotalDeleted,
	)
	return err
}

// UpdateTaskStatus 更新任务状态
func UpdateTaskStatus(db *sql.DB, taskID, status, errMsg string) error {
	_, err := db.Exec(
		"UPDATE review_tasks SET status = ?, error_message = ? WHERE task_id = ?",
		status, errMsg, taskID,
	)
	return err
}

// UpdateTaskTotals 回写任务的 diff 汇总。
func UpdateTaskTotals(db *sql.DB, taskID string, totalFiles, totalAdded, totalDeleted int) error {
	_, err := db.Exec(`UPDATE review_tasks SET total_files = ?, total_additions = ?, total_deletions = ? WHERE task_id = ?`, totalFiles, totalAdded, totalDeleted, taskID)
	return err
}

// SaveDiffSummary 保存 Diff 摘要
func SaveDiffSummary(db *sql.DB, taskID string, filePath string, status string, additions int, deletions int) error {
	_, err := db.Exec(
		`INSERT INTO diff_summaries (task_id, file_path, status, additions, deletions)
		 VALUES (?, ?, ?, ?, ?)`,
		taskID, filePath, status, additions, deletions,
	)
	return err
}

// SaveFindings 批量保存 findings（使用事务）
func SaveFindings(db *sql.DB, findings []Finding) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO findings (task_id, severity, category, file_path, line_number, title, description, evidence, recommendation, confidence, source, rule_id, review_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, f := range findings {
		if _, err := stmt.Exec(
			f.TaskID, f.Severity, f.Category, f.File, f.Line, f.Title,
			f.Description, f.Evidence, f.Recommendation, f.Confidence,
			f.Source, f.RuleID, findingStatus(f),
		); err != nil {
			return fmt.Errorf("insert finding: %w", err)
		}
	}

	return tx.Commit()
}

func findingStatus(f Finding) string {
	if f.ReviewStatus != "" {
		return f.ReviewStatus
	}
	if f.Confidence < 0.7 {
		return "warning"
	}
	return "finding"
}

// SaveFinding 保存单个 finding
func SaveFinding(db *sql.DB, f *Finding) error {
	_, err := db.Exec(
		`INSERT INTO findings (task_id, severity, category, file_path, line_number, title, description, evidence, recommendation, confidence, source, rule_id, review_status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.TaskID, f.Severity, f.Category, f.File, f.Line, f.Title, f.Description,
		f.Evidence, f.Recommendation, f.Confidence, f.Source, f.RuleID, findingStatus(*f),
	)
	return err
}

// SaveSandboxRun 保存沙箱执行记录
func SaveSandboxRun(db *sql.DB, run *SandboxRun) error {
	_, err := db.Exec(
		`INSERT INTO sandbox_runs (task_id, script_name, command, exit_code, stdout, stderr, duration_ms, truncated, error_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.TaskID, run.ScriptName, run.Command, run.ExitCode, run.Stdout, run.Stderr, run.DurationMs, run.Truncated, run.ErrorType,
	)
	return err
}

// SavePermissionDecision 保存权限决策
func SavePermissionDecision(db *sql.DB, decision *PermissionDecision) error {
	_, err := db.Exec(
		`INSERT INTO permission_decisions (task_id, command, decision, reason) VALUES (?, ?, ?, ?)`,
		decision.TaskID, decision.Command, decision.Decision, decision.Reason,
	)
	return err
}

// SaveMonitoringSummary 保存监控摘要
func SaveMonitoringSummary(db *sql.DB, summary *MonitoringSummary) error {
	severityJSON, _ := json.Marshal(summary.SeverityDistribution)
	exceptionJSON, _ := json.Marshal(summary.ExceptionDistribution)

	_, err := db.Exec(
		`INSERT INTO monitoring_summaries (task_id, total_duration_ms, sandbox_duration_ms, tool_calls_count, permission_blocks_count, findings_count, severity_distribution, exception_distribution)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		summary.TaskID, summary.TotalDurationMs, summary.SandboxDurationMs, summary.ToolCallsCount, summary.PermissionBlocksCount, summary.FindingsCount,
		string(severityJSON), string(exceptionJSON),
	)
	return err
}

// Artifact 存储
type Artifact struct {
	ID           int       `json:"id"`
	TaskID       string    `json:"task_id"`
	ArtifactType string    `json:"artifact_type"`
	FilePath     string    `json:"file_path,omitempty"`
	Content      string    `json:"content,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ReviewReport 审查报告存储
type ReviewReport struct {
	ID         int       `json:"id"`
	TaskID     string    `json:"task_id"`
	ReportJSON string    `json:"report_json"`
	ReportMD   string    `json:"report_md"`
	CreatedAt  time.Time `json:"created_at"`
}

// SaveArtifact 保存 artifact
func SaveArtifact(db *sql.DB, artifact *Artifact) error {
	_, err := db.Exec(
		`INSERT INTO artifacts (task_id, artifact_type, file_path, content) VALUES (?, ?, ?, ?)`,
		artifact.TaskID, artifact.ArtifactType, artifact.FilePath, artifact.Content,
	)
	return err
}

// SaveReviewReport 保存审查报告
func SaveReviewReport(db *sql.DB, report *ReviewReport) error {
	_, err := db.Exec(
		`INSERT INTO review_reports (task_id, report_json, report_md) VALUES (?, ?, ?)`,
		report.TaskID, report.ReportJSON, report.ReportMD,
	)
	return err
}

// GetTaskReport 获取任务报告
func GetTaskReport(db *sql.DB, taskID string) (*ReviewReport, error) {
	report := &ReviewReport{}
	err := db.QueryRow(
		"SELECT id, task_id, report_json, report_md, created_at FROM review_reports WHERE task_id = ? ORDER BY created_at DESC LIMIT 1",
		taskID,
	).Scan(&report.ID, &report.TaskID, &report.ReportJSON, &report.ReportMD, &report.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return report, err
}

func GetTask(db *sql.DB, taskID string) (*ReviewTask, error) {
	task := &ReviewTask{}
	err := db.QueryRow(`SELECT id, task_id, repo_path, diff_file, status,
		total_files, total_additions, total_deletions, error_message, created_at
		FROM review_tasks WHERE task_id = ?`, taskID).Scan(
		&task.ID, &task.TaskID, &task.RepoPath, &task.DiffFile, &task.Status,
		&task.TotalFiles, &task.TotalAdded, &task.TotalDeleted, &task.Error, &task.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return task, err
}

func ListDiffSummaries(db *sql.DB, taskID string) ([]DiffSummary, error) {
	rows, err := db.Query(`SELECT id, task_id, file_path, status, additions, deletions, created_at
		FROM diff_summaries WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []DiffSummary
	for rows.Next() {
		var item DiffSummary
		if err := rows.Scan(&item.ID, &item.TaskID, &item.FilePath, &item.Status, &item.Additions, &item.Deletions, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func ListFindings(db *sql.DB, taskID string) ([]Finding, error) {
	rows, err := db.Query(`SELECT id, task_id, severity, category, file_path, line_number, title,
		description, evidence, recommendation, confidence, source, rule_id, review_status, created_at
		FROM findings WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Finding
	for rows.Next() {
		var item Finding
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Severity, &item.Category, &item.File, &item.Line,
			&item.Title, &item.Description, &item.Evidence, &item.Recommendation, &item.Confidence,
			&item.Source, &item.RuleID, &item.ReviewStatus, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func ListSandboxRuns(db *sql.DB, taskID string) ([]SandboxRun, error) {
	rows, err := db.Query(`SELECT id, task_id, script_name, command, exit_code, stdout, stderr,
		duration_ms, truncated, error_type, created_at FROM sandbox_runs WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SandboxRun
	for rows.Next() {
		var item SandboxRun
		if err := rows.Scan(&item.ID, &item.TaskID, &item.ScriptName, &item.Command, &item.ExitCode,
			&item.Stdout, &item.Stderr, &item.DurationMs, &item.Truncated, &item.ErrorType, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func ListPermissionDecisions(db *sql.DB, taskID string) ([]PermissionDecision, error) {
	rows, err := db.Query(`SELECT id, task_id, command, decision, reason, created_at
		FROM permission_decisions WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PermissionDecision
	for rows.Next() {
		var item PermissionDecision
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Command, &item.Decision, &item.Reason, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func GetMonitoringSummary(db *sql.DB, taskID string) (*MonitoringSummary, error) {
	var item MonitoringSummary
	var severityJSON, exceptionJSON string
	err := db.QueryRow(`SELECT id, task_id, total_duration_ms, sandbox_duration_ms, tool_calls_count,
		permission_blocks_count, findings_count, severity_distribution, exception_distribution, created_at
		FROM monitoring_summaries WHERE task_id = ? ORDER BY id DESC LIMIT 1`, taskID).Scan(
		&item.ID, &item.TaskID, &item.TotalDurationMs, &item.SandboxDurationMs, &item.ToolCallsCount,
		&item.PermissionBlocksCount, &item.FindingsCount, &severityJSON, &exceptionJSON, &item.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(severityJSON), &item.SeverityDistribution); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(exceptionJSON), &item.ExceptionDistribution); err != nil {
		return nil, err
	}
	return &item, nil
}

func ListArtifacts(db *sql.DB, taskID string) ([]Artifact, error) {
	rows, err := db.Query(`SELECT id, task_id, artifact_type, file_path, content, created_at
		FROM artifacts WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Artifact
	for rows.Next() {
		var item Artifact
		if err := rows.Scan(&item.ID, &item.TaskID, &item.ArtifactType, &item.FilePath, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// CalculateMonitoring 计算监控摘要
func CalculateMonitoring(taskID string, findings []Finding, sandboxRuns []SandboxRun, permissionDecisions []PermissionDecision, startTime time.Time) *MonitoringSummary {
	totalDurationMs := int(time.Since(startTime).Milliseconds())
	sandboxDurationMs := 0
	for _, run := range sandboxRuns {
		sandboxDurationMs += run.DurationMs
	}

	severityDistribution := map[string]int{
		"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0,
	}
	for _, f := range findings {
		severityDistribution[f.Severity]++
	}
	permissionBlocks := 0
	for _, decision := range permissionDecisions {
		if decision.Decision != "allow" {
			permissionBlocks++
		}
	}
	exceptionDistribution := map[string]int{}
	for _, run := range sandboxRuns {
		switch {
		case run.ErrorType != "":
			exceptionDistribution[run.ErrorType]++
		case run.ExitCode == -2:
			exceptionDistribution["timeout"]++
		case run.ExitCode != 0:
			exceptionDistribution["non_zero_exit"]++
		}
	}

	return &MonitoringSummary{
		TaskID:                taskID,
		TotalDurationMs:       totalDurationMs,
		SandboxDurationMs:     sandboxDurationMs,
		ToolCallsCount:        len(sandboxRuns),
		PermissionBlocksCount: permissionBlocks,
		FindingsCount:         len(findings),
		SeverityDistribution:  severityDistribution,
		ExceptionDistribution: exceptionDistribution,
	}
}
