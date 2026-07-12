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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateJSONReport(t *testing.T) {
	data := &ReportData{
		TaskID: "test-1",
		Findings: []Finding{
			{Severity: SeverityCritical, File: "a.go", Line: 10,
				Title: "SQL injection", RuleID: "SQL_INJECTION"},
		},
		Warnings: []Warning{},
		SeverityCounts: map[string]int{
			SeverityCritical: 1, SeverityHigh: 0,
			SeverityMedium: 0, SeverityLow: 0,
		},
		Recommendations: []string{"Fix critical issues."},
	}

	jsonStr, err := GenerateJSONReport(data)
	require.NoError(t, err)
	require.Contains(t, jsonStr, "test-1")
	require.Contains(t, jsonStr, "SQL_INJECTION")

	// Verify it's valid JSON.
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(jsonStr), &parsed))
	require.Equal(t, "test-1", parsed["task_id"])
}

func TestGenerateMarkdownReport(t *testing.T) {
	data := &ReportData{
		TaskID:      "test-1",
		DiffSummary: "1 files, +5 -0",
		Findings: []Finding{
			{Severity: SeverityCritical, File: "a.go", Line: 10,
				Title: "SQL injection", RuleID: "SQL_INJECTION",
				Evidence: "SELECT * FROM", Recommendation: "Use parameterized queries"},
		},
		SeverityCounts: map[string]int{
			SeverityCritical: 1, SeverityHigh: 0,
			SeverityMedium: 0, SeverityLow: 0,
		},
		Monitoring: &MonitoringSummary{
			TotalDurationMs: 100,
			ToolCallCount:   3,
		},
		Recommendations: []string{"Fix critical issues."},
	}

	md := GenerateMarkdownReport(data)
	require.Contains(t, md, "# Code Review Report")
	require.Contains(t, md, "test-1")
	require.Contains(t, md, "SQL injection")
	require.Contains(t, md, "## Monitoring Metrics")
}

func TestGenerateMarkdownReport_NoFindings(t *testing.T) {
	data := &ReportData{
		TaskID:   "test-2",
		Findings: []Finding{},
		SeverityCounts: map[string]int{
			SeverityCritical: 0, SeverityHigh: 0,
			SeverityMedium: 0, SeverityLow: 0,
		},
	}

	md := GenerateMarkdownReport(data)
	require.Contains(t, md, "No findings")
}

func TestBuildRecommendations(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical},
		{Severity: SeverityHigh},
	}
	recs := BuildRecommendations(findings)
	require.Len(t, recs, 2)
	require.Contains(t, recs[0], "critical")
}

func TestBuildRecommendations_Empty(t *testing.T) {
	recs := BuildRecommendations(nil)
	require.Len(t, recs, 1)
	require.Contains(t, recs[0], "ready for review")
}
