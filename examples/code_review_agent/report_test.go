//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRenderMarkdownAndMetrics(t *testing.T) {
	now := time.Now().Add(-time.Second)
	report := minimalReport("task-md")
	report.SandboxRuns[0].ErrorType = "execution_error"
	report.Warnings = []Finding{{
		Severity:       severityLow,
		Category:       "style",
		File:           "warn.go",
		Line:           3,
		Title:          "warning title",
		Evidence:       "warning evidence",
		Recommendation: "warning recommendation",
		Confidence:     0.4,
		Source:         "test",
		RuleID:         "test.warning",
	}}
	md := renderMarkdown(report)
	require.Contains(t, md, "Code Review Report")
	require.Contains(t, md, "Governance")
	require.Contains(t, md, "## Warnings")
	require.Contains(t, md, "warning title")
	var b strings.Builder
	renderFindingList(&b, nil)
	require.Contains(t, b.String(), "No items")

	metrics := buildMetrics(now, report.SandboxRuns, report.PermissionSummary, report.Findings, report.Warnings, report.NeedsHumanReview)
	require.Equal(t, 1, metrics.FindingCount)
	require.Equal(t, 1, metrics.ToolCalls)
	require.Equal(t, 1, metrics.ErrorCounts["execution_error"])
}

func TestWriteReportsArtifactMetadataMatchesFiles(t *testing.T) {
	report := minimalReport("task-artifacts")
	jsonPath, mdPath, artifacts, err := writeReports(report, t.TempDir())
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	jsonInfo, err := os.Stat(jsonPath)
	require.NoError(t, err)
	mdInfo, err := os.Stat(mdPath)
	require.NoError(t, err)
	require.Equal(t, jsonInfo.Size(), artifacts[0].SizeBytes)
	require.Equal(t, mdInfo.Size(), artifacts[1].SizeBytes)
	raw, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	var disk ReviewReport
	require.NoError(t, json.Unmarshal(raw, &disk))
	require.Equal(t, artifacts[0].SizeBytes, disk.Artifacts[0].SizeBytes)
	require.Equal(t, artifacts[1].SizeBytes, disk.Artifacts[1].SizeBytes)
}

func TestWriteReportsRedactsSecrets(t *testing.T) {
	report := minimalReport("task-redacted-report")
	report.Input = DiffSummary{
		Hash:       "hash",
		Files:      []ChangedFile{{OldPath: "pkg/a.go", NewPath: "pkg/a.go"}},
		AddedLines: []AddedLine{{File: "pkg/a.go", Line: 1, Content: `token := "supersecretpassword123"`}},
		Packages:   []PackageInfo{{Dir: "pkg", Name: "pkg", GoFiles: 1}},
	}
	report.Conclusion = "AKIA1234567890ABCDEF"
	report.SandboxRuns[0].Output = "token := bearer supersecretpassword123"
	jsonPath, mdPath, _, err := writeReports(report, t.TempDir())
	require.NoError(t, err)

	raw, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "supersecretpassword123")
	require.NotContains(t, string(raw), "AKIA1234567890ABCDEF")
	require.NotContains(t, string(raw), "bearer supersecretpassword123")

	md, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	require.NotContains(t, string(md), "supersecretpassword123")
	require.NotContains(t, string(md), "bearer supersecretpassword123")
}
