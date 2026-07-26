// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package report 提供报告生成功能
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// ReviewReport 审查报告
type ReviewReport struct {
	TaskID              string                     `json:"task_id"`
	Timestamp           time.Time                  `json:"timestamp"`
	Summary             ReportSummary              `json:"summary"`
	Findings            []store.Finding            `json:"findings"`
	Warnings            []store.Finding            `json:"warnings"`
	Sandbox             []store.SandboxRun         `json:"sandbox_runs"`
	PermissionDecisions []store.PermissionDecision `json:"permission_decisions"`
	Monitoring          *store.MonitoringSummary   `json:"monitoring,omitempty"`
}

// ReportSummary 报告摘要
type ReportSummary struct {
	TotalFindings int    `json:"total_findings"`
	CriticalCount int    `json:"critical_count"`
	HighCount     int    `json:"high_count"`
	MediumCount   int    `json:"medium_count"`
	LowCount      int    `json:"low_count"`
	InfoCount     int    `json:"info_count"`
	OverallRisk   string `json:"overall_risk"`
	DurationMs    int    `json:"duration_ms"`
}

// Generate 生成报告
func Generate(taskID string, findings []store.Finding, warnings []store.Finding, sandboxRuns []store.SandboxRun, monitoring *store.MonitoringSummary) *ReviewReport {
	critical, high, medium, low, info := countBySeverity(findings)
	overallRisk := overallRiskFromCounts(critical, high, medium, low)

	summary := ReportSummary{
		TotalFindings: len(findings),
		CriticalCount: critical,
		HighCount:     high,
		MediumCount:   medium,
		LowCount:      low,
		InfoCount:     info,
		OverallRisk:   overallRisk,
	}

	if monitoring != nil {
		summary.DurationMs = monitoring.TotalDurationMs
	}

	return &ReviewReport{
		TaskID:              taskID,
		Timestamp:           time.Now(),
		Summary:             summary,
		Findings:            findings,
		Warnings:            warnings,
		Sandbox:             sandboxRuns,
		PermissionDecisions: make([]store.PermissionDecision, 0),
		Monitoring:          monitoring,
	}
}

func countBySeverity(findings []store.Finding) (critical, high, medium, low, info int) {
	for _, f := range findings {
		switch f.Severity {
		case "critical":
			critical++
		case "high":
			high++
		case "medium":
			medium++
		case "low":
			low++
		case "info":
			info++
		}
	}
	return
}

func overallRiskFromCounts(critical, high, medium, low int) string {
	switch {
	case critical > 0:
		return "critical"
	case high > 0:
		return "high"
	case medium > 0:
		return "medium"
	case low > 0:
		return "low"
	default:
		return "safe"
	}
}

// PrintJSON 输出 JSON 格式
func PrintJSON(report *ReviewReport) {
	data, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(data))
}

// PrintMarkdown 输出 Markdown 格式
func PrintMarkdown(report *ReviewReport) {
	fmt.Printf("# GoLens Code Review Report\n\n")
	fmt.Printf("**Task ID:** %s\n", report.TaskID)
	fmt.Printf("**Timestamp:** %s\n\n", report.Timestamp.Format(time.RFC3339))

	fmt.Printf("## Summary\n\n")
	fmt.Printf("- **Total Findings:** %d\n", report.Summary.TotalFindings)
	fmt.Printf("- **Critical:** %d\n", report.Summary.CriticalCount)
	fmt.Printf("- **High:** %d\n", report.Summary.HighCount)
	fmt.Printf("- **Medium:** %d\n", report.Summary.MediumCount)
	fmt.Printf("- **Low:** %d\n", report.Summary.LowCount)
	fmt.Printf("- **Overall Risk:** %s\n", report.Summary.OverallRisk)
	fmt.Printf("- **Duration:** %dms\n\n", report.Summary.DurationMs)

	if len(report.Findings) > 0 {
		fmt.Printf("## Findings\n\n")
		for i, f := range report.Findings {
			fmt.Printf("### %d. %s\n\n", i+1, f.Title)
			fmt.Printf("- **Severity:** %s\n", f.Severity)
			fmt.Printf("- **Category:** %s\n", f.Category)
			fmt.Printf("- **File:** %s:%d\n", f.File, f.Line)
			fmt.Printf("- **Confidence:** %.0f%%\n", f.Confidence*100)
			fmt.Printf("- **Rule ID:** %s\n\n", f.RuleID)
			if f.Evidence != "" {
				fmt.Printf("**Evidence:**\n```\n%s\n```\n\n", f.Evidence)
			}
			if f.Recommendation != "" {
				fmt.Printf("**Recommendation:** %s\n\n", f.Recommendation)
			}
		}
	}

	if len(report.Warnings) > 0 {
		fmt.Printf("## Warnings\n\n")
		for _, w := range report.Warnings {
			fmt.Printf("- %s (%s:%d)\n", w.Title, w.File, w.Line)
		}
		fmt.Println()
	}

	if len(report.Sandbox) > 0 {
		fmt.Printf("## Sandbox Runs\n\n")
		for _, s := range report.Sandbox {
			status := "✅"
			if s.ExitCode != 0 {
				status = "❌"
			}
			fmt.Printf("- %s %s (exit: %d, %dms)\n", status, s.ScriptName, s.ExitCode, s.DurationMs)
		}
		fmt.Println()
	}

	// 治理拦截摘要
	if len(report.PermissionDecisions) > 0 {
		fmt.Printf("## Permission Decisions\n\n")
		for _, p := range report.PermissionDecisions {
			fmt.Printf("- **%s**: %s (%s)\n", p.Command, p.Decision, p.Reason)
		}
		fmt.Println()
	}

	// 监控指标
	if report.Monitoring != nil {
		fmt.Printf("## Monitoring\n\n")
		fmt.Printf("- **Total Duration:** %dms\n", report.Monitoring.TotalDurationMs)
		fmt.Printf("- **Sandbox Duration:** %dms\n", report.Monitoring.SandboxDurationMs)
		fmt.Printf("- **Tool Calls:** %d\n", report.Monitoring.ToolCallsCount)
		fmt.Printf("- **Permission Blocks:** %d\n", report.Monitoring.PermissionBlocksCount)
		fmt.Printf("- **Findings Count:** %d\n", report.Monitoring.FindingsCount)

		// 按固定顺序输出 severity 分布
		if len(report.Monitoring.SeverityDistribution) > 0 {
			fmt.Printf("\n**Severity Distribution:**\n")
			severityOrder := []string{"critical", "high", "medium", "low", "info"}
			for _, severity := range severityOrder {
				if count, ok := report.Monitoring.SeverityDistribution[severity]; ok && count > 0 {
					fmt.Printf("- %s: %d\n", severity, count)
				}
			}
		}
		fmt.Println()
	}
}

// PrintText 输出纯文本格式
func PrintText(report *ReviewReport) {
	fmt.Printf("Task: %s\n", report.TaskID)
	fmt.Printf("Findings: %d\n", report.Summary.TotalFindings)
	fmt.Printf("Risk: %s\n", report.Summary.OverallRisk)
	fmt.Printf("Duration: %dms\n\n", report.Summary.DurationMs)

	for _, f := range report.Findings {
		fmt.Printf("[%s] %s (%s:%d)\n", f.Severity, f.Title, f.File, f.Line)
	}
}

// SaveJSON 保存 JSON 报告
func SaveJSON(report *ReviewReport, filename string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// SaveMarkdown 保存 Markdown 报告
func SaveMarkdown(report *ReviewReport, filename string) error {
	content := GenerateMarkdownString(report)
	return os.WriteFile(filename, []byte(content), 0644)
}

// GenerateMarkdownString 生成 Markdown 字符串
func GenerateMarkdownString(report *ReviewReport) string {
	var sb strings.Builder

	sb.WriteString("# GoLens Code Review Report\n\n")
	sb.WriteString(fmt.Sprintf("**Task ID:** %s\n", report.TaskID))
	sb.WriteString(fmt.Sprintf("**Timestamp:** %s\n\n", report.Timestamp.Format(time.RFC3339)))

	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("- **Total Findings:** %d\n", report.Summary.TotalFindings))
	sb.WriteString(fmt.Sprintf("- **Critical:** %d\n", report.Summary.CriticalCount))
	sb.WriteString(fmt.Sprintf("- **High:** %d\n", report.Summary.HighCount))
	sb.WriteString(fmt.Sprintf("- **Medium:** %d\n", report.Summary.MediumCount))
	sb.WriteString(fmt.Sprintf("- **Low:** %d\n", report.Summary.LowCount))
	sb.WriteString(fmt.Sprintf("- **Overall Risk:** %s\n", report.Summary.OverallRisk))
	sb.WriteString(fmt.Sprintf("- **Duration:** %dms\n\n", report.Summary.DurationMs))

	if len(report.Findings) > 0 {
		sb.WriteString("## Findings\n\n")
		for i, f := range report.Findings {
			sb.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, f.Title))
			sb.WriteString(fmt.Sprintf("- **Severity:** %s\n", f.Severity))
			sb.WriteString(fmt.Sprintf("- **Category:** %s\n", f.Category))
			sb.WriteString(fmt.Sprintf("- **File:** %s:%d\n", f.File, f.Line))
			sb.WriteString(fmt.Sprintf("- **Confidence:** %.0f%%\n", f.Confidence*100))
			sb.WriteString(fmt.Sprintf("- **Rule ID:** %s\n\n", f.RuleID))
			if f.Evidence != "" {
				sb.WriteString(fmt.Sprintf("**Evidence:**\n```\n%s\n```\n\n", f.Evidence))
			}
			if f.Recommendation != "" {
				sb.WriteString(fmt.Sprintf("**Recommendation:** %s\n\n", f.Recommendation))
			}
		}
	}

	return sb.String()
}
