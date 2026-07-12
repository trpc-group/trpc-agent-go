//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) *SQLiteStorage {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewSQLiteStorage(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStorage_SaveAndGetTask(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	taskID := "task-" + uuid.NewString()[:8]
	task := &ReviewTask{
		ID:        taskID,
		InputType: "diff",
		InputPath: "/tmp/test.diff",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	require.NoError(t, s.SaveTask(ctx, task))

	got, err := s.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, taskID, got.ID)
	require.Equal(t, "diff", got.InputType)
	require.Equal(t, "running", got.Status)
}

func TestStorage_UpdateTaskStatus(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	taskID := "task-" + uuid.NewString()[:8]
	task := &ReviewTask{
		ID:        taskID,
		InputType: "diff",
		InputPath: "/tmp/test.diff",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	require.NoError(t, s.SaveTask(ctx, task))

	completedAt := time.Now()
	require.NoError(t, s.UpdateTaskStatus(ctx, taskID, "completed",
		completedAt, 1500))

	got, err := s.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "completed", got.Status)
	require.Equal(t, int64(1500), got.TotalDurationMs)
	require.NotNil(t, got.CompletedAt)
}

func TestStorage_SaveAndGetFindings(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	taskID := "task-" + uuid.NewString()[:8]
	task := &ReviewTask{
		ID: taskID, InputType: "diff", InputPath: "x",
		Status: "running", CreatedAt: time.Now(),
	}
	require.NoError(t, s.SaveTask(ctx, task))

	finding := &Finding{
		ID:             "f-1",
		Severity:       SeverityCritical,
		Category:       "security",
		File:           "auth/handler.go",
		Line:           10,
		Title:          "SQL injection",
		Evidence:       "SELECT * FROM",
		Recommendation: "Use parameterized queries",
		Confidence:     0.9,
		Source:         SourceRule,
		RuleID:         "SQL_INJECTION",
	}
	require.NoError(t, s.SaveFinding(ctx, taskID, finding))

	findings, err := s.GetFindingsByTask(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	require.Equal(t, SeverityCritical, findings[0].Severity)
	require.Equal(t, "SQL_INJECTION", findings[0].RuleID)
}

func TestStorage_SaveSandboxRun(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	taskID := "task-" + uuid.NewString()[:8]
	task := &ReviewTask{
		ID: taskID, InputType: "diff", InputPath: "x",
		Status: "running", CreatedAt: time.Now(),
	}
	require.NoError(t, s.SaveTask(ctx, task))

	run := &SandboxRun{
		ID:       "sr-1",
		TaskID:   taskID,
		Command:  "go test ./...",
		Status:   SandboxStatusSuccess,
		Stdout:   "PASS",
		ExitCode: 0,
	}
	require.NoError(t, s.SaveSandboxRun(ctx, run))

	runs, err := s.GetSandboxRunsByTask(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "go test ./...", runs[0].Command)
}

func TestStorage_SavePermissionDecision(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	taskID := "task-" + uuid.NewString()[:8]
	task := &ReviewTask{
		ID: taskID, InputType: "diff", InputPath: "x",
		Status: "running", CreatedAt: time.Now(),
	}
	require.NoError(t, s.SaveTask(ctx, task))

	rec := &PermissionRecord{
		ID:        "pd-1",
		TaskID:    taskID,
		Command:   "rm -rf /",
		Decision:  DecisionDeny,
		Reason:    "command denied",
		Timestamp: time.Now().Format(time.RFC3339Nano),
	}
	require.NoError(t, s.SavePermissionDecision(ctx, rec))
}

func TestStorage_SaveReport(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	taskID := "task-" + uuid.NewString()[:8]
	task := &ReviewTask{
		ID: taskID, InputType: "diff", InputPath: "x",
		Status: "running", CreatedAt: time.Now(),
	}
	require.NoError(t, s.SaveTask(ctx, task))

	report := &ReviewReport{
		ID:         "r-1",
		TaskID:     taskID,
		ReportJSON: `{"findings": []}`,
		ReportMD:   "# Report",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, s.SaveReport(ctx, report))
}

func TestStorage_SaveMonitoring(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	taskID := "task-" + uuid.NewString()[:8]
	task := &ReviewTask{
		ID: taskID, InputType: "diff", InputPath: "x",
		Status: "running", CreatedAt: time.Now(),
	}
	require.NoError(t, s.SaveTask(ctx, task))

	m := &MonitoringSummary{
		ID:              taskID + "-monitor",
		TaskID:          taskID,
		TotalDurationMs: 500,
		FindingCount:    3,
		SeverityCounts: map[string]int{
			SeverityCritical: 1,
			SeverityHigh:     2,
		},
		ErrorTypes: map[string]int{},
	}
	require.NoError(t, s.SaveMonitoring(ctx, m))
}

func TestStorage_InitSchema(t *testing.T) {
	s := newTestStorage(t)
	// Schema is initialized in NewSQLiteStorage; verify tables exist.
	ctx := context.Background()
	require.NoError(t, s.InitSchema(ctx))
}

func TestStorage_SaveAndQueryArtifacts(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	taskID := "task-" + uuid.NewString()[:8]
	require.NoError(t, s.SaveTask(ctx, &ReviewTask{
		ID: taskID, InputType: "diff", InputPath: "x",
		Status: "running", CreatedAt: time.Now(),
	}))
	require.NoError(t, s.SaveArtifact(ctx, &Artifact{
		ID: "artifact-1", TaskID: taskID, Name: "review_report.json",
		MIMEType: "application/json", Size: 42, CreatedAt: time.Now(),
	}))
	artifacts, err := s.GetArtifactsByTask(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, int64(42), artifacts[0].Size)
}
