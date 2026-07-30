//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package report implements the ReportGenerator GraphAgent node.
// Generates JSON and Markdown review reports.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/sanitize"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Run is the ReportGenerator GraphAgent node.
// Reads findings, warnings, sandbox_results, permission_decisions, and node
// timings from state; writes json_report_path and md_report_path.
func Run(ctx context.Context, gs graph.State) (any, error) {
	start := time.Now()
	defer func() {
		gs[state.StateKeyNodeReportGeneratorMs] = time.Since(start).Milliseconds()
	}()

	taskID, _ := gs[state.StateKeyTaskID].(string)
	findings, _ := gs[state.StateKeyFindings].([]types.Finding)
	warnings, _ := gs[state.StateKeyWarnings].([]types.Finding)
	permDecisions, _ := gs[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	sandboxResults, _ := gs[state.StateKeySandboxResults].([]types.SandboxResult)
	outputDir, _ := gs[state.StateKeyOutputDir].(string)
	if outputDir == "" {
		outputDir = "./output"
	}

	report := buildReport(taskID, findings, warnings, permDecisions, sandboxResults, gs)

	// ── Sanitize sensitive data in findings before output ──
	redactor := sanitize.NewRedactor(nil, "***REDACTED***")
	for i := range findings {
		findings[i].Evidence = redactor.RedactFinding(findings[i].Evidence)
		findings[i].Recommendation = redactor.RedactFinding(findings[i].Recommendation)
	}
	for i := range warnings {
		warnings[i].Evidence = redactor.RedactFinding(warnings[i].Evidence)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Generate JSON report
	jsonPath := filepath.Join(outputDir, taskID+"_report.json")
	jsonData, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return nil, fmt.Errorf("write json report: %w", err)
	}

	// Generate Markdown report (already sanitized via findings)
	mdPath := filepath.Join(outputDir, taskID+"_report.md")
	md := formatMarkdown(report, findings, warnings)
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		return nil, fmt.Errorf("write md report: %w", err)
	}

	gs[state.StateKeyJSONReportPath] = jsonPath
	gs[state.StateKeyMDReportPath] = mdPath
	return gs, nil
}

func buildReport(taskID string, findings, warnings []types.Finding,
	decisions []types.PermissionDecision, sandboxResults []types.SandboxResult,
	gs graph.State) types.ReviewReport {

	sevDist := map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "warning": 0}
	catDist := make(map[string]int)
	for _, f := range findings {
		sevDist[f.Severity]++
		catDist[f.Category]++
	}
	for _, w := range warnings {
		sevDist["warning"]++
		catDist[w.Category]++
	}

	// Collect node timings
	nodeTimings := make(map[string]int64)
	timingKeys := []string{
		state.StateKeyNodeDiffParserMs, state.StateKeyNodePermissionFilterMs,
		state.StateKeyNodeSandboxRunnerMs, state.StateKeyNodeRuleEngineMs,
		state.StateKeyNodeLLMAnalyzerMs, state.StateKeyNodeDedupEngineMs,
		state.StateKeyNodeReportGeneratorMs, state.StateKeyNodeStorageWriterMs,
	}
	for _, key := range timingKeys {
		if v, ok := gs[key].(int64); ok {
			nodeTimings[key] = v
		}
	}

	// Permission summary
	var permSum types.PermissionSummary
	permSum.Total = len(decisions)
	for _, d := range decisions {
		switch d.Decision {
		case "allow":
			permSum.Allowed++
		case "deny":
			permSum.Denied++
		case "needs_human_review":
			permSum.NeedsHR++
		}
	}

	// Sandbox summary
	var sandboxSum []types.SandboxSummary
	for _, s := range sandboxResults {
		sandboxSum = append(sandboxSum, types.SandboxSummary{
			Command:    s.Command,
			ExitCode:   s.ExitCode,
			DurationMs: s.DurationMs,
			TimedOut:   s.TimedOut,
			ErrorType:  s.ErrorType,
		})
	}

	return types.ReviewReport{
		TaskID:               taskID,
		FindingsCount:        len(findings),
		WarningsCount:        len(warnings),
		SeverityDistribution: sevDist,
		CategoryDistribution: catDist,
		Summary:              fmt.Sprintf("Reviewed %d findings (%d warnings).", len(findings), len(warnings)),
		NodeTimings:          nodeTimings,
		PermissionSummary:    permSum,
		SandboxSummary:       sandboxSum,
	}
}

func formatMarkdown(report types.ReviewReport, findings, warnings []types.Finding) string {
	var sb strings.Builder

	sb.WriteString("# Code Review Report\n\n")
	sb.WriteString(fmt.Sprintf("**Task ID**: `%s`\n\n", report.TaskID))

	// Summary
	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("- **Findings**: %d\n", report.FindingsCount))
	sb.WriteString(fmt.Sprintf("- **Warnings (needs human review)**: %d\n", report.WarningsCount))

	// Severity breakdown
	sb.WriteString("\n## Severity Distribution\n\n")
	sb.WriteString("| Severity | Count |\n|----------|------|\n")
	for _, sev := range []string{"critical", "high", "medium", "low", "warning"} {
		if c := report.SeverityDistribution[sev]; c > 0 {
			sb.WriteString(fmt.Sprintf("| %s | %d |\n", sev, c))
		}
	}

	// Findings detail
	sb.WriteString("\n## Findings\n\n")
	for i, f := range findings {
		sb.WriteString(fmt.Sprintf("### %d. [%s] %s\n\n", i+1, strings.ToUpper(f.Severity), f.Title))
		sb.WriteString(fmt.Sprintf("- **File**: `%s:%d`\n", f.File, f.Line))
		sb.WriteString(fmt.Sprintf("- **Category**: %s\n", f.Category))
		sb.WriteString(fmt.Sprintf("- **Source**: %s\n", f.Source))
		sb.WriteString(fmt.Sprintf("- **Confidence**: %.0f%%\n", f.Confidence*100))
		if f.Evidence != "" {
			sb.WriteString(fmt.Sprintf("\n```\n%s\n```\n", strings.TrimSpace(f.Evidence)))
		}
		if f.Recommendation != "" {
			sb.WriteString(fmt.Sprintf("\n**Fix**: %s\n\n", f.Recommendation))
		}
		sb.WriteString("---\n\n")
	}

	// Warnings
	if len(warnings) > 0 {
		sb.WriteString("## Needs Human Review\n\n")
		for i, w := range warnings {
			sb.WriteString(fmt.Sprintf("%d. [%s] **%s** (`%s:%d`, confidence: %.0f%%)\n\n",
				i+1, w.Category, w.Title, w.File, w.Line, w.Confidence*100))
		}
	}

	// Sandbox summary
	sb.WriteString("## Sandbox Execution Summary\n\n")
	sb.WriteString("| Command | Exit | Duration | Error |\n|---------|------|----------|-------|\n")
	for _, s := range report.SandboxSummary {
		sb.WriteString(fmt.Sprintf("| %s | %d | %dms | %s |\n",
			s.Command, s.ExitCode, s.DurationMs, s.ErrorType))
	}

	// Permission summary
	sb.WriteString("\n## Permission Decisions\n\n")
	sb.WriteString(fmt.Sprintf("- Allowed: %d, Denied: %d, Needs Review: %d\n",
		report.PermissionSummary.Allowed, report.PermissionSummary.Denied, report.PermissionSummary.NeedsHR))

	// Node timings
	sb.WriteString("\n## Performance\n\n")
	sb.WriteString("| Node | Time (ms) |\n|------|----------|\n")
	for name, ms := range report.NodeTimings {
		shortName := strings.TrimPrefix(name, "node_")
		shortName = strings.TrimSuffix(shortName, "_ms")
		sb.WriteString(fmt.Sprintf("| %s | %d |\n", shortName, ms))
	}

	return sb.String()
}
