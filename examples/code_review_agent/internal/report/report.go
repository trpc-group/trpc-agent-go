//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package report provides code review report generation for JSON and Markdown formats.
package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

// ReviewReport is the final output of a code review.
type ReviewReport struct {
	TaskID          string                      `json:"task_id"`
	DiffSummary     string                      `json:"diff_summary"`
	Findings        []finding.Finding           `json:"findings"`
	Warnings        []finding.Finding           `json:"warnings"`
	RiskSummary     RiskSummary                 `json:"risk_summary"`
	PermissionLog   []PermissionDecisionSummary `json:"permission_log"`
	SandboxSummary  SandboxSummary              `json:"sandbox_summary"`
	Monitoring      MonitoringSummary           `json:"monitoring"`
	Recommendations []string                    `json:"recommendations"`
	GeneratedAt     time.Time                   `json:"generated_at"`
}

// RiskSummary aggregates risk statistics.
type RiskSummary struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ByCategory map[string]int `json:"by_category"`
	NeedReview int            `json:"need_human_review"`
}

// PermissionDecisionSummary is a summary entry for the permission log.
type PermissionDecisionSummary struct {
	ToolName string `json:"tool_name"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// SandboxSummary aggregates sandbox execution statistics.
type SandboxSummary struct {
	TotalRuns       int   `json:"total_runs"`
	Succeeded       int   `json:"succeeded"`
	Failed          int   `json:"failed"`
	TimedOut        int   `json:"timed_out"`
	TotalDurationMs int64 `json:"total_duration_ms"`
}

// MonitoringSummary contains monitoring metrics for a review task.
type MonitoringSummary struct {
	TotalDurationMs       int64          `json:"total_duration_ms"`
	SandboxDurationMs     int64          `json:"sandbox_duration_ms"`
	ToolCallCount         int            `json:"tool_call_count"`
	PermissionDenied      int            `json:"permission_denied"`
	PermissionAsked       int            `json:"permission_asked"`
	FindingCount          int            `json:"finding_count"`
	WarningCount          int            `json:"warning_count"`
	SeverityDist          map[string]int `json:"severity_distribution"`
	ErrorCount            int            `json:"error_count"`
	ErrorTypeDistribution map[string]int `json:"error_type_distribution,omitempty"`
}

// BuildRiskSummary computes risk summary from findings.
func BuildRiskSummary(findings, warnings []finding.Finding) RiskSummary {
	bySeverity := make(map[string]int)
	byCategory := make(map[string]int)
	needReview := 0

	for _, f := range findings {
		bySeverity[string(f.Severity)]++
		byCategory[string(f.Category)]++
		if f.Confidence == finding.ConfidenceLow {
			needReview++
		}
	}

	return RiskSummary{
		Total:      len(findings) + len(warnings),
		BySeverity: bySeverity,
		ByCategory: byCategory,
		NeedReview: needReview + len(warnings),
	}
}

// BuildMonitoringSummary computes monitoring summary.
func BuildMonitoringSummary(totalDuration, sandboxDuration int64, findings []finding.Finding, warnings []finding.Finding, toolCalls, permDenied, permAsked int) MonitoringSummary {
	sevDist := make(map[string]int)
	for _, f := range findings {
		sevDist[string(f.Severity)]++
	}
	for _, w := range warnings {
		_ = w
		sevDist["warning"]++
	}

	return MonitoringSummary{
		TotalDurationMs:   totalDuration,
		SandboxDurationMs: sandboxDuration,
		ToolCallCount:     toolCalls,
		PermissionDenied:  permDenied,
		PermissionAsked:   permAsked,
		FindingCount:      len(findings),
		WarningCount:      len(warnings),
		SeverityDist:      sevDist,
	}
}

// ToJSON serializes the report to JSON.
func ToJSON(report ReviewReport) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	return string(data), nil
}

// ToMarkdown generates a human-readable Markdown report.
func ToMarkdown(report ReviewReport) string {
	var b strings.Builder

	b.WriteString("# Code Review Report\n\n")
	b.WriteString(fmt.Sprintf("**Task ID**: %s\n", report.TaskID))
	b.WriteString(fmt.Sprintf("**Generated**: %s\n", report.GeneratedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("**Diff**: %s\n\n", report.DiffSummary))

	// Risk summary.
	b.WriteString("## Risk Summary\n\n")
	b.WriteString(fmt.Sprintf("- **Total Findings**: %d\n", report.RiskSummary.Total))
	b.WriteString(fmt.Sprintf("- **High/Critical**: %d\n", report.RiskSummary.BySeverity["critical"]+report.RiskSummary.BySeverity["high"]))
	b.WriteString(fmt.Sprintf("- **Need Human Review**: %d\n\n", report.RiskSummary.NeedReview))

	if len(report.RiskSummary.BySeverity) > 0 {
		b.WriteString("### By Severity\n\n")
		for sev, count := range report.RiskSummary.BySeverity {
			b.WriteString(fmt.Sprintf("- **%s**: %d\n", sev, count))
		}
		b.WriteString("\n")
	}

	// Findings.
	if len(report.Findings) > 0 {
		b.WriteString("## Findings\n\n")
		for _, f := range report.Findings {
			b.WriteString(fmt.Sprintf("### [%s] %s\n", f.Severity, f.Title))
			b.WriteString(fmt.Sprintf("- **File**: `%s`", f.File))
			if f.Line > 0 {
				b.WriteString(fmt.Sprintf(" line %d", f.Line))
			}
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("- **Category**: %s\n", f.Category))
			b.WriteString(fmt.Sprintf("- **Rule**: `%s`\n", f.RuleID))
			b.WriteString(fmt.Sprintf("- **Confidence**: %s\n", f.Confidence))
			if f.Evidence != "" {
				b.WriteString(fmt.Sprintf("- **Evidence**:\n```\n%s\n```\n", f.Evidence))
			}
			b.WriteString(fmt.Sprintf("- **Recommendation**: %s\n\n", f.Recommendation))
		}
	}

	// Warnings.
	if len(report.Warnings) > 0 {
		b.WriteString("## Warnings (Low Confidence)\n\n")
		for _, w := range report.Warnings {
			b.WriteString(fmt.Sprintf("- `%s:%d` [%s] %s\n", w.File, w.Line, w.Category, w.Title))
		}
		b.WriteString("\n")
	}

	// Sandbox summary.
	if report.SandboxSummary.TotalRuns > 0 {
		b.WriteString("## Sandbox Execution\n\n")
		b.WriteString(fmt.Sprintf("- **Total Runs**: %d\n", report.SandboxSummary.TotalRuns))
		b.WriteString(fmt.Sprintf("- **Succeeded**: %d\n", report.SandboxSummary.Succeeded))
		b.WriteString(fmt.Sprintf("- **Failed**: %d\n", report.SandboxSummary.Failed))
		b.WriteString(fmt.Sprintf("- **Timed Out**: %d\n", report.SandboxSummary.TimedOut))
		b.WriteString(fmt.Sprintf("- **Total Duration**: %dms\n\n", report.SandboxSummary.TotalDurationMs))
	}

	// Permission log.
	if len(report.PermissionLog) > 0 {
		b.WriteString("## Governance Interceptions\n\n")
		b.WriteString("| Tool | Decision | Reason |\n")
		b.WriteString("|------|----------|--------|\n")
		for _, p := range report.PermissionLog {
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", p.ToolName, p.Decision, p.Reason))
		}
		b.WriteString("\n")
	}

	// Monitoring.
	b.WriteString("## Monitoring\n\n")
	b.WriteString(fmt.Sprintf("- **Total Duration**: %dms\n", report.Monitoring.TotalDurationMs))
	b.WriteString(fmt.Sprintf("- **Sandbox Duration**: %dms\n", report.Monitoring.SandboxDurationMs))
	b.WriteString(fmt.Sprintf("- **Tool Calls**: %d\n", report.Monitoring.ToolCallCount))
	b.WriteString(fmt.Sprintf("- **Permission Denied**: %d\n", report.Monitoring.PermissionDenied))
	if len(report.Monitoring.SeverityDist) > 0 {
		b.WriteString("- **Severity Distribution**:\n")
		for sev, count := range report.Monitoring.SeverityDist {
			b.WriteString(fmt.Sprintf("  - %s: %d\n", sev, count))
		}
	}
	b.WriteString("\n")

	// Recommendations.
	if len(report.Recommendations) > 0 {
		b.WriteString("## Recommendations\n\n")
		for i, r := range report.Recommendations {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// SortFindings sorts findings by severity (critical first) then by file/line.
func SortFindings(findings []finding.Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return sevScore(findings[i].Severity) < sevScore(findings[j].Severity)
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
}

func sevScore(s finding.Severity) int {
	switch s {
	case finding.SeverityCritical:
		return 0
	case finding.SeverityHigh:
		return 1
	case finding.SeverityMedium:
		return 2
	case finding.SeverityLow:
		return 3
	default:
		return 4
	}
}
