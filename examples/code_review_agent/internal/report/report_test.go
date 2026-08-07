//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package report

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

func TestGenerateJSON(t *testing.T) {
	tmpDir := t.TempDir()
	jsonPath := filepath.Join(tmpDir, "report.json")

	task := &reviewmodel.ReviewTask{
		ID:               "test-001",
		Status:           reviewmodel.StatusCompletedWithWarnings,
		FindingsTotal:    3,
		FindingsCritical: 1,
		FindingsHigh:     2,
		SandboxType:      "fake",
		TotalDurationMs:  1000,
	}
	findings := []reviewmodel.Finding{
		{
			Severity:       reviewmodel.SeverityCritical,
			Category:       reviewmodel.CategorySecurity,
			FilePath:       "file.go",
			Line:           10,
			Title:          "Security issue",
			Evidence:       "exec.Command(cmd)",
			Recommendation: "sanitize input",
			Confidence:     0.9,
			Source:         "rule",
			RuleID:         "GO-SEC-001",
		},
	}
	sandboxRuns := []reviewmodel.SandboxRun{
		{ID: "run-001", Command: "go vet", ExitCode: 0, DurationMs: 500},
	}
	permissionDecisions := []reviewmodel.PermissionDecision{
		{ToolName: "go_vet", Action: "allow"},
	}

	report := NewReport(task, findings, sandboxRuns, permissionDecisions, 1, 2)
	err := GenerateJSON(report, jsonPath)
	require.NoError(t, err)

	data, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"severity": "critical"`)
	assert.Contains(t, string(data), `"category": "security"`)
}

func TestGenerateMarkdown(t *testing.T) {
	tmpDir := t.TempDir()
	mdPath := filepath.Join(tmpDir, "report.md")

	task := &reviewmodel.ReviewTask{
		ID:            "test-002",
		Status:        reviewmodel.StatusCompleted,
		FindingsTotal: 1,
	}
	findings := []reviewmodel.Finding{
		{
			Severity:       reviewmodel.SeverityHigh,
			Category:       reviewmodel.CategoryResource,
			FilePath:       "file.go",
			Line:           5,
			Title:          "Resource issue",
			Evidence:       "os.Open(path)",
			Recommendation: "use defer close",
			Confidence:     0.8,
			Source:         "rule",
			RuleID:         "GO-RES-001",
		},
	}

	report := NewReport(task, findings, nil, nil, 1, 1)
	err := GenerateMarkdown(report, mdPath)
	require.NoError(t, err)

	data, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "# Code Review Report")
	assert.Contains(t, string(data), "Resource issue")
	assert.Contains(t, string(data), "GO-RES-001")
}

func TestReportSummary(t *testing.T) {
	task := &reviewmodel.ReviewTask{
		ID:                "test-003",
		Status:            reviewmodel.StatusCompleted,
		TotalDurationMs:   2000,
		SandboxDurationMs: 1500,
		ToolCallCount:     2,
	}
	findings := []reviewmodel.Finding{
		{Severity: reviewmodel.SeverityCritical, Category: reviewmodel.CategorySecurity},
		{Severity: reviewmodel.SeverityHigh, Category: reviewmodel.CategoryGoroutine},
		{Severity: reviewmodel.SeverityHigh, Category: reviewmodel.CategoryResource},
		{Severity: reviewmodel.SeverityMedium, Category: reviewmodel.CategoryTest},
		{Severity: reviewmodel.SeverityMedium, NeedsHumanReview: true, Title: "review me"},
	}
	sandboxRuns := []reviewmodel.SandboxRun{
		{ID: "r1", Command: "go vet", TimedOut: true, DurationMs: 30000},
	}

	report := NewReport(task, findings, sandboxRuns, nil, 3, 5)
	assert.Equal(t, 3, report.Summary.TotalFiles)
	assert.Equal(t, 5, report.Summary.TotalHunks)
	assert.Equal(t, 1, report.Summary.SeverityCounts[reviewmodel.SeverityCritical])
	assert.Equal(t, 2, report.Summary.SeverityCounts[reviewmodel.SeverityHigh])
	assert.Equal(t, 2, report.Summary.SeverityCounts[reviewmodel.SeverityMedium])
	assert.Len(t, report.Summary.HumanReviewItems, 1)
	assert.Contains(t, report.Summary.SandboxSummary, "timed_out")
	assert.Len(t, report.Summary.Monitoring.AnomalyTypes, 1)
}

func TestEscapeMarkdown(t *testing.T) {
	assert.Equal(t, "hello world", escapeMarkdown("hello world"))
	assert.Contains(t, escapeMarkdown("`code`"), "\\`")
	assert.Contains(t, escapeMarkdown("a|b"), "\\|")
}

func TestReportWithSandboxError(t *testing.T) {
	task := &reviewmodel.ReviewTask{ID: "task-err", Status: reviewmodel.StatusCompleted}
	report := NewReport(task, nil, nil, nil, 1, 0)
	assert.Contains(t, report.Summary.SandboxSummary, "No sandbox executions")

	// Test with timed-out sandbox run
	sandboxRuns := []reviewmodel.SandboxRun{
		{ID: "r-err", Command: "go test", TimedOut: true, DurationMs: 30000},
	}
	report2 := NewReport(task, nil, sandboxRuns, nil, 1, 0)
	assert.Contains(t, report2.Summary.SandboxSummary, "timed_out")
}

func TestGenerateReportWithNewFinding(t *testing.T) {
	task := &reviewmodel.ReviewTask{
		ID:        "test-new",
		Status:    reviewmodel.StatusCompletedWithWarnings,
		CreatedAt: time.Now(),
	}
	report := NewReport(task, nil, nil, nil, 0, 0)
	assert.NotZero(t, report.GeneratedAt)
	assert.NotNil(t, report.Summary.Monitoring)
}
