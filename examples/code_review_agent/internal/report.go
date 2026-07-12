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
	"fmt"
	"strings"
	"time"
)

// ReportData holds the structured data for a review report.
type ReportData struct {
	TaskID            string             `json:"task_id"`
	GeneratedAt       string             `json:"generated_at"`
	DiffSummary       string             `json:"diff_summary"`
	Findings          []Finding          `json:"findings"`
	Warnings          []Warning          `json:"warnings"`
	SeverityCounts    map[string]int     `json:"severity_counts"`
	PermissionSummary []PermissionRecord `json:"permission_summary"`
	SandboxRuns       []SandboxRun       `json:"sandbox_runs"`
	Monitoring        *MonitoringSummary `json:"monitoring"`
	Recommendations   []string           `json:"recommendations"`
}

// GenerateJSONReport generates a JSON-formatted review report.
func GenerateJSONReport(data *ReportData) (string, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	return string(b), nil
}

// GenerateMarkdownReport generates a Markdown-formatted review report.
func GenerateMarkdownReport(data *ReportData) string {
	var sb strings.Builder

	sb.WriteString("# Code Review Report\n\n")
	sb.WriteString(fmt.Sprintf("**Task ID:** %s\n\n", data.TaskID))
	sb.WriteString(fmt.Sprintf("**Generated:** %s\n\n", data.GeneratedAt))
	sb.WriteString(fmt.Sprintf("**Diff Summary:** %s\n\n", data.DiffSummary))

	// Summary
	sb.WriteString("## Summary\n\n")
	total := len(data.Findings)
	sb.WriteString(fmt.Sprintf("- **Total findings:** %d\n", total))
	sb.WriteString(fmt.Sprintf("- **Critical:** %d\n", data.SeverityCounts[SeverityCritical]))
	sb.WriteString(fmt.Sprintf("- **High:** %d\n", data.SeverityCounts[SeverityHigh]))
	sb.WriteString(fmt.Sprintf("- **Medium:** %d\n", data.SeverityCounts[SeverityMedium]))
	sb.WriteString(fmt.Sprintf("- **Low:** %d\n", data.SeverityCounts[SeverityLow]))
	sb.WriteString(fmt.Sprintf("- **Warnings (needs review):** %d\n", len(data.Warnings)))
	sb.WriteString("\n")

	// Findings
	if len(data.Findings) > 0 {
		sb.WriteString("## Findings\n\n")
		for _, f := range data.Findings {
			sb.WriteString(fmt.Sprintf("### [%s] %s\n\n", strings.ToUpper(f.Severity), f.Title))
			sb.WriteString(fmt.Sprintf("- **File:** %s:%d\n", f.File, f.Line))
			sb.WriteString(fmt.Sprintf("- **Category:** %s\n", f.Category))
			sb.WriteString(fmt.Sprintf("- **Rule ID:** %s\n", f.RuleID))
			sb.WriteString(fmt.Sprintf("- **Source:** %s\n", f.Source))
			sb.WriteString(fmt.Sprintf("- **Confidence:** %.0f%%\n", f.Confidence*100))
			sb.WriteString(fmt.Sprintf("- **Evidence:**\n```\n%s\n```\n", f.Evidence))
			sb.WriteString(fmt.Sprintf("- **Recommendation:** %s\n\n", f.Recommendation))
		}
	} else {
		sb.WriteString("## Findings\n\nNo findings. The code looks clean.\n\n")
	}

	// Warnings (low confidence)
	if len(data.Warnings) > 0 {
		sb.WriteString("## Items Needing Human Review\n\n")
		sb.WriteString("The following items have low confidence and may be false positives:\n\n")
		for _, w := range data.Warnings {
			sb.WriteString(fmt.Sprintf("- **%s** (%s:%d) — %s\n", w.Title, w.File, w.Line, w.Recommendation))
		}
		sb.WriteString("\n")
	}

	// Permission decisions
	if len(data.PermissionSummary) > 0 {
		sb.WriteString("## Permission Decisions\n\n")
		sb.WriteString("| Command | Decision | Reason |\n")
		sb.WriteString("|---------|----------|--------|\n")
		for _, p := range data.PermissionSummary {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", p.Command, p.Decision, p.Reason))
		}
		sb.WriteString("\n")
	}

	// Sandbox execution summary
	if len(data.SandboxRuns) > 0 {
		sb.WriteString("## Sandbox Execution Summary\n\n")
		sb.WriteString("| Command | Status | Exit Code | Duration |\n")
		sb.WriteString("|---------|--------|-----------|----------|\n")
		for _, r := range data.SandboxRuns {
			sb.WriteString(fmt.Sprintf("| %s | %s | %d | %dms |\n",
				r.Command, r.Status, r.ExitCode, r.Duration.Milliseconds()))
		}
		sb.WriteString("\n")
	}

	// Monitoring metrics
	if data.Monitoring != nil {
		sb.WriteString("## Monitoring Metrics\n\n")
		sb.WriteString(fmt.Sprintf("- **Total duration:** %dms\n", data.Monitoring.TotalDurationMs))
		sb.WriteString(fmt.Sprintf("- **Sandbox duration:** %dms\n", data.Monitoring.SandboxDurationMs))
		sb.WriteString(fmt.Sprintf("- **Tool calls:** %d\n", data.Monitoring.ToolCallCount))
		sb.WriteString(fmt.Sprintf("- **Permission blocks:** %d\n", data.Monitoring.PermissionBlockCount))
		if len(data.Monitoring.ErrorTypes) > 0 {
			sb.WriteString("- **Error types:**\n")
			for et, c := range data.Monitoring.ErrorTypes {
				sb.WriteString(fmt.Sprintf("  - %s: %d\n", et, c))
			}
		}
		sb.WriteString("\n")
	}

	// Executable recommendations
	if len(data.Recommendations) > 0 {
		sb.WriteString("## Recommendations\n\n")
		for i, r := range data.Recommendations {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// BuildRecommendations generates actionable recommendations from
// the findings.
func BuildRecommendations(findings []Finding) []string {
	var recs []string
	critical := 0
	high := 0
	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical:
			critical++
		case SeverityHigh:
			high++
		}
	}
	if critical > 0 {
		recs = append(recs, fmt.Sprintf(
			"Fix %d critical issue(s) before merging — these are likely "+
				"security vulnerabilities or crash risks.", critical))
	}
	if high > 0 {
		recs = append(recs, fmt.Sprintf(
			"Address %d high-severity issue(s) — these may cause resource "+
				"leaks or data loss in production.", high))
	}
	if len(findings) == 0 {
		recs = append(recs, "No issues found. Code is ready for review.")
	}
	return recs
}

// NewReportData creates a ReportData from the review results.
func NewReportData(
	taskID, diffSummary string,
	findings []Finding,
	warnings []Warning,
	permissionRecords []PermissionRecord,
	sandboxRuns []SandboxRun,
	monitoring *MonitoringSummary,
) *ReportData {
	return &ReportData{
		TaskID:            taskID,
		GeneratedAt:       time.Now().Format(time.RFC3339),
		DiffSummary:       diffSummary,
		Findings:          findings,
		Warnings:          warnings,
		SeverityCounts:    CountBySeverity(findings),
		PermissionSummary: permissionRecords,
		SandboxRuns:       sandboxRuns,
		Monitoring:        monitoring,
		Recommendations:   BuildRecommendations(findings),
	}
}
