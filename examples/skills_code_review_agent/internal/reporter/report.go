//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package reporter generates JSON and Markdown review reports.
package reporter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/rules"
)

// Metrics captures audit telemetry for a review run.
type Metrics struct {
	TotalDurationMs   int64          `json:"total_duration_ms"`
	SandboxDurationMs int64          `json:"sandbox_duration_ms"`
	ToolCallCount     int            `json:"tool_call_count"`
	FindingCount      int            `json:"finding_count"`
	SeverityDist      map[string]int `json:"severity_distribution"`
	CategoryDist      map[string]int `json:"category_distribution"`
}

// Report is the top-level output for a review run.
type Report struct {
	TaskID    string          `json:"task_id"`
	CreatedAt string          `json:"created_at"`
	DiffFile  string          `json:"diff_file,omitempty"`
	RepoPath  string          `json:"repo_path,omitempty"`
	Findings  []rules.Finding `json:"findings"`
	Warnings  []rules.Finding `json:"warnings,omitempty"`
	Metrics   Metrics         `json:"metrics"`
}

// Build partitions findings into confirmed and warnings, then assembles the report.
func Build(taskID, diffFile, repoPath string, findings []rules.Finding, metrics Metrics) Report {
	var confirmed, warnings []rules.Finding
	for _, f := range findings {
		if f.Confidence == "low" {
			warnings = append(warnings, f)
		} else {
			confirmed = append(confirmed, f)
		}
	}
	metrics.FindingCount = len(confirmed)
	metrics.SeverityDist = dist(confirmed, func(f rules.Finding) string { return f.Severity })
	metrics.CategoryDist = dist(confirmed, func(f rules.Finding) string { return f.Category })
	return Report{
		TaskID:    taskID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		DiffFile:  diffFile,
		RepoPath:  repoPath,
		Findings:  confirmed,
		Warnings:  warnings,
		Metrics:   metrics,
	}
}

// ToJSON serialises the report to indented JSON.
func ToJSON(r Report) (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	return string(b), err
}

// ToMarkdown converts the report to a Markdown document.
func ToMarkdown(r Report) string {
	var sb strings.Builder
	sb.WriteString("# Code Review Report\n\n")
	sb.WriteString(fmt.Sprintf("**Task ID:** %s  \n", r.TaskID))
	sb.WriteString(fmt.Sprintf("**Created:** %s  \n\n", r.CreatedAt))

	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("- Total findings: **%d**\n", len(r.Findings)))
	sb.WriteString(fmt.Sprintf("- Warnings (low confidence): **%d**\n\n", len(r.Warnings)))

	if len(r.Metrics.SeverityDist) > 0 {
		sb.WriteString("### Severity Distribution\n\n")
		for sev, count := range r.Metrics.SeverityDist {
			sb.WriteString(fmt.Sprintf("- %s: %d\n", sev, count))
		}
		sb.WriteString("\n")
	}

	if len(r.Findings) > 0 {
		sb.WriteString("## Findings\n\n")
		for i, f := range r.Findings {
			sb.WriteString(fmt.Sprintf("### %d. [%s] %s\n\n", i+1, strings.ToUpper(f.Severity), f.Title))
			sb.WriteString("| Field | Value |\n|---|---|\n")
			sb.WriteString(fmt.Sprintf("| Rule | `%s` |\n", f.RuleID))
			sb.WriteString(fmt.Sprintf("| Category | %s |\n", f.Category))
			sb.WriteString(fmt.Sprintf("| File | `%s:%d` |\n", f.File, f.Line))
			sb.WriteString(fmt.Sprintf("| Confidence | %s |\n\n", f.Confidence))
			sb.WriteString(fmt.Sprintf("**Evidence:**\n```go\n%s\n```\n\n", f.Evidence))
			sb.WriteString(fmt.Sprintf("**Recommendation:** %s\n\n", f.Recommendation))
		}
	} else {
		sb.WriteString("## Findings\n\nNo high/medium confidence findings.\n\n")
	}

	if len(r.Warnings) > 0 {
		sb.WriteString("## Warnings\n\n")
		for _, f := range r.Warnings {
			sb.WriteString(fmt.Sprintf("- **%s** `%s:%d` - %s *(low confidence)*\n", f.RuleID, f.File, f.Line, f.Title))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Metrics\n\n")
	sb.WriteString(fmt.Sprintf("- Total duration: %d ms\n", r.Metrics.TotalDurationMs))
	sb.WriteString(fmt.Sprintf("- Sandbox duration: %d ms\n", r.Metrics.SandboxDurationMs))

	return sb.String()
}

func dist[T any](items []T, key func(T) string) map[string]int {
	m := make(map[string]int)
	for _, item := range items {
		m[key(item)]++
	}
	return m
}
