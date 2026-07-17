//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func writeReports(report ReviewReport, outDir string) (string, string, []ArtifactRecord, error) {
	if outDir == "" {
		outDir = "code_review_agent_out"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", nil, err
	}
	jsonPath := filepath.Join(outDir, "review_report.json")
	mdPath := filepath.Join(outDir, "review_report.md")
	md := []byte(RedactSecrets(renderMarkdown(report)))
	now := time.Now().UTC()
	artifacts := []ArtifactRecord{
		{TaskID: report.Task.ID, Name: "review_report.json", Path: jsonPath, MIMEType: "application/json", CreatedAt: now},
		{TaskID: report.Task.ID, Name: "review_report.md", Path: mdPath, MIMEType: "text/markdown", SizeBytes: int64(len(md)), CreatedAt: now},
	}
	raw, artifacts, err := marshalReportWithArtifacts(report, artifacts)
	if err != nil {
		return "", "", nil, err
	}
	if err := os.WriteFile(jsonPath, raw, 0o600); err != nil {
		return "", "", nil, err
	}
	if err := os.WriteFile(mdPath, md, 0o600); err != nil {
		return "", "", nil, err
	}
	return jsonPath, mdPath, artifacts, nil
}

func marshalReportWithArtifacts(report ReviewReport, artifacts []ArtifactRecord) ([]byte, []ArtifactRecord, error) {
	for i := 0; i < 5; i++ {
		report.Artifacts = artifacts
		next, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		next = []byte(RedactSecrets(string(next)))
		size := int64(len(next))
		if artifacts[0].SizeBytes == size {
			return next, artifacts, nil
		}
		artifacts[0].SizeBytes = size
	}
	return nil, nil, fmt.Errorf("artifact metadata size did not stabilize")
}

func renderMarkdown(report ReviewReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "- Task: `%s`\n", report.Task.ID)
	fmt.Fprintf(&b, "- Status: `%s`\n", report.Task.Status)
	fmt.Fprintf(&b, "- Diff hash: `%s`\n", report.Task.DiffHash)
	fmt.Fprintf(&b, "- Conclusion: %s\n\n", report.Conclusion)
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Findings: %d\n", len(report.Findings))
	fmt.Fprintf(&b, "- Needs human review: %d\n", len(report.NeedsHumanReview))
	fmt.Fprintf(&b, "- Warnings: %d\n", len(report.Warnings))
	fmt.Fprintf(&b, "- Permission blocks: %d\n", report.Metrics.PermissionBlocks)
	fmt.Fprintf(&b, "- Tool calls: %d\n\n", report.Metrics.ToolCalls)
	fmt.Fprintf(&b, "## Severity\n\n")
	keys := make([]string, 0, len(report.Metrics.SeverityCounts))
	for k := range report.Metrics.SeverityCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %d\n", k, report.Metrics.SeverityCounts[k])
	}
	fmt.Fprintf(&b, "\n## Findings\n\n")
	renderFindingList(&b, report.Findings)
	fmt.Fprintf(&b, "\n## Warnings\n\n")
	renderFindingList(&b, report.Warnings)
	fmt.Fprintf(&b, "\n## Human Review\n\n")
	renderFindingList(&b, report.NeedsHumanReview)
	fmt.Fprintf(&b, "\n## Governance\n\n")
	for _, d := range report.FilterSummary {
		fmt.Fprintf(&b, "- filter `%s`: `%s`", d.Filter, d.Action)
		if d.Reason != "" {
			fmt.Fprintf(&b, " - %s", d.Reason)
		}
		fmt.Fprintf(&b, "\n")
	}
	for _, d := range report.PermissionSummary {
		fmt.Fprintf(&b, "- `%s`: `%s`", d.Action, d.Command)
		if d.Reason != "" {
			fmt.Fprintf(&b, " - %s", d.Reason)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "\n## Sandbox\n\n")
	for _, run := range report.SandboxRuns {
		fmt.Fprintf(&b, "- `%s` on `%s`: %s exit=%d timed_out=%t truncated=%t\n", run.Command, run.Runtime, run.Status, run.ExitCode, run.TimedOut, run.Truncated)
	}
	fmt.Fprintf(&b, "\n## Metrics\n\n")
	fmt.Fprintf(&b, "- Total duration ms: %d\n", report.Metrics.TotalDurationMS)
	fmt.Fprintf(&b, "- Sandbox duration ms: %d\n", report.Metrics.SandboxDurationMS)
	return b.String()
}

func renderFindingList(b *strings.Builder, findings []Finding) {
	if len(findings) == 0 {
		fmt.Fprintf(b, "No items.\n")
		return
	}
	for _, f := range findings {
		fmt.Fprintf(b, "- [%s] %s:%d %s (`%s`, %.2f)\n", f.Severity, f.File, f.Line, f.Title, f.RuleID, f.Confidence)
		fmt.Fprintf(b, "  Evidence: `%s`\n", strings.TrimSpace(f.Evidence))
		fmt.Fprintf(b, "  Recommendation: %s\n", f.Recommendation)
	}
}

func buildMetrics(start time.Time, runs []SandboxRun, permissions []PermissionRecord, findings []Finding, warnings []Finding, human []Finding) Metrics {
	m := Metrics{
		TotalDurationMS:       time.Since(start).Milliseconds(),
		SeverityCounts:        map[string]int{},
		ErrorCounts:           map[string]int{},
		ToolCalls:             len(runs),
		FindingCount:          len(findings),
		WarningCount:          len(warnings),
		NeedsHumanReviewCount: len(human),
	}
	for _, run := range runs {
		m.SandboxDurationMS += run.Duration.Milliseconds()
		if run.ErrorType != "" {
			m.ErrorCounts[run.ErrorType]++
		}
	}
	for _, d := range permissions {
		if d.Action != "allow" {
			m.PermissionBlocks++
		}
	}
	for _, f := range findings {
		m.SeverityCounts[f.Severity]++
	}
	return m
}
