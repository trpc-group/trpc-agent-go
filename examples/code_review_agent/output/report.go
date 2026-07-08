//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package output

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/parser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/storage"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/telemetry"
)

func GenerateReport(taskID string, diff *parser.DiffResult, findings []storage.Finding,
	metricsSummary telemetry.MetricsSummary, sandboxRuns []storage.SandboxRun,
	permissionRecords []storage.PermissionRecord, outputDir string) error {

	if diff == nil {
		return fmt.Errorf("diff parameter cannot be nil")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	var report strings.Builder
	report.WriteString("# Code Review Report\n\n")
	report.WriteString(fmt.Sprintf("**Task ID:** %s\n\n", taskID))
	report.WriteString(fmt.Sprintf("**Generated:** %s\n\n", time.Now().Format(time.RFC3339)))

	report.WriteString("## Summary\n\n")
	report.WriteString(fmt.Sprintf("- **Files Changed:** %d\n", len(diff.Files)))
	report.WriteString(fmt.Sprintf("- **Lines Added:** %d\n", diff.TotalAdded))
	report.WriteString(fmt.Sprintf("- **Lines Removed:** %d\n", diff.TotalRemoved))
	report.WriteString(fmt.Sprintf("- **Total Findings:** %d\n", metricsSummary.TotalFindings))
	report.WriteString(fmt.Sprintf("- **Review Time:** %s\n\n", metricsSummary.TotalReviewTime))

	if len(metricsSummary.FindingsBySeverity) > 0 {
		report.WriteString("### Findings by Severity\n\n")
		severities := make([]storage.FindingSeverity, 0, len(metricsSummary.FindingsBySeverity))
		for s := range metricsSummary.FindingsBySeverity {
			severities = append(severities, s)
		}
		sort.Slice(severities, func(i, j int) bool {
			return string(severities[i]) < string(severities[j])
		})
		for _, severity := range severities {
			count := metricsSummary.FindingsBySeverity[severity]
			report.WriteString(fmt.Sprintf("- **%s:** %d\n", severity, count))
		}
		report.WriteString("\n")
	}

	if len(findings) > 0 {
		report.WriteString("## Findings\n\n")
		for _, f := range findings {
			report.WriteString(fmt.Sprintf("### [%s] %s\n\n", f.Severity, f.Message))
			report.WriteString(fmt.Sprintf("- **File:** %s\n", f.Filepath))
			report.WriteString(fmt.Sprintf("- **Line:** %d\n", f.LineNumber))
			report.WriteString(fmt.Sprintf("- **Rule:** %s\n", f.RuleID))
			report.WriteString(fmt.Sprintf("- **Category:** %s\n", f.Category))
			report.WriteString(fmt.Sprintf("- **Confidence:** %.2f\n", f.Confidence))
			if f.Evidence != "" {
				report.WriteString(fmt.Sprintf("- **Evidence:** `%s`\n", truncate(f.Evidence, 100)))
			}
			if f.Suggestion != "" {
				report.WriteString(fmt.Sprintf("- **Suggestion:** %s\n", f.Suggestion))
			}
			if f.NeedsReview {
				report.WriteString("- **Requires Manual Review:** Yes\n")
			}
			report.WriteString("\n")
		}
	}

	if len(sandboxRuns) > 0 {
		report.WriteString("## Sandbox Executions\n\n")
		for _, run := range sandboxRuns {
			report.WriteString(fmt.Sprintf("### Command: %s\n\n", run.Command))
			report.WriteString(fmt.Sprintf("- **Exit Code:** %d\n", run.ExitCode))
			report.WriteString(fmt.Sprintf("- **Duration:** %dms\n", run.DurationMs))
			if run.TimedOut {
				report.WriteString("- **Timed Out:** Yes\n")
			}
			if run.Output != "" {
				report.WriteString(fmt.Sprintf("- **Output:**\n```\n%s\n```\n", truncate(run.Output, 500)))
			}
			if run.Error != "" {
				report.WriteString(fmt.Sprintf("- **Error:**\n```\n%s\n```\n", truncate(run.Error, 500)))
			}
			report.WriteString("\n")
		}
	}

	if len(permissionRecords) > 0 {
		report.WriteString("## Permission Records\n\n")
		for _, record := range permissionRecords {
			report.WriteString(fmt.Sprintf("- **Command:** `%s`\n", truncate(record.Command, 50)))
			report.WriteString(fmt.Sprintf("  - **Action:** %s\n", record.Action))
			report.WriteString(fmt.Sprintf("  - **Reason:** %s\n\n", record.Reason))
		}
	}

	if metricsSummary.SandboxExecutions > 0 {
		report.WriteString("## Metrics\n\n")
		report.WriteString(fmt.Sprintf("- **Sandbox Executions:** %d\n", metricsSummary.SandboxExecutions))
		report.WriteString(fmt.Sprintf("- **Sandbox Total Time:** %s\n", metricsSummary.SandboxExecutionTime))
		report.WriteString(fmt.Sprintf("- **Tool Calls:** %d\n", metricsSummary.ToolCalls))
		report.WriteString(fmt.Sprintf("- **Permission Blocks:** %d\n", metricsSummary.PermissionBlocks))
		report.WriteString(fmt.Sprintf("- **Errors:** %d\n", metricsSummary.Errors))
	}

	reportPath := filepath.Join(outputDir, "report.md")
	if err := os.WriteFile(reportPath, []byte(report.String()), 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
