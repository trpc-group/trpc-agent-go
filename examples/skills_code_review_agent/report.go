//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReviewReportJSON defines the JSON report structure.
type ReviewReportJSON struct {
	TaskID              string                   `json:"task_id"`
	Status              string                   `json:"status"`
	RepoPath            string                   `json:"repo_path"`
	Summary             ReportSummary            `json:"summary"`
	Findings            []Finding                `json:"findings"`
	SandboxRuns         []SandboxRunInfo         `json:"sandbox_runs"`
	PermissionDecisions []PermissionDecisionInfo `json:"permission_decisions"`
	Metrics             ReviewMetrics            `json:"metrics"`
}

// ReportSummary holds high-level status counts for reporting.
type ReportSummary struct {
	TotalFindings int `json:"total_findings"`
	HighSeverity  int `json:"high_severity"`
	MedSeverity   int `json:"medium_severity"`
	LowSeverity   int `json:"low_severity"`
	Warnings      int `json:"warnings"`
}

// SaveReportJSON writes review result to JSON report file.
func SaveReportJSON(result *ReviewResult, outputPath string) error {
	rep := ReviewReportJSON{
		TaskID:   result.TaskID,
		Status:   result.Status,
		RepoPath: result.RepoPath,
		Summary: ReportSummary{
			TotalFindings: len(result.Findings),
			HighSeverity:  result.Metrics.SeverityCounts["high"],
			MedSeverity:   result.Metrics.SeverityCounts["medium"],
			LowSeverity:   result.Metrics.SeverityCounts["low"],
			Warnings:      result.Metrics.SeverityCounts["warning"],
		},
		Findings:            result.Findings,
		SandboxRuns:         result.SandboxRuns,
		PermissionDecisions: result.PermissionDecisions,
		Metrics:             result.Metrics,
	}

	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json report failed: %w", err)
	}

	return os.WriteFile(outputPath, data, 0644)
}

// SaveReportMarkdown writes review result to human-readable Markdown report.
func SaveReportMarkdown(result *ReviewResult, outputPath string) error {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Automated Code Review Report (%s)\n\n", result.TaskID))
	sb.WriteString(fmt.Sprintf("- **Repository Path**: `%s`\n", result.RepoPath))
	sb.WriteString(fmt.Sprintf("- **Status**: `%s`\n", result.Status))
	sb.WriteString(fmt.Sprintf("- **Total Duration**: %d ms\n", result.DurationMs))
	sb.WriteString(fmt.Sprintf("- **Total Findings**: %d\n\n", len(result.Findings)))

	sb.WriteString("## Severity Distribution\n\n")
	sb.WriteString(fmt.Sprintf("| High | Medium | Low | Warnings |\n"))
	sb.WriteString(fmt.Sprintf("| --- | --- | --- | --- |\n"))
	sb.WriteString(fmt.Sprintf("| %d | %d | %d | %d |\n\n",
		result.Metrics.SeverityCounts["high"],
		result.Metrics.SeverityCounts["medium"],
		result.Metrics.SeverityCounts["low"],
		result.Metrics.SeverityCounts["warning"],
	))

	sb.WriteString("## Findings & Recommendations\n\n")
	if len(result.Findings) == 0 {
		sb.WriteString("✅ **No issues detected. Code looks clean!**\n\n")
	} else {
		for i, f := range result.Findings {
			sb.WriteString(fmt.Sprintf("### %d. [%s] %s\n", i+1, strings.ToUpper(f.Severity), f.Title))
			sb.WriteString(fmt.Sprintf("- **File**: `%s:%d`\n", f.File, f.Line))
			sb.WriteString(fmt.Sprintf("- **Category**: %s (Rule ID: `%s`)\n", f.Category, f.RuleID))
			sb.WriteString(fmt.Sprintf("- **Evidence**: `%s`\n", f.Evidence))
			sb.WriteString(fmt.Sprintf("- **Recommendation**: %s\n\n", f.Recommendation))
		}
	}

	sb.WriteString("## Governance & Sandbox Execution\n\n")
	sb.WriteString(fmt.Sprintf("- **Permission Denials**: %d\n", result.Metrics.PermissionDenials))
	sb.WriteString(fmt.Sprintf("- **Sandbox Duration**: %d ms\n\n", result.Metrics.SandboxDurationMs))

	if len(result.PermissionDecisions) > 0 {
		sb.WriteString("### Permission Decisions\n\n")
		for _, dec := range result.PermissionDecisions {
			sb.WriteString(fmt.Sprintf("- Command: `%s` -> **%s** (%s)\n", dec.Command, dec.Decision, dec.Reason))
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(outputPath, []byte(sb.String()), 0644)
}
