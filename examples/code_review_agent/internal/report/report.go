//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package report generates review reports in JSON and Markdown formats.
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/analysis"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

// GenerateJSON writes a review report as JSON to path.
func GenerateJSON(report *reviewmodel.ReviewReport, path string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json report: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

// GenerateMarkdown writes a review report as Markdown to path.
func GenerateMarkdown(report *reviewmodel.ReviewReport, path string) error {
	var buf bytes.Buffer
	if err := markdownTmpl.Execute(&buf, report); err != nil {
		return fmt.Errorf("execute markdown template: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

var markdownTmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"escapeMD": escapeMarkdown,
	"mul":      func(a, b float64) float64 { return a * b },
}).Parse(markdownTemplate))

func escapeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "|", "\\|")
	return s
}

// NewReport creates a ReviewReport from findings and metadata.
func NewReport(
	task *reviewmodel.ReviewTask,
	findings []reviewmodel.Finding,
	sandboxRuns []reviewmodel.SandboxRun,
	permissionDecisions []reviewmodel.PermissionDecision,
	totalFiles, totalHunks int,
) *reviewmodel.ReviewReport {
	severityCounts := analysis.SeverityCounts(findings)
	categoryCounts := analysis.CategoryCounts(findings)
	humanReviewItems := analysis.HumanReviewItems(findings)

	var govLines []string
	denyCount := 0
	for _, d := range permissionDecisions {
		if d.Action == "deny" {
			denyCount++
			govLines = append(govLines, fmt.Sprintf("Denied: %s (%s)", d.ToolName, d.Reason))
		}
	}
	governanceSummary := "All commands allowed"
	if len(govLines) > 0 {
		governanceSummary = strings.Join(govLines, "; ")
	}

	var sandboxLines []string
	for _, r := range sandboxRuns {
		status := "success"
		if r.TimedOut {
			status = "timed_out"
		} else if r.ExitCode != 0 {
			status = fmt.Sprintf("exit_code=%d", r.ExitCode)
		} else if r.Error != "" {
			status = "error"
		}
		sandboxLines = append(sandboxLines, fmt.Sprintf("%s: %s (%dms)", r.Command, status, r.DurationMs))
	}
	sandboxSummary := "No sandbox executions"
	if len(sandboxLines) > 0 {
		sandboxSummary = strings.Join(sandboxLines, "; ")
	}

	needHumanReviewCount := len(humanReviewItems)

	var anomalies []string
	for _, r := range sandboxRuns {
		if r.TimedOut {
			anomalies = append(anomalies, "sandbox_timeout")
		}
		if r.Error != "" {
			anomalies = append(anomalies, "sandbox_error:"+r.Error)
		}
		if r.ExitCode != 0 {
			anomalies = append(anomalies, fmt.Sprintf("sandbox_exit_nonzero:%d", r.ExitCode))
		}
	}

	if task != nil {
		task.FindingsTotal = len(findings)
		task.FindingsCritical = severityCounts[reviewmodel.SeverityCritical]
		task.FindingsHigh = severityCounts[reviewmodel.SeverityHigh]
		task.FindingsMedium = severityCounts[reviewmodel.SeverityMedium]
		task.FindingsLow = severityCounts[reviewmodel.SeverityLow]
		task.FindingsWarning = severityCounts[reviewmodel.SeverityWarning]
		task.PermissionDenyCount = denyCount
		task.NeedHumanReviewCount = needHumanReviewCount
	}

	report := &reviewmodel.ReviewReport{
		Task:                *task,
		Findings:            findings,
		SandboxRuns:         sandboxRuns,
		PermissionDecisions: permissionDecisions,
		GeneratedAt:         time.Now(),
		Summary: reviewmodel.ReportSummary{
			TotalFiles:        totalFiles,
			TotalHunks:        totalHunks,
			SeverityCounts:    severityCounts,
			CategoryCounts:    categoryCounts,
			HumanReviewItems:  humanReviewItems,
			GovernanceSummary: governanceSummary,
			SandboxSummary:    sandboxSummary,
			Monitoring: reviewmodel.MonitoringSummary{
				TotalDurationMs:     task.TotalDurationMs,
				SandboxDurationMs:   task.SandboxDurationMs,
				ToolCallCount:       task.ToolCallCount,
				PermissionDenyCount: denyCount,
				AnomalyTypes:        anomalies,
			},
		},
	}
	return report
}

const markdownTemplate = `# Code Review Report

**Task ID:** {{.Task.ID}}
**Generated:** {{.GeneratedAt.Format "2006-01-02 15:04:05"}}
**Status:** {{.Task.Status}}

---

## Summary

| Metric | Value |
|--------|-------|
| Total Files | {{.Summary.TotalFiles}} |
| Total Hunks | {{.Summary.TotalHunks}} |
| Total Findings | {{.Task.FindingsTotal}} |
| Critical | {{.Task.FindingsCritical}} |
| High | {{.Task.FindingsHigh}} |
| Medium | {{.Task.FindingsMedium}} |
| Low | {{.Task.FindingsLow}} |
| Warning | {{.Task.FindingsWarning}} |
| Needs Human Review | {{.Task.NeedHumanReviewCount}} |

## Severity Distribution

{{range $severity, $count := .Summary.SeverityCounts}}
- **{{$severity}}**: {{$count}}
{{end}}

## Monitoring

| Metric | Value |
|--------|-------|
| Total Duration | {{.Summary.Monitoring.TotalDurationMs}}ms |
| Sandbox Duration | {{.Summary.Monitoring.SandboxDurationMs}}ms |
| Tool Calls | {{.Summary.Monitoring.ToolCallCount}} |
| Permission Denies | {{.Summary.Monitoring.PermissionDenyCount}} |

{{if .Summary.Monitoring.AnomalyTypes}}
### Anomalies
{{range .Summary.Monitoring.AnomalyTypes}}
- {{.}}
{{end}}
{{end}}

## Governance Summary

{{.Summary.GovernanceSummary}}

## Sandbox Execution Summary

{{.Summary.SandboxSummary}}

## Human Review Items

{{if .Summary.HumanReviewItems}}
{{range .Summary.HumanReviewItems}}
- {{.}}
{{end}}
{{else}}
No items need human review.
{{end}}

## Findings

{{if .Findings}}
{{range .Findings}}
### {{escapeMD .Title}}

- **Severity:** {{.Severity}}
- **Category:** {{.Category}}
- **File:** {{escapeMD .FilePath}}{{if .Line}} (line {{.Line}}){{end}}
- **Rule:** {{.RuleID}}
- **Confidence:** {{printf "%.0f" (mul .Confidence 100)}}%
{{if .NeedsHumanReview}}- **Needs Human Review:** Yes{{end}}

**Evidence:**
` + "```" + `
{{escapeMD .Evidence}}
` + "```" + `

**Recommendation:**
{{escapeMD .Recommendation}}

---
{{end}}
{{else}}
No findings detected.
{{end}}
`
