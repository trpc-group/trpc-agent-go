//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package report renders review results to JSON and Markdown.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
)

// WriteJSON writes the review result as JSON.
func WriteJSON(path string, result *findings.ReviewResult) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// WriteMarkdown writes a human-readable review report.
func WriteMarkdown(path string, result *findings.ReviewResult) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	var b strings.Builder
	b.WriteString("# Code Review Report\n\n")
	b.WriteString(fmt.Sprintf("**Task ID:** %s\n\n", result.TaskID))
	b.WriteString(fmt.Sprintf("**Status:** %s\n\n", result.Status))
	b.WriteString(fmt.Sprintf("**Input:** %s\n\n", result.InputSummary))

	b.WriteString("## Summary\n\n")
	b.WriteString("| Severity | Count |\n")
	b.WriteString("|----------|-------|\n")
	severities := severityOrder(result.Metrics.SeverityCounts)
	for _, sev := range severities {
		b.WriteString(fmt.Sprintf("| %s | %d |\n", sev, result.Metrics.SeverityCounts[sev]))
	}
	b.WriteString(fmt.Sprintf("\n**Confirmed findings:** %d\n\n", result.Metrics.FindingCount))
	b.WriteString(fmt.Sprintf("**Needs human review:** %d\n\n", result.Metrics.WarningCount))

	b.WriteString("## Findings\n\n")
	if len(result.Findings) == 0 {
		b.WriteString("No confirmed findings.\n\n")
	} else {
		for i, f := range result.Findings {
			writeFinding(&b, i+1, f)
		}
	}

	b.WriteString("## Needs Human Review\n\n")
	if len(result.Warnings) == 0 {
		b.WriteString("No low-confidence warnings.\n\n")
	} else {
		for i, f := range result.Warnings {
			writeFinding(&b, i+1, f)
		}
	}

	b.WriteString("## Monitoring\n\n")
	b.WriteString(fmt.Sprintf("- Total duration: %d ms\n", result.Metrics.TotalDurationMs))
	b.WriteString(fmt.Sprintf("- Sandbox duration: %d ms\n", result.Metrics.SandboxDurationMs))
	b.WriteString(fmt.Sprintf("- Tool calls: %d\n", result.Metrics.ToolCallCount))
	b.WriteString(fmt.Sprintf("- Permission denials: %d\n", result.Metrics.PermissionDenyCount))
	if len(result.Metrics.ExceptionCounts) > 0 {
		b.WriteString("- Exception types:\n")
		for k, v := range result.Metrics.ExceptionCounts {
			b.WriteString(fmt.Sprintf("  - %s: %d\n", k, v))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Sandbox Execution\n\n")
	if len(result.SandboxRuns) == 0 {
		b.WriteString("No sandbox runs recorded.\n\n")
	} else {
		for i, run := range result.SandboxRuns {
			b.WriteString(fmt.Sprintf("%d. `%s` (%s) — **%s** exit=%d duration=%dms\n",
				i+1, run.Command, run.Runtime, run.Status, run.ExitCode, run.DurationMs))
			if run.ErrorType != "" {
				b.WriteString(fmt.Sprintf("   - error_type: %s\n", run.ErrorType))
			}
			if run.Stderr != "" {
				b.WriteString(fmt.Sprintf("   - stderr: `%s`\n", run.Stderr))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Governance\n\n")
	if len(result.PermissionDecisions) == 0 {
		b.WriteString("No permission decisions recorded.\n\n")
	} else {
		for i, d := range result.PermissionDecisions {
			b.WriteString(fmt.Sprintf("%d. [%s] `%s` → **%s**", i+1, d.ToolName, d.Command, d.Action))
			if d.Reason != "" {
				b.WriteString(fmt.Sprintf(" (%s)", d.Reason))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Recommendations\n\n")
	if len(result.Findings) == 0 && len(result.Warnings) == 0 {
		b.WriteString("No action required.\n")
	} else {
		all := append(append([]findings.Finding{}, result.Findings...), result.Warnings...)
		for i, f := range all {
			b.WriteString(fmt.Sprintf("%d. [%s] %s:%d — %s\n", i+1, f.RuleID, f.File, f.Line, f.Recommendation))
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeFinding(b *strings.Builder, index int, f findings.Finding) {
	b.WriteString(fmt.Sprintf("### %d. %s (%s)\n\n", index, f.Title, f.Severity))
	b.WriteString(fmt.Sprintf("- **File:** `%s:%d`\n", f.File, f.Line))
	b.WriteString(fmt.Sprintf("- **Category:** %s\n", f.Category))
	b.WriteString(fmt.Sprintf("- **Rule:** %s\n", f.RuleID))
	b.WriteString(fmt.Sprintf("- **Confidence:** %.2f\n", f.Confidence))
	b.WriteString(fmt.Sprintf("- **Evidence:** `%s`\n", f.Evidence))
	b.WriteString(fmt.Sprintf("- **Recommendation:** %s\n\n", f.Recommendation))
}

func severityOrder(counts map[string]int) []string {
	order := []string{"critical", "high", "medium", "low"}
	var out []string
	for _, sev := range order {
		if counts[sev] > 0 {
			out = append(out, sev)
		}
	}
	for sev := range counts {
		if !contains(out, sev) {
			out = append(out, sev)
		}
	}
	return out
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
