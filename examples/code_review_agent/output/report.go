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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/parser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/storage"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/telemetry"
)

func escapeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "#", "\\#")
	s = strings.ReplaceAll(s, "*", "\\*")
	return s
}

func findLongestBacktickSequence(s string) int {
	re := regexp.MustCompile("`+")
	matches := re.FindAllString(s, -1)
	maxLen := 0
	for _, match := range matches {
		if len(match) > maxLen {
			maxLen = len(match)
		}
	}
	return maxLen
}

func getCodeFence(s string) string {
	longest := findLongestBacktickSequence(s)
	return strings.Repeat("`", longest+1)
}

func GenerateReportContent(taskID string, diff *parser.DiffResult, findings []storage.Finding,
	metricsSummary telemetry.MetricsSummary, sandboxRuns []storage.SandboxRun,
	permissionRecords []storage.PermissionRecord) string {

	var report strings.Builder
	report.WriteString("# Code Review Report\n\n")
	report.WriteString(fmt.Sprintf("**Task ID:** %s\n\n", escapeMarkdown(taskID)))
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
			report.WriteString(fmt.Sprintf("- **%s:** %d\n", escapeMarkdown(string(severity)), count))
		}
		report.WriteString("\n")
	}

	if len(findings) > 0 {
		report.WriteString("## Findings\n\n")
		for _, f := range findings {
			report.WriteString(fmt.Sprintf("### [%s] %s\n\n", escapeMarkdown(string(f.Severity)), escapeMarkdown(f.Message)))
			report.WriteString(fmt.Sprintf("- **File:** %s\n", escapeMarkdown(f.Filepath)))
			report.WriteString(fmt.Sprintf("- **Line:** %d\n", f.LineNumber))
			report.WriteString(fmt.Sprintf("- **Rule:** %s\n", escapeMarkdown(f.RuleID)))
			report.WriteString(fmt.Sprintf("- **Category:** %s\n", escapeMarkdown(string(f.Category))))
			report.WriteString(fmt.Sprintf("- **Confidence:** %.2f\n", f.Confidence))
			if f.Evidence != "" {
				report.WriteString(fmt.Sprintf("- **Evidence:** `%s`\n", escapeMarkdown(truncate(f.Evidence, 100))))
			}
			if f.Suggestion != "" {
				report.WriteString(fmt.Sprintf("- **Suggestion:** %s\n", escapeMarkdown(f.Suggestion)))
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
			report.WriteString(fmt.Sprintf("### Command: %s\n\n", escapeMarkdown(run.Command)))
			report.WriteString(fmt.Sprintf("- **Exit Code:** %d\n", run.ExitCode))
			report.WriteString(fmt.Sprintf("- **Duration:** %dms\n", run.DurationMs))
			if run.TimedOut {
				report.WriteString("- **Timed Out:** Yes\n")
			}
			if run.Output != "" {
				output := truncate(run.Output, 500)
				fence := getCodeFence(output)
				report.WriteString(fmt.Sprintf("- **Output:**\n%s\n%s\n%s\n", fence, output, fence))
			}
			if run.Error != "" {
				errorMsg := truncate(run.Error, 500)
				fence := getCodeFence(errorMsg)
				report.WriteString(fmt.Sprintf("- **Error:**\n%s\n%s\n%s\n", fence, errorMsg, fence))
			}
			report.WriteString("\n")
		}
	}

	if len(permissionRecords) > 0 {
		report.WriteString("## Permission Records\n\n")
		for _, record := range permissionRecords {
			report.WriteString(fmt.Sprintf("- **Command:** `%s`\n", escapeMarkdown(truncate(record.Command, 50))))
			report.WriteString(fmt.Sprintf("  - **Action:** %s\n", escapeMarkdown(record.Action)))
			report.WriteString(fmt.Sprintf("  - **Reason:** %s\n\n", escapeMarkdown(record.Reason)))
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

	return report.String()
}

func GenerateReport(taskID string, diff *parser.DiffResult, findings []storage.Finding,
	metricsSummary telemetry.MetricsSummary, sandboxRuns []storage.SandboxRun,
	permissionRecords []storage.PermissionRecord, outputDir string) error {

	if diff == nil {
		return fmt.Errorf("diff parameter cannot be nil")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	reportContent := GenerateReportContent(taskID, diff, findings, metricsSummary, sandboxRuns, permissionRecords)

	reportPath := filepath.Join(outputDir, "review_report.md")
	if err := os.WriteFile(reportPath, []byte(reportContent), 0644); err != nil {
		return fmt.Errorf("write review_report.md: %w", err)
	}

	type ReportJSON struct {
		TaskID             string                          `json:"task_id"`
		GeneratedAt        string                          `json:"generated_at"`
		FilesChanged       int                             `json:"files_changed"`
		LinesAdded         int                             `json:"lines_added"`
		LinesRemoved       int                             `json:"lines_removed"`
		TotalFindings      int                             `json:"total_findings"`
		ReviewTime         string                          `json:"review_time"`
		FindingsBySeverity map[storage.FindingSeverity]int `json:"findings_by_severity"`
		Findings           []storage.Finding               `json:"findings"`
		SandboxRuns        []storage.SandboxRun            `json:"sandbox_runs"`
		PermissionRecords  []storage.PermissionRecord      `json:"permission_records"`
		Metrics            struct {
			SandboxExecutions int    `json:"sandbox_executions"`
			SandboxTotalTime  string `json:"sandbox_total_time"`
			ToolCalls         int    `json:"tool_calls"`
			PermissionBlocks  int    `json:"permission_blocks"`
			Errors            int    `json:"errors"`
		} `json:"metrics"`
	}

	reportJSON := ReportJSON{
		TaskID:             taskID,
		GeneratedAt:        time.Now().Format(time.RFC3339),
		FilesChanged:       len(diff.Files),
		LinesAdded:         diff.TotalAdded,
		LinesRemoved:       diff.TotalRemoved,
		TotalFindings:      metricsSummary.TotalFindings,
		ReviewTime:         metricsSummary.TotalReviewTime.String(),
		FindingsBySeverity: metricsSummary.FindingsBySeverity,
		Findings:           findings,
		SandboxRuns:        sandboxRuns,
		PermissionRecords:  permissionRecords,
	}
	reportJSON.Metrics.SandboxExecutions = metricsSummary.SandboxExecutions
	reportJSON.Metrics.SandboxTotalTime = metricsSummary.SandboxExecutionTime.String()
	reportJSON.Metrics.ToolCalls = metricsSummary.ToolCalls
	reportJSON.Metrics.PermissionBlocks = metricsSummary.PermissionBlocks
	reportJSON.Metrics.Errors = metricsSummary.Errors

	jsonData, err := json.MarshalIndent(reportJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report JSON: %w", err)
	}

	jsonPath := filepath.Join(outputDir, "review_report.json")
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("write review_report.json: %w", err)
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
