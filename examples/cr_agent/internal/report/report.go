//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package report serializes the review output into JSON and Markdown
// formats for human and machine consumption.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// SaveJSON writes a ReviewReport to a JSON file at the given path.
func SaveJSON(path string, report *types.ReviewReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write report file: %w", err)
	}
	return nil
}

// SaveMarkdown writes a ReviewReport to a Markdown file at the given
// path.
func SaveMarkdown(path string, report *types.ReviewReport) error {
	md := GenerateMarkdown(report)
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return fmt.Errorf("write markdown report: %w", err)
	}
	return nil
}

// GenerateMarkdown produces a human-readable Markdown report from a
// ReviewReport.
func GenerateMarkdown(report *types.ReviewReport) string {
	var b strings.Builder

	b.WriteString("# Code Review Report\n\n")
	b.WriteString(fmt.Sprintf("- **Task ID**: %s\n", report.TaskID))
	b.WriteString(fmt.Sprintf("- **Generated At**: %s\n",
		report.GeneratedAt.Format(time.RFC3339)))
	b.WriteString("\n")

	// Summary table.
	b.WriteString("## Summary\n\n")
	b.WriteString("| Severity | Count |\n")
	b.WriteString("|----------|-------|\n")
	b.WriteString(fmt.Sprintf("| Critical | %d |\n", report.Summary.Critical))
	b.WriteString(fmt.Sprintf("| High     | %d |\n", report.Summary.High))
	b.WriteString(fmt.Sprintf("| Medium   | %d |\n", report.Summary.Medium))
	b.WriteString(fmt.Sprintf("| Low      | %d |\n", report.Summary.Low))
	b.WriteString(fmt.Sprintf("| Warning  | %d |\n", report.Summary.Warning))
	b.WriteString(fmt.Sprintf("\n**Total files reviewed**: %d\n",
		report.Summary.TotalFiles))
	b.WriteString(fmt.Sprintf("**Findings needing human review**: %d\n\n",
		report.Summary.NeedsHumanReview))

	// Findings grouped by severity.
	b.WriteString("## Findings\n\n")
	sorted := sortedFindings(report.Findings)
	groups := groupBySeverity(sorted)
	for _, sev := range []types.Severity{
		types.SeverityCritical, types.SeverityHigh,
		types.SeverityMedium, types.SeverityLow,
		types.SeverityWarning,
	} {
		fs := groups[sev]
		if len(fs) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s (%d)\n\n",
			severityTitle(sev), len(fs)))
		for _, f := range fs {
			b.WriteString(formatFindingMD(f))
		}
	}

	// Warnings.
	if len(report.Warnings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, w := range report.Warnings {
			b.WriteString(fmt.Sprintf("- %s\n", w))
		}
		b.WriteString("\n")
	}

	// Metrics.
	b.WriteString("## Metrics\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	b.WriteString(fmt.Sprintf("| Total duration | %d ms |\n",
		report.Metrics.TotalDurationMs))
	b.WriteString(fmt.Sprintf("| Sandbox duration | %d ms |\n",
		report.Metrics.SandboxDurationMs))
	b.WriteString(fmt.Sprintf("| Parse duration | %d ms |\n",
		report.Metrics.ParseDurationMs))
	b.WriteString(fmt.Sprintf("| Review duration | %d ms |\n",
		report.Metrics.ReviewDurationMs))
	b.WriteString(fmt.Sprintf("| Report duration | %d ms |\n",
		report.Metrics.ReportDurationMs))
	b.WriteString(fmt.Sprintf("| Tool calls | %d |\n",
		report.Metrics.ToolCalls))
	b.WriteString(fmt.Sprintf("| Sandbox runs | %d |\n",
		report.Metrics.SandboxRuns))
	b.WriteString(fmt.Sprintf("| Permission denials | %d |\n",
		report.Metrics.PermissionDenials))
	b.WriteString(fmt.Sprintf("| Rules evaluated | %d |\n",
		report.Metrics.RulesEvaluated))

	return b.String()
}

func formatFindingMD(f types.Finding) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("#### [%s] %s — `%s:%d`\n\n",
		f.RuleID, f.Title, f.File, f.Line))
	b.WriteString(fmt.Sprintf("- **Severity**: %s\n", f.Severity))
	b.WriteString(fmt.Sprintf("- **Category**: %s\n", f.Category))
	b.WriteString(fmt.Sprintf("- **Confidence**: %.2f\n", f.Confidence))
	b.WriteString(fmt.Sprintf("- **Source**: %s\n", f.Source))
	if f.NeedsHumanReview {
		b.WriteString("- **Needs Human Review**: yes\n")
	}
	b.WriteString("\n")
	b.WriteString("**Evidence**:\n```go\n")
	b.WriteString(f.Evidence)
	b.WriteString("\n```\n\n")
	b.WriteString(fmt.Sprintf("**Recommendation**: %s\n\n", f.Recommendation))
	return b.String()
}

func severityTitle(s types.Severity) string {
	switch s {
	case types.SeverityCritical:
		return "Critical"
	case types.SeverityHigh:
		return "High"
	case types.SeverityMedium:
		return "Medium"
	case types.SeverityLow:
		return "Low"
	case types.SeverityWarning:
		return "Warning"
	}
	return string(s)
}

func sortedFindings(findings []types.Finding) []types.Finding {
	out := make([]types.Finding, len(findings))
	copy(out, findings)
	sort.SliceStable(out, func(i, j int) bool {
		return severityRank(out[i].Severity) > severityRank(out[j].Severity)
	})
	return out
}

func severityRank(s types.Severity) int {
	switch s {
	case types.SeverityCritical:
		return 5
	case types.SeverityHigh:
		return 4
	case types.SeverityMedium:
		return 3
	case types.SeverityLow:
		return 2
	case types.SeverityWarning:
		return 1
	}
	return 0
}

func groupBySeverity(findings []types.Finding) map[types.Severity][]types.Finding {
	m := make(map[types.Severity][]types.Finding)
	for _, f := range findings {
		m[f.Severity] = append(m[f.Severity], f)
	}
	return m
}

// FillSummary populates the Summary field of a ReviewReport from
// the report's own Findings list. It is used by the pipeline to
// compute the roll-up counts after deduplication.
func FillSummary(report *types.ReviewReport, totalFiles int) {
	report.Summary.TotalFiles = totalFiles
	for _, f := range report.Findings {
		switch f.Severity {
		case types.SeverityCritical:
			report.Summary.Critical++
		case types.SeverityHigh:
			report.Summary.High++
		case types.SeverityMedium:
			report.Summary.Medium++
		case types.SeverityLow:
			report.Summary.Low++
		case types.SeverityWarning:
			report.Summary.Warning++
		}
		if f.NeedsHumanReview {
			report.Summary.NeedsHumanReview++
		}
	}
}
