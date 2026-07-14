//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package storage

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSQLiteStorage_CreateAndGetReviewTask(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	task := ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "running",
		StartedAt: time.Now(),
	}

	if err := storage.CreateReviewTask(ctx, task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	retrieved, err := storage.GetReviewTask(ctx, taskID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Expected task to exist")
	}

	if retrieved.ID != taskID {
		t.Errorf("Expected ID %s, got %s", taskID, retrieved.ID)
	}

	if retrieved.DiffPath != "test.diff" {
		t.Errorf("Expected DiffPath 'test.diff', got '%s'", retrieved.DiffPath)
	}

	if retrieved.Status != "running" {
		t.Errorf("Expected Status 'running', got '%s'", retrieved.Status)
	}
}

func TestSQLiteStorage_UpdateReviewTask(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	task := ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "running",
		StartedAt: time.Now(),
	}

	if err := storage.CreateReviewTask(ctx, task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	completedAt := time.Now()
	task.Status = "completed"
	task.CompletedAt = &completedAt
	task.TotalTimeMs = 1000

	if err := storage.UpdateReviewTask(ctx, task); err != nil {
		t.Fatalf("Failed to update task: %v", err)
	}

	retrieved, err := storage.GetReviewTask(ctx, taskID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if retrieved.Status != "completed" {
		t.Errorf("Expected Status 'completed', got '%s'", retrieved.Status)
	}

	if retrieved.TotalTimeMs != 1000 {
		t.Errorf("Expected TotalTimeMs 1000, got %d", retrieved.TotalTimeMs)
	}
}

func TestSQLiteStorage_CreateAndGetFindings(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	if err := storage.CreateReviewTask(ctx, ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "running",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	finding := Finding{
		ID:          uuid.New().String(),
		TaskID:      taskID,
		RuleID:      "GOROUTINE_LEAK",
		Filepath:    "pkg/worker/worker.go",
		LineNumber:  10,
		Severity:    SeverityHigh,
		Message:     "Goroutine leak detected",
		Suggestion:  "Use proper synchronization",
		Confidence:  0.9,
		NeedsReview: false,
		CreatedAt:   time.Now(),
	}

	if err := storage.CreateFinding(ctx, finding); err != nil {
		t.Fatalf("Failed to create finding: %v", err)
	}

	findings, err := storage.GetFindingsByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("Failed to get findings: %v", err)
	}

	if len(findings) != 1 {
		t.Errorf("Expected 1 finding, got %d", len(findings))
	}

	if findings[0].RuleID != "GOROUTINE_LEAK" {
		t.Errorf("Expected RuleID 'GOROUTINE_LEAK', got '%s'", findings[0].RuleID)
	}

	if findings[0].Severity != SeverityHigh {
		t.Errorf("Expected Severity 'HIGH', got '%s'", findings[0].Severity)
	}
}

func TestSQLiteStorage_CreateAndGetSandboxRuns(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	if err := storage.CreateReviewTask(ctx, ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "running",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	run := SandboxRun{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Command:    "go vet ./...",
		Output:     "no issues found",
		Error:      "",
		ExitCode:   0,
		TimedOut:   false,
		DurationMs: 500,
		CreatedAt:  time.Now(),
	}

	if err := storage.CreateSandboxRun(ctx, run); err != nil {
		t.Fatalf("Failed to create sandbox run: %v", err)
	}

	runs, err := storage.GetSandboxRunsByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("Failed to get sandbox runs: %v", err)
	}

	if len(runs) != 1 {
		t.Errorf("Expected 1 sandbox run, got %d", len(runs))
	}

	if runs[0].Command != "go vet ./..." {
		t.Errorf("Expected Command 'go vet ./...', got '%s'", runs[0].Command)
	}

	if runs[0].ExitCode != 0 {
		t.Errorf("Expected ExitCode 0, got %d", runs[0].ExitCode)
	}
}

func TestSQLiteStorage_CreateAndGetPermissionRecords(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	if err := storage.CreateReviewTask(ctx, ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "running",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	record := PermissionRecord{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		Command:   "rm -rf /",
		Action:    "DENY",
		Reason:    "Command is denied",
		CreatedAt: time.Now(),
	}

	if err := storage.CreatePermissionRecord(ctx, record); err != nil {
		t.Fatalf("Failed to create permission record: %v", err)
	}

	records, err := storage.GetPermissionRecords(ctx, taskID)
	if err != nil {
		t.Fatalf("Failed to get permission records: %v", err)
	}

	if len(records) != 1 {
		t.Errorf("Expected 1 permission record, got %d", len(records))
	}

	if records[0].Action != "DENY" {
		t.Errorf("Expected Action 'DENY', got '%s'", records[0].Action)
	}
}

func TestSQLiteStorage_CreateAndGetReport(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	if err := storage.CreateReviewTask(ctx, ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "completed",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	reportID := uuid.New().String()
	report := Report{
		ID:        reportID,
		TaskID:    taskID,
		Content:   "# Code Review Report\n\nSummary: ...",
		Format:    "markdown",
		CreatedAt: time.Now(),
	}

	if err := storage.CreateReport(ctx, report); err != nil {
		t.Fatalf("Failed to create report: %v", err)
	}

	retrieved, err := storage.GetReport(ctx, reportID)
	if err != nil {
		t.Fatalf("Failed to get report: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Expected report to exist")
	}

	if retrieved.ID != reportID {
		t.Errorf("Expected ID %s, got %s", reportID, retrieved.ID)
	}

	if retrieved.Format != "markdown" {
		t.Errorf("Expected Format 'markdown', got '%s'", retrieved.Format)
	}
}

func TestSQLiteStorage_CreateAndGetArtifacts(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	if err := storage.CreateReviewTask(ctx, ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "running",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	artifact := Artifact{
		ID:          uuid.New().String(),
		TaskID:      taskID,
		Name:        "diff_file.diff",
		Path:        "output/diff_file.diff",
		ContentType: "text/plain",
		Size:        1024,
		CreatedAt:   time.Now(),
	}

	if err := storage.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("Failed to create artifact: %v", err)
	}

	artifacts, err := storage.GetArtifactsByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("Failed to get artifacts: %v", err)
	}

	if len(artifacts) != 1 {
		t.Fatalf("Expected 1 artifact, got %d", len(artifacts))
	}

	if artifacts[0].Name != "diff_file.diff" {
		t.Fatalf("Expected Name 'diff_file.diff', got '%s'", artifacts[0].Name)
	}

	if artifacts[0].ContentType != "text/plain" {
		t.Fatalf("Expected ContentType 'text/plain', got '%s'", artifacts[0].ContentType)
	}

	if artifacts[0].Size != 1024 {
		t.Fatalf("Expected Size 1024, got %d", artifacts[0].Size)
	}
}

func TestSQLiteStorage_CreateAndGetTelemetryMetrics(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	if err := storage.CreateReviewTask(ctx, ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "completed",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	metrics := TelemetryMetrics{
		ID:                     uuid.New().String(),
		TaskID:                 taskID,
		TotalReviewTimeMs:      5000,
		SandboxExecutionTimeMs: 2000,
		SandboxExecutions:      3,
		ToolCalls:              10,
		PermissionBlocks:       2,
		TotalFindings:          15,
		Errors:                 1,
		TasksCompleted:         1,
		TasksFailed:            0,
		FindingsBySeverityJSON: `{"HIGH": 5, "MEDIUM": 8, "LOW": 2}`,
		CreatedAt:              time.Now(),
	}

	if err := storage.CreateTelemetryMetrics(ctx, metrics); err != nil {
		t.Fatalf("Failed to create telemetry metrics: %v", err)
	}

	results, err := storage.GetTelemetryMetricsByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("Failed to get telemetry metrics: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 telemetry metrics entry, got %d", len(results))
	}

	if results[0].TotalFindings != 15 {
		t.Fatalf("Expected TotalFindings 15, got %d", results[0].TotalFindings)
	}

	if results[0].SandboxExecutions != 3 {
		t.Fatalf("Expected SandboxExecutions 3, got %d", results[0].SandboxExecutions)
	}

	if results[0].PermissionBlocks != 2 {
		t.Fatalf("Expected PermissionBlocks 2, got %d", results[0].PermissionBlocks)
	}
}

func TestSQLiteStorage_GetReviewTask_NonExistent(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	retrieved, err := storage.GetReviewTask(ctx, "non-existent-id")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Error("Expected nil for non-existent task")
	}
}

func TestSQLiteStorage_GetReport_NonExistent(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	retrieved, err := storage.GetReport(ctx, "non-existent-id")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Error("Expected nil for non-existent report")
	}
}

func TestSQLiteStorage_GetFindingsByTask_NoFindings(t *testing.T) {
	storage, err := NewSQLiteStorage(":memory:")
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	if err := storage.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	taskID := uuid.New().String()
	if err := storage.CreateReviewTask(ctx, ReviewTask{
		ID:        taskID,
		DiffPath:  "test.diff",
		RepoPath:  ".",
		Status:    "completed",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	findings, err := storage.GetFindingsByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("Failed to get findings: %v", err)
	}

	if len(findings) != 0 {
		t.Errorf("Expected 0 findings, got %d", len(findings))
	}
}
