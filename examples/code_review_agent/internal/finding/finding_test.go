//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finding

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSeverityConstants(t *testing.T) {
	assert.Equal(t, Severity("critical"), SeverityCritical)
	assert.Equal(t, Severity("high"), SeverityHigh)
	assert.Equal(t, Severity("medium"), SeverityMedium)
	assert.Equal(t, Severity("low"), SeverityLow)
	assert.Equal(t, Severity("warning"), SeverityWarning)
	assert.Equal(t, Severity("info"), SeverityInfo)
}

func TestCategoryConstants(t *testing.T) {
	assert.Equal(t, Category("security"), CategorySecurity)
	assert.Equal(t, Category("goroutine_leak"), CategoryGoroutineLeak)
	assert.Equal(t, Category("resource_leak"), CategoryResourceLeak)
	assert.Equal(t, Category("error_handling"), CategoryErrorHandling)
	assert.Equal(t, Category("missing_test"), CategoryMissingTest)
	assert.Equal(t, Category("db_lifecycle"), CategoryDBLifecycle)
	assert.Equal(t, Category("sensitive_info"), CategorySensitiveInfo)
	assert.Equal(t, Category("best_practice"), CategoryBestPractice)
}

func TestConfidenceConstants(t *testing.T) {
	assert.Equal(t, Confidence("high"), ConfidenceHigh)
	assert.Equal(t, Confidence("medium"), ConfidenceMedium)
	assert.Equal(t, Confidence("low"), ConfidenceLow)
}

func TestSourceConstants(t *testing.T) {
	assert.Equal(t, Source("go_vet"), SourceGoVet)
	assert.Equal(t, Source("staticcheck"), SourceStaticcheck)
	assert.Equal(t, Source("gosec"), SourceGosec)
	assert.Equal(t, Source("custom_rule"), SourceCustomRule)
	assert.Equal(t, Source("diff_pattern"), SourceDiffPattern)
	assert.Equal(t, Source("llm_review"), SourceLLM)
}

func TestFindingCreation(t *testing.T) {
	f := Finding{
		ID:             "f1",
		Severity:       SeverityCritical,
		Category:       CategorySecurity,
		File:           "main.go",
		Line:           10,
		Title:          "test",
		Evidence:       "evidence",
		Recommendation: "fix it",
		Confidence:     ConfidenceHigh,
		Source:         SourceCustomRule,
		RuleID:         "R1",
	}
	assert.Equal(t, "f1", f.ID)
	assert.True(t, f.Severity == SeverityCritical)
	assert.False(t, f.IsDuplicate)
	assert.False(t, f.Sanitized)
}

func TestChangedFileInfo(t *testing.T) {
	info := ChangedFileInfo{
		File: "main.go", Status: "modified",
		Additions: 5, Deletions: 3, Package: "main",
	}
	assert.Equal(t, "main.go", info.File)
	assert.Equal(t, "modified", info.Status)
	assert.Equal(t, 5, info.Additions)
	assert.True(t, info.Package == "main")
}

func TestReviewTaskDefaults(t *testing.T) {
	task := ReviewTask{
		ID: "task-1", DiffSource: "test",
		Status: "pending",
	}
	assert.Equal(t, "pending", task.Status)
	assert.Equal(t, 0, task.FindingCount)
	assert.False(t, task.DryRun)
	assert.True(t, task.CreatedAt.IsZero())
}

func TestSandboxRunDefaults(t *testing.T) {
	r := SandboxRun{
		ID: "run-1", TaskID: "task-1",
		Backend: "local", Command: "go vet",
	}
	assert.Equal(t, 0, r.ExitCode) // Go zero value
	assert.False(t, r.Timeout)
}

func TestPermissionDecision(t *testing.T) {
	pd := PermissionDecision{
		ID: "pd-1", Decision: "deny",
		ToolName: "workspace_exec", Command: "rm -rf /",
		Reason: "dangerous",
	}
	assert.Equal(t, "deny", pd.Decision)
}

func TestReviewReportEmpty(t *testing.T) {
	r := ReviewReport{
		TaskID: "empty", GeneratedAt: time.Now(),
	}
	assert.Empty(t, r.Findings)
	assert.Empty(t, r.Warnings)
	assert.Empty(t, r.Recommendations)
}

func TestRiskSummaryZero(t *testing.T) {
	rs := RiskSummary{}
	assert.Equal(t, 0, rs.Total)
	assert.Nil(t, rs.BySeverity)
}

func TestSandboxSummary(t *testing.T) {
	ss := SandboxSummary{TotalRuns: 3, Succeeded: 2, Failed: 1}
	assert.Equal(t, 3, ss.TotalRuns)
	assert.Equal(t, 2, ss.Succeeded)
}

func TestMonitoringSummary(t *testing.T) {
	ms := MonitoringSummary{
		TotalDurationMs: 1000,
		FindingCount:    5,
		SeverityDist:    map[Severity]int{"high": 3, "critical": 2},
	}
	assert.Equal(t, int64(1000), ms.TotalDurationMs)
	assert.Equal(t, 5, ms.FindingCount)
	assert.Equal(t, 3, ms.SeverityDist["high"])
}
