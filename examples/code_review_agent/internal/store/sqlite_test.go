//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

func tempDBPath(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "review_test_*.db")
	require.NoError(t, err)
	path := f.Name()
	f.Close()
	os.Remove(path)
	t.Cleanup(func() { os.Remove(path) })
	return path
}

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(tempDBPath(t))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGetTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := &reviewmodel.ReviewTask{
		ID:          "task-001",
		Status:      reviewmodel.StatusRunning,
		DryRun:      true,
		SandboxType: "fake",
		CreatedAt:   time.Now(),
	}
	err := s.CreateTask(ctx, task)
	require.NoError(t, err)

	got, err := s.GetTask(ctx, "task-001")
	require.NoError(t, err)
	assert.Equal(t, "task-001", got.ID)
	assert.Equal(t, reviewmodel.StatusRunning, got.Status)
	assert.True(t, got.DryRun)
}

func TestSaveAndGetFindings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := &reviewmodel.ReviewTask{
		ID:          "task-002",
		Status:      reviewmodel.StatusRunning,
		SandboxType: "local",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, s.CreateTask(ctx, task))

	f1 := &reviewmodel.Finding{
		Severity:   reviewmodel.SeverityHigh,
		Category:   reviewmodel.CategorySecurity,
		FilePath:   "file.go",
		Line:       10,
		Title:      "Test finding",
		Confidence: 0.9,
		Source:     "rule",
		RuleID:     "GO-SEC-001",
	}
	require.NoError(t, s.SaveFinding(ctx, "task-002", f1))

	f2 := &reviewmodel.Finding{
		Severity:   reviewmodel.SeverityMedium,
		Category:   reviewmodel.CategoryResource,
		FilePath:   "file2.go",
		Line:       20,
		Title:      "Test finding 2",
		Confidence: 0.7,
		Source:     "rule",
		RuleID:     "GO-RES-001",
	}
	require.NoError(t, s.SaveFinding(ctx, "task-002", f2))

	findings, err := s.GetFindings(ctx, "task-002")
	require.NoError(t, err)
	assert.Len(t, findings, 2)
}

func TestSaveSandboxRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := &reviewmodel.ReviewTask{
		ID:          "task-003",
		Status:      reviewmodel.StatusRunning,
		SandboxType: "local",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, s.CreateTask(ctx, task))

	run := &reviewmodel.SandboxRun{
		ID:         "run-001",
		Command:    "go vet ./...",
		ExitCode:   0,
		Stdout:     "ok",
		DurationMs: 1500,
	}
	require.NoError(t, s.SaveSandboxRun(ctx, "task-003", run))
}

func TestSavePermissionDecision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := &reviewmodel.ReviewTask{
		ID:          "task-004",
		Status:      reviewmodel.StatusRunning,
		SandboxType: "local",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, s.CreateTask(ctx, task))

	dec := &reviewmodel.PermissionDecision{
		ToolName: "go_vet",
		Action:   "allow",
	}
	require.NoError(t, s.SavePermissionDecision(ctx, "task-004", dec))
}

func TestFinalize(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := &reviewmodel.ReviewTask{
		ID:          "task-005",
		Status:      reviewmodel.StatusRunning,
		SandboxType: "local",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, s.CreateTask(ctx, task))

	task.Status = reviewmodel.StatusCompleted
	task.FindingsTotal = 5
	task.FindingsHigh = 3
	now := time.Now()
	require.NoError(t, s.Finalize(ctx, "task-005", task))

	got, err := s.GetTask(ctx, "task-005")
	require.NoError(t, err)
	assert.Equal(t, reviewmodel.StatusCompleted, got.Status)
	assert.Equal(t, 5, got.FindingsTotal)
	assert.NotNil(t, got.CompletedAt)
	assert.True(t, got.CompletedAt.After(now.Add(-time.Second)))
}

func TestFinalizeInvalidStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := &reviewmodel.ReviewTask{
		ID:          "task-006",
		Status:      reviewmodel.StatusRunning,
		SandboxType: "local",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, s.CreateTask(ctx, task))

	task.Status = reviewmodel.StatusRunning
	err := s.Finalize(ctx, "task-006", task)
	assert.ErrorIs(t, err, ErrInvalidTransition)
}

func TestStoreClose(t *testing.T) {
	s := newTestStore(t)
	assert.NoError(t, s.Close())
	// Double close should not panic
	assert.NoError(t, s.Close())
}

func TestGetSandboxRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := &reviewmodel.ReviewTask{
		ID:          "task-sr-001",
		Status:      reviewmodel.StatusRunning,
		SandboxType: "local",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, s.CreateTask(ctx, task))

	run1 := &reviewmodel.SandboxRun{
		ID: "run-1", Command: "go vet", ExitCode: 0, DurationMs: 100,
	}
	run2 := &reviewmodel.SandboxRun{
		ID: "run-2", Command: "go test", TimedOut: true, DurationMs: 30000,
	}
	require.NoError(t, s.SaveSandboxRun(ctx, "task-sr-001", run1))
	require.NoError(t, s.SaveSandboxRun(ctx, "task-sr-001", run2))

	runs, err := s.GetSandboxRuns(ctx, "task-sr-001")
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.Equal(t, "go vet", runs[0].Command)
	assert.True(t, runs[1].TimedOut)
}

func TestGetPermissionDecisions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := &reviewmodel.ReviewTask{
		ID:          "task-pd-001",
		Status:      reviewmodel.StatusRunning,
		SandboxType: "local",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, s.CreateTask(ctx, task))

	d1 := &reviewmodel.PermissionDecision{ToolName: "go_vet", Action: "allow"}
	d2 := &reviewmodel.PermissionDecision{ToolName: "rm", Action: "deny", Reason: "in deny list"}
	require.NoError(t, s.SavePermissionDecision(ctx, "task-pd-001", d1))
	require.NoError(t, s.SavePermissionDecision(ctx, "task-pd-001", d2))

	decs, err := s.GetPermissionDecisions(ctx, "task-pd-001")
	require.NoError(t, err)
	assert.Len(t, decs, 2)
	assert.Equal(t, "allow", decs[0].Action)
	assert.Equal(t, "deny", decs[1].Action)
}

func TestFullTaskQueryability(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	taskID := "task-full-001"
	task := &reviewmodel.ReviewTask{
		ID:                  taskID,
		Status:              reviewmodel.StatusCompletedWithWarnings,
		SandboxType:         "container",
		DryRun:              false,
		FindingsTotal:       3,
		FindingsCritical:    1,
		FindingsHigh:        2,
		PermissionDenyCount: 1,
		TotalDurationMs:     5000,
		SandboxDurationMs:   3000,
		CreatedAt:           time.Now(),
	}
	require.NoError(t, s.CreateTask(ctx, task))

	// Save findings.
	for i, f := range []reviewmodel.Finding{
		{Severity: "critical", Category: "security", FilePath: "a.go", Line: 1, Title: "F1", Confidence: 0.9, Source: "rule", RuleID: "R1"},
		{Severity: "high", Category: "goroutine", FilePath: "b.go", Line: 2, Title: "F2", Confidence: 0.8, Source: "rule", RuleID: "R2"},
		{Severity: "high", Category: "resource", FilePath: "c.go", Line: 3, Title: "F3", Confidence: 0.7, Source: "rule", RuleID: "R3"},
	} {
		require.NoError(t, s.SaveFinding(ctx, taskID, &f), "save finding %d", i)
	}

	// Save sandbox runs.
	require.NoError(t, s.SaveSandboxRun(ctx, taskID, &reviewmodel.SandboxRun{
		ID: "sr-1", Command: "go vet", ExitCode: 1, TimedOut: false, DurationMs: 1000,
	}))

	// Save permission decisions.
	require.NoError(t, s.SavePermissionDecision(ctx, taskID, &reviewmodel.PermissionDecision{
		ToolName: "rm", Action: "deny", Reason: "in deny list",
	}))

	// Finalize.
	task.Status = reviewmodel.StatusCompletedWithWarnings
	require.NoError(t, s.Finalize(ctx, taskID, task))

	// Verify all queries.
	got, err := s.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, reviewmodel.StatusCompletedWithWarnings, got.Status)
	assert.Equal(t, 3, got.FindingsTotal)
	assert.Equal(t, 1, got.PermissionDenyCount)

	findings, err := s.GetFindings(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, findings, 3)

	runs, err := s.GetSandboxRuns(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, 1, runs[0].ExitCode)

	decs, err := s.GetPermissionDecisions(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, decs, 1)
	assert.Equal(t, "deny", decs[0].Action)
}
