package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "golens-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to init db: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

func TestInitDB(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// 验证表是否存在
	tables := []string{
		"review_tasks", "diff_summaries", "findings",
		"sandbox_runs", "permission_decisions", "monitoring_summaries",
		"artifacts", "review_reports",
	}

	for _, table := range tables {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}
}

func TestSaveAndGetTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task := &ReviewTask{
		TaskID:    "task_001",
		RepoPath:  "/path/to/repo",
		DiffFile:  "changes.diff",
		Status:    "running",
		CreatedAt: time.Now(),
	}

	if err := SaveTask(db, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	// 验证任务已保存
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM review_tasks WHERE task_id = ?", "task_001").Scan(&count)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 task, got %d", count)
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task := &ReviewTask{
		TaskID: "task_002",
		Status: "running",
	}
	SaveTask(db, task)

	if err := UpdateTaskStatus(db, "task_002", "completed", ""); err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}

	var status string
	err := db.QueryRow("SELECT status FROM review_tasks WHERE task_id = ?", "task_002").Scan(&status)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %s, want completed", status)
	}
}

func TestSaveFinding(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task := &ReviewTask{TaskID: "task_003", Status: "running"}
	SaveTask(db, task)

	finding := &Finding{
		TaskID:         "task_003",
		Severity:       "critical",
		Category:       "security",
		File:           "db.go",
		Line:           10,
		Title:          "SQL Injection",
		Description:    "SQL injection vulnerability",
		Evidence:       "fmt.Sprintf(...)",
		Recommendation: "Use parameterized queries",
		Confidence:     0.95,
		Source:         "rule",
		RuleID:         "SEC001",
	}

	if err := SaveFinding(db, finding); err != nil {
		t.Fatalf("SaveFinding() error = %v", err)
	}

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM findings WHERE task_id = ?", "task_003").Scan(&count)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 finding, got %d", count)
	}
}

func TestSaveSandboxRun(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task := &ReviewTask{TaskID: "task_004", Status: "running"}
	SaveTask(db, task)

	run := &SandboxRun{
		TaskID:     "task_004",
		ScriptName: "go_vet",
		Command:    "go vet ./...",
		ExitCode:   0,
		Stdout:     "",
		Stderr:     "",
		DurationMs: 1500,
	}

	if err := SaveSandboxRun(db, run); err != nil {
		t.Fatalf("SaveSandboxRun() error = %v", err)
	}

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sandbox_runs WHERE task_id = ?", "task_004").Scan(&count)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 sandbox run, got %d", count)
	}
}

func TestSavePermissionDecision(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task := &ReviewTask{TaskID: "task_005", Status: "running"}
	SaveTask(db, task)

	decision := &PermissionDecision{
		TaskID:   "task_005",
		Command:  "go vet",
		Decision: "allow",
		Reason:   "command in whitelist",
	}

	if err := SavePermissionDecision(db, decision); err != nil {
		t.Fatalf("SavePermissionDecision() error = %v", err)
	}

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM permission_decisions WHERE task_id = ?", "task_005").Scan(&count)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 decision, got %d", count)
	}
}

func TestSaveMonitoringSummary(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task := &ReviewTask{TaskID: "task_006", Status: "running"}
	SaveTask(db, task)

	summary := &MonitoringSummary{
		TaskID:                "task_006",
		TotalDurationMs:       5000,
		SandboxDurationMs:     2000,
		ToolCallsCount:        2,
		PermissionBlocksCount: 0,
		FindingsCount:         3,
		SeverityDistribution:  map[string]int{"critical": 1, "high": 1, "medium": 1},
		ExceptionDistribution: map[string]int{},
	}

	if err := SaveMonitoringSummary(db, summary); err != nil {
		t.Fatalf("SaveMonitoringSummary() error = %v", err)
	}

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM monitoring_summaries WHERE task_id = ?", "task_006").Scan(&count)
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 summary, got %d", count)
	}
}

func TestSaveReviewReport(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task := &ReviewTask{TaskID: "task_007", Status: "running"}
	SaveTask(db, task)

	report := &ReviewReport{
		TaskID:     "task_007",
		ReportJSON: `{"task_id":"task_007"}`,
		ReportMD:   "# Report",
	}

	if err := SaveReviewReport(db, report); err != nil {
		t.Fatalf("SaveReviewReport() error = %v", err)
	}

	got, err := GetTaskReport(db, "task_007")
	if err != nil {
		t.Fatalf("GetTaskReport() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetTaskReport() returned nil")
	}
	if got.TaskID != "task_007" {
		t.Errorf("TaskID = %s, want task_007", got.TaskID)
	}
}

func TestCalculateMonitoring(t *testing.T) {
	startTime := time.Now().Add(-5 * time.Second)

	findings := []Finding{
		{Severity: "critical", Category: "security"},
		{Severity: "high", Category: "resource"},
	}

	sandboxRuns := []SandboxRun{
		{DurationMs: 1000},
		{DurationMs: 2000},
	}

	summary := CalculateMonitoring("task_008", findings, sandboxRuns, nil, startTime)

	if summary.TaskID != "task_008" {
		t.Errorf("TaskID = %s, want task_008", summary.TaskID)
	}

	if summary.FindingsCount != 2 {
		t.Errorf("FindingsCount = %d, want 2", summary.FindingsCount)
	}

	if summary.SandboxDurationMs != 3000 {
		t.Errorf("SandboxDurationMs = %d, want 3000", summary.SandboxDurationMs)
	}

	if summary.SeverityDistribution["critical"] != 1 {
		t.Errorf("critical count = %d, want 1", summary.SeverityDistribution["critical"])
	}
}
