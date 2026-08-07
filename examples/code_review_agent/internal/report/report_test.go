//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package report

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

func TestBuildRiskSummary(t *testing.T) {
	findings := []finding.Finding{
		{Severity: finding.SeverityCritical, Category: finding.CategorySecurity, Confidence: finding.ConfidenceHigh},
		{Severity: finding.SeverityHigh, Category: finding.CategoryGoroutineLeak, Confidence: finding.ConfidenceMedium},
	}
	warnings := []finding.Finding{
		{Severity: finding.SeverityLow, Category: finding.CategoryBestPractice, Confidence: finding.ConfidenceLow},
	}
	rs := BuildRiskSummary(findings, warnings)
	assert.Equal(t, 3, rs.Total)
	assert.Equal(t, 1, rs.BySeverity["critical"])
	assert.Equal(t, 1, rs.BySeverity["high"])
	assert.Equal(t, 1, rs.NeedReview) // 1 low-confidence
}

func TestBuildMonitoringSummary(t *testing.T) {
	findings := []finding.Finding{
		{Severity: finding.SeverityCritical},
		{Severity: finding.SeverityHigh},
		{Severity: finding.SeverityHigh},
	}
	ms := BuildMonitoringSummary(1000, 500, findings, nil, 10, 2, 1)
	assert.Equal(t, int64(1000), ms.TotalDurationMs)
	assert.Equal(t, 10, ms.ToolCallCount)
	assert.Equal(t, 2, ms.PermissionDenied)
	assert.Equal(t, 3, ms.FindingCount)
	assert.Equal(t, 1, ms.SeverityDist["critical"])
	assert.Equal(t, 2, ms.SeverityDist["high"])
}

func TestToJSON(t *testing.T) {
	report := ReviewReport{
		TaskID:      "task-1",
		DiffSummary: "3 files changed",
		GeneratedAt: time.Now(),
		RiskSummary: RiskSummary{Total: 5},
	}
	json, err := ToJSON(report)
	assert.NoError(t, err)
	assert.Contains(t, json, "task-1")
	assert.Contains(t, json, "risk_summary")
}

func TestToMarkdown(t *testing.T) {
	report := ReviewReport{
		TaskID:      "task-1",
		DiffSummary: "3 files changed",
		GeneratedAt: time.Now(),
		Findings: []finding.Finding{
			{File: "main.go", Line: 10, Severity: finding.SeverityCritical, Category: finding.CategorySecurity,
				Title: "SQL injection", RuleID: "SEC001", Evidence: "db.Query(\"SELECT * FROM users WHERE id=\" + id)",
				Recommendation: "Use parameterized queries", Confidence: finding.ConfidenceHigh},
		},
		Warnings: []finding.Finding{
			{File: "helper.go", Line: 5, Severity: finding.SeverityLow, Category: finding.CategoryBestPractice,
				Title: "Style issue", Confidence: finding.ConfidenceLow},
		},
		RiskSummary:    RiskSummary{Total: 2, BySeverity: map[string]int{"critical": 1}},
		SandboxSummary: SandboxSummary{TotalRuns: 3, Succeeded: 2, Failed: 1, TotalDurationMs: 500},
		PermissionLog: []PermissionDecisionSummary{
			{ToolName: "workspace_exec", Decision: "deny", Reason: "rm is denied"},
		},
		Monitoring: MonitoringSummary{
			TotalDurationMs: 1000, SandboxDurationMs: 500, ToolCallCount: 5,
			PermissionDenied: 1, SeverityDist: map[string]int{"critical": 1},
		},
		Recommendations: []string{"Use parameterized queries", "Add tests"},
	}
	md := ToMarkdown(report)
	assert.Contains(t, md, "# Code Review Report")
	assert.Contains(t, md, "task-1")
	assert.Contains(t, md, "SQL injection")
	assert.Contains(t, md, "critical")
	assert.Contains(t, md, "Governance Interceptions")
	assert.Contains(t, md, "deny")
	assert.Contains(t, md, "Sandbox Execution")
	assert.Contains(t, md, "Recommendations")
	assert.Contains(t, md, "Use parameterized queries")
}

func TestToMarkdown_Empty(t *testing.T) {
	report := ReviewReport{
		TaskID:      "empty",
		GeneratedAt: time.Now(),
		RiskSummary: RiskSummary{Total: 0},
	}
	md := ToMarkdown(report)
	assert.Contains(t, md, "# Code Review Report")
	assert.NotContains(t, md, "## Findings")
}

func TestSortFindings(t *testing.T) {
	findings := []finding.Finding{
		{Severity: finding.SeverityLow, File: "b.go", Line: 1},
		{Severity: finding.SeverityCritical, File: "a.go", Line: 10},
		{Severity: finding.SeverityHigh, File: "a.go", Line: 5},
	}
	SortFindings(findings)
	assert.Equal(t, finding.SeverityCritical, findings[0].Severity)
	assert.Equal(t, finding.SeverityHigh, findings[1].Severity)
	assert.Equal(t, finding.SeverityLow, findings[2].Severity)
}

func TestSevScore(t *testing.T) {
	assert.Equal(t, 0, sevScore(finding.SeverityCritical))
	assert.Equal(t, 1, sevScore(finding.SeverityHigh))
	assert.Equal(t, 2, sevScore(finding.SeverityMedium))
	assert.Equal(t, 3, sevScore(finding.SeverityLow))
	assert.Equal(t, 4, sevScore(finding.SeverityWarning))
	assert.Equal(t, 4, sevScore(finding.SeverityInfo))
}

func TestBuildMonitoringSummary_Warnings(t *testing.T) {
	findings := []finding.Finding{
		{Severity: finding.SeverityCritical},
	}
	warnings := []finding.Finding{
		{File: "x.go", Line: 1, Severity: finding.SeverityWarning},
	}
	ms := BuildMonitoringSummary(0, 0, findings, warnings, 0, 0, 0)
	assert.Equal(t, 1, ms.FindingCount)
	assert.Equal(t, 1, ms.WarningCount)
}
