// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package storage

import (
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	tmpDir := t.TempDir()
	store, err := NewSQLiteStore(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore 失败: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// ========== 接口合规性 ==========

func TestStoreInterface(t *testing.T) {
	// 验证 SQLiteStore 实现了 Store 接口
	var _ Store = (*SQLiteStore)(nil)
}

// ========== 任务管理 ==========

func TestCreateAndGetTask(t *testing.T) {
	store := newTestStore(t)

	task := &ReviewTask{
		TaskID:    "task-001",
		Status:    TaskStatusPending,
		InputType: "diff_file",
		InputPath: "test.diff",
		StartedAt: time.Now(),
	}

	if err := store.CreateTask(task); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	got, err := store.GetTask("task-001")
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}

	if got.TaskID != "task-001" {
		t.Errorf("TaskID = %q, 期望 %q", got.TaskID, "task-001")
	}
	if got.Status != TaskStatusPending {
		t.Errorf("Status = %q, 期望 %q", got.Status, TaskStatusPending)
	}
	if got.InputType != "diff_file" {
		t.Errorf("InputType = %q, 期望 %q", got.InputType, "diff_file")
	}
}

func TestGetTaskNotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.GetTask("nonexistent")
	if err == nil {
		t.Error("不存在的任务应返回错误")
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	store := newTestStore(t)

	task := &ReviewTask{
		TaskID:    "task-002",
		Status:    TaskStatusPending,
		InputType: "diff_file",
		InputPath: "test.diff",
		StartedAt: time.Now(),
	}
	store.CreateTask(task)

	// 更新为 running
	if err := store.UpdateTaskStatus("task-002", TaskStatusRunning); err != nil {
		t.Fatalf("UpdateTaskStatus(running) 失败: %v", err)
	}

	got, _ := store.GetTask("task-002")
	if got.Status != TaskStatusRunning {
		t.Errorf("Status = %q, 期望 %q", got.Status, TaskStatusRunning)
	}

	// 更新为 completed
	time.Sleep(10 * time.Millisecond) // 确保有时间差
	if err := store.UpdateTaskStatus("task-002", TaskStatusCompleted); err != nil {
		t.Fatalf("UpdateTaskStatus(completed) 失败: %v", err)
	}

	got, _ = store.GetTask("task-002")
	if got.Status != TaskStatusCompleted {
		t.Errorf("Status = %q, 期望 %q", got.Status, TaskStatusCompleted)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt 不应为 nil")
	}
	if got.Duration == "" {
		t.Error("Duration 不应为空")
	}
}

func TestListTasks(t *testing.T) {
	store := newTestStore(t)

	for i := 0; i < 5; i++ {
		store.CreateTask(&ReviewTask{
			TaskID:    "task-" + string(rune('0'+i)),
			Status:    TaskStatusPending,
			InputType: "diff_file",
			InputPath: "test.diff",
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	tasks, err := store.ListTasks(3)
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("ListTasks(3) 返回 %d 条，期望 3", len(tasks))
	}
}

// ========== 审查发现 ==========

func TestSaveAndGetFindings(t *testing.T) {
	store := newTestStore(t)

	// 先创建任务
	store.CreateTask(&ReviewTask{
		TaskID: "task-f01", Status: TaskStatusPending,
		InputType: "diff_file", InputPath: "test.diff", StartedAt: time.Now(),
	})

	// 保存 findings
	input := []findings.Finding{
		*findings.NewFinding(
			findings.SeverityHigh, findings.CategorySecurity, "SEC-001",
			"硬编码密钥", "config.go", 10,
			`APIKey = "sk-xxx"`, "使用环境变量", 0.95, "rule:sec",
		),
		*findings.NewFinding(
			findings.SeverityMedium, findings.CategoryResource, "RES-001",
			"资源未关闭", "handler.go", 25,
			`f, err := os.Open(path)`, "添加 defer f.Close()", 0.80, "rule:res",
		),
	}

	if err := store.SaveFindings("task-f01", input); err != nil {
		t.Fatalf("SaveFindings 失败: %v", err)
	}

	// 读取 findings
	got, err := store.GetFindings("task-f01")
	if err != nil {
		t.Fatalf("GetFindings 失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetFindings 返回 %d 条，期望 2", len(got))
	}

	// 验证按严重级别排序（high 在前）
	if got[0].Severity != findings.SeverityHigh {
		t.Errorf("第一条 Severity = %q, 期望 high", got[0].Severity)
	}
	if got[1].Severity != findings.SeverityMedium {
		t.Errorf("第二条 Severity = %q, 期望 medium", got[1].Severity)
	}
}

func TestGetFindingsEmpty(t *testing.T) {
	store := newTestStore(t)

	store.CreateTask(&ReviewTask{
		TaskID: "task-empty", Status: TaskStatusPending,
		InputType: "diff_file", InputPath: "test.diff", StartedAt: time.Now(),
	})

	got, err := store.GetFindings("task-empty")
	if err != nil {
		t.Fatalf("GetFindings 失败: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("GetFindings 返回 %d 条，期望 0", len(got))
	}
}

// ========== 沙箱执行记录 ==========

func TestSaveAndGetSandboxRuns(t *testing.T) {
	store := newTestStore(t)

	store.CreateTask(&ReviewTask{
		TaskID: "task-s01", Status: TaskStatusPending,
		InputType: "diff_file", InputPath: "test.diff", StartedAt: time.Now(),
	})

	run := &SandboxRun{
		TaskID:    "task-s01",
		Command:   "go vet ./...",
		Backend:   "local",
		ExitCode:  0,
		Output:    "ok",
		Truncated: false,
		Duration:  "1.2s",
		StartedAt: time.Now(),
	}

	if err := store.SaveSandboxRun(run); err != nil {
		t.Fatalf("SaveSandboxRun 失败: %v", err)
	}

	runs, err := store.GetSandboxRuns("task-s01")
	if err != nil {
		t.Fatalf("GetSandboxRuns 失败: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("GetSandboxRuns 返回 %d 条，期望 1", len(runs))
	}
	if runs[0].Command != "go vet ./..." {
		t.Errorf("Command = %q, 期望 %q", runs[0].Command, "go vet ./...")
	}
}

// ========== 权限决策记录 ==========

func TestSaveAndGetPermissionDecisions(t *testing.T) {
	store := newTestStore(t)

	store.CreateTask(&ReviewTask{
		TaskID: "task-p01", Status: TaskStatusPending,
		InputType: "diff_file", InputPath: "test.diff", StartedAt: time.Now(),
	})

	decision := &PermissionDecision{
		TaskID:    "task-p01",
		ToolName:  "workspace_exec",
		Command:   "go test ./...",
		Action:    "allow",
		Reason:    "",
		DecidedAt: time.Now(),
	}

	if err := store.SavePermissionDecision(decision); err != nil {
		t.Fatalf("SavePermissionDecision 失败: %v", err)
	}

	decisions, err := store.GetPermissionDecisions("task-p01")
	if err != nil {
		t.Fatalf("GetPermissionDecisions 失败: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("GetPermissionDecisions 返回 %d 条，期望 1", len(decisions))
	}
	if decisions[0].Action != "allow" {
		t.Errorf("Action = %q, 期望 %q", decisions[0].Action, "allow")
	}
}

// ========== 报告 ==========

func TestSaveAndGetReport(t *testing.T) {
	store := newTestStore(t)

	store.CreateTask(&ReviewTask{
		TaskID: "task-r01", Status: TaskStatusPending,
		InputType: "diff_file", InputPath: "test.diff", StartedAt: time.Now(),
	})

	jsonReport := `{"task_id":"task-r01","findings":[]}`
	mdReport := "# 代码审查报告\n\n无问题。"

	if err := store.SaveReport("task-r01", jsonReport, mdReport); err != nil {
		t.Fatalf("SaveReport 失败: %v", err)
	}

	gotJSON, gotMD, err := store.GetReport("task-r01")
	if err != nil {
		t.Fatalf("GetReport 失败: %v", err)
	}
	if gotJSON != jsonReport {
		t.Errorf("JSON 报告不匹配")
	}
	if gotMD != mdReport {
		t.Errorf("MD 报告不匹配")
	}
}

// ========== 辅助方法 ==========

func TestGetTaskSummary(t *testing.T) {
	store := newTestStore(t)

	store.CreateTask(&ReviewTask{
		TaskID: "task-sum", Status: TaskStatusCompleted,
		InputType: "diff_file", InputPath: "test.diff", StartedAt: time.Now(),
	})

	store.SaveFindings("task-sum", []findings.Finding{
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySecurity, "SEC-001", "t", "a.go", 1, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySecurity, "SEC-002", "t", "b.go", 2, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityMedium, findings.CategoryResource, "RES-001", "t", "c.go", 3, "e", "r", 0.8, "s"),
	})

	summary, err := store.GetTaskSummary("task-sum")
	if err != nil {
		t.Fatalf("GetTaskSummary 失败: %v", err)
	}

	if summary["high"] != 2 {
		t.Errorf("high = %v, 期望 2", summary["high"])
	}
	if summary["medium"] != 1 {
		t.Errorf("medium = %v, 期望 1", summary["medium"])
	}
	if summary["total"] != 3 {
		t.Errorf("total = %v, 期望 3", summary["total"])
	}
}
