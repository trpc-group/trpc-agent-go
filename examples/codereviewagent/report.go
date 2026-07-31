//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeReviewReports(outputDir string, report *reviewReport) ([]artifact, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal review report: %w", err)
	}
	jsonData = append(jsonData, '\n')
	markdownData := []byte(markdownReviewReport(report))
	files := []struct {
		kind string
		name string
		data []byte
	}{
		{kind: "json_report", name: "review_report.json", data: jsonData},
		{kind: "markdown_report", name: "review_report.md", data: markdownData},
	}
	artifacts := make([]artifact, 0, len(files))
	for _, file := range files {
		path := filepath.Join(outputDir, file.name)
		if err := os.WriteFile(path, file.data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", file.name, err)
		}
		sum := sha256.Sum256(file.data)
		artifacts = append(artifacts, artifact{
			Kind: file.kind, Path: file.name, SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(file.data)),
		})
	}
	return artifacts, nil
}

func markdownReviewReport(report *reviewReport) string {
	var output strings.Builder
	fmt.Fprintln(&output, "# Governed Code Review Report")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Task: `%s`\n", report.TaskID)
	fmt.Fprintf(&output, "- Status: **%s**\n", report.Status)
	fmt.Fprintf(&output, "- Mode: `%s`\n", report.Mode)
	fmt.Fprintf(&output, "- Skill: `%s`\n", report.Skill)
	fmt.Fprintf(&output, "- Diff SHA-256: `%s`\n", report.DiffSHA256)
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Findings")
	fmt.Fprintln(&output)
	if len(report.Findings) == 0 {
		fmt.Fprintln(&output, "No findings.")
	}
	for _, finding := range report.Findings {
		fmt.Fprintf(&output, "### %s `%s` — %s\n\n", finding.Severity, finding.RuleID, finding.Category)
		fmt.Fprintf(&output, "`%s:%d` · confidence %.2f · %s\n\n", finding.File, finding.StartLine, finding.Confidence, finding.Status)
		fmt.Fprintf(&output, "%s\n\nSuggested action: %s\n\n", finding.Message, finding.Suggestion)
	}
	fmt.Fprintln(&output, "## Governance")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Permission: `%s` — %s\n", report.Permission.Action, report.Permission.Reason)
	fmt.Fprintf(&output, "- Sandbox: `%s`, exit `%d`, timeout `%t`, capped `%t`\n",
		report.Sandbox.Status, report.Sandbox.ExitCode, report.Sandbox.TimedOut, report.Sandbox.OutputCapped)
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Monitoring")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Total duration: %d ms\n", report.Metrics.DurationMS)
	fmt.Fprintf(&output, "- Sandbox duration: %d ms\n", report.Metrics.SandboxDuration)
	fmt.Fprintf(&output, "- Tool calls: %d\n", report.Metrics.ToolCalls)
	fmt.Fprintf(&output, "- Permission checks: %d\n", report.Metrics.PermissionChecks)
	fmt.Fprintf(&output, "- Findings: %d; human-review warnings: %d\n", report.Metrics.FindingCount, report.Metrics.Warnings)
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Conclusion")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, report.Summary)
	return output.String()
}
