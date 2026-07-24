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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxArtifactBytes = 1 << 20

// RenderReports renders redacted deterministic JSON and Markdown reports.
func RenderReports(report ReviewReport) ([]byte, []byte, error) {
	report = sanitizeReport(report)
	jsonReport, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal JSON report: %w", err)
	}
	jsonReport = append(jsonReport, '\n')

	var markdown bytes.Buffer
	fmt.Fprintf(&markdown, "# Code Review Report\n\n")
	fmt.Fprintf(&markdown, "- Task: `%s`\n", report.TaskID)
	fmt.Fprintf(&markdown, "- Status: `%s`\n", report.Status)
	fmt.Fprintf(&markdown, "- Conclusion: `%s`\n", report.Conclusion)
	fmt.Fprintf(&markdown, "- Mode: `%s`\n", report.Mode)
	fmt.Fprintf(&markdown, "- Runtime: `%s`\n", report.Runtime)
	fmt.Fprintf(&markdown, "- Skill: `%s`\n\n", report.Skill)

	fmt.Fprintln(&markdown, "## Findings Summary")
	fmt.Fprintln(&markdown)
	fmt.Fprintf(
		&markdown,
		"%d high-confidence findings and %d warnings across %d changed files.\n\n",
		len(report.Findings), len(report.Warnings), len(report.Input.ChangedFiles),
	)
	fmt.Fprintln(&markdown, "## Severity Statistics")
	fmt.Fprintln(&markdown)
	for _, severity := range []string{
		severityCritical, severityHigh, severityMedium, severityLow,
	} {
		fmt.Fprintf(
			&markdown, "- `%s`: %d\n",
			severity, report.Metrics.Severity[severity],
		)
	}
	fmt.Fprintln(&markdown)

	renderFindingSection(&markdown, "Findings", report.Findings)
	renderFindingSection(
		&markdown, "Human Review",
		report.NeedsHumanReview,
	)
	renderGovernanceSection(&markdown, report.Decisions)
	renderSandboxSection(&markdown, report.SandboxRuns)
	renderMetricsSection(&markdown, report.Metrics)

	return []byte(Redact(string(jsonReport))),
		[]byte(Redact(markdown.String())), nil
}

func renderFindingSection(
	output *bytes.Buffer,
	title string,
	findings []Finding,
) {
	fmt.Fprintf(output, "## %s\n\n", title)
	if len(findings) == 0 {
		fmt.Fprintln(output, "None.")
		fmt.Fprintln(output)
		return
	}
	for _, finding := range findings {
		fmt.Fprintf(
			output, "### %s: %s\n\n",
			strings.ToUpper(finding.Severity), finding.Title,
		)
		fmt.Fprintf(
			output,
			"- Location: `%s:%d`\n- Category: `%s`\n- Rule: `%s`\n"+
				"- Confidence: `%.2f`\n- Source: `%s`\n",
			finding.File, finding.Line, finding.Category, finding.RuleID,
			finding.Confidence, finding.Source,
		)
		fmt.Fprintf(output, "- Evidence: `%s`\n", inlineMarkdown(finding.Evidence))
		fmt.Fprintf(
			output, "- Recommendation: %s\n\n",
			finding.Recommendation,
		)
	}
}

func renderGovernanceSection(
	output *bytes.Buffer,
	decisions []PermissionDecision,
) {
	fmt.Fprintln(output, "## Governance Decisions")
	fmt.Fprintln(output)
	if len(decisions) == 0 {
		fmt.Fprintln(output, "No sandbox command was requested.")
		fmt.Fprintln(output)
		return
	}
	for _, decision := range decisions {
		fmt.Fprintf(
			output, "- `%s` risk=`%s`: `%s`",
			decision.Action, decision.Risk, inlineMarkdown(decision.Command),
		)
		if decision.Reason != "" {
			fmt.Fprintf(output, " (%s)", decision.Reason)
		}
		fmt.Fprintln(output)
	}
	fmt.Fprintln(output)
}

func renderSandboxSection(output *bytes.Buffer, runs []SandboxRun) {
	fmt.Fprintln(output, "## Sandbox Execution")
	fmt.Fprintln(output)
	if len(runs) == 0 {
		fmt.Fprintln(output, "No sandbox command was executed.")
		fmt.Fprintln(output)
		return
	}
	for _, run := range runs {
		fmt.Fprintf(
			output,
			"- `%s`: status=`%s`, exit=`%d`, duration=`%dms`, timeout=`%t`",
			inlineMarkdown(run.Command), run.Status, run.ExitCode,
			run.DurationMS, run.TimedOut,
		)
		if run.ErrorType != "" {
			fmt.Fprintf(output, ", error=`%s`", run.ErrorType)
		}
		fmt.Fprintln(output)
	}
	fmt.Fprintln(output)
}

func renderMetricsSection(output *bytes.Buffer, metrics Metrics) {
	fmt.Fprintln(output, "## Monitoring")
	fmt.Fprintln(output)
	fmt.Fprintf(output, "- Total duration: `%dms`\n", metrics.TotalDurationMS)
	fmt.Fprintf(
		output, "- Sandbox duration: `%dms`\n", metrics.SandboxDurationMS,
	)
	fmt.Fprintf(output, "- Tool calls: `%d`\n", metrics.ToolCalls)
	fmt.Fprintf(
		output, "- Permission blocks: `%d`\n", metrics.PermissionBlocked,
	)
	if len(metrics.Errors) == 0 {
		fmt.Fprintln(output, "- Exceptions: `0`")
		return
	}
	keys := make([]string, 0, len(metrics.Errors))
	for key := range metrics.Errors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(output, "- Exception `%s`: `%d`\n", key, metrics.Errors[key])
	}
}

func inlineMarkdown(value string) string {
	value = strings.ReplaceAll(value, "`", "'")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return Redact(value)
}

// WriteReportFiles atomically writes bounded report artifacts.
func WriteReportFiles(
	outputDir string,
	jsonReport []byte,
	markdownReport []byte,
) ([]ArtifactRecord, error) {
	if outputDir == "" {
		return nil, errors.New("output directory is required")
	}
	if len(jsonReport) > maxArtifactBytes ||
		len(markdownReport) > maxArtifactBytes {
		return nil, fmt.Errorf(
			"report exceeds %d-byte artifact limit", maxArtifactBytes,
		)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	files := []struct {
		kind string
		name string
		data []byte
	}{
		{kind: "report_json", name: "review_report.json", data: jsonReport},
		{kind: "report_markdown", name: "review_report.md", data: markdownReport},
	}
	artifacts := make([]ArtifactRecord, 0, len(files))
	for _, file := range files {
		destination := filepath.Join(outputDir, file.name)
		if err := writeAtomic(destination, file.data); err != nil {
			return nil, err
		}
		digest := sha256.Sum256(file.data)
		artifacts = append(artifacts, ArtifactRecord{
			Kind:      file.kind,
			Path:      file.name,
			SHA256:    hex.EncodeToString(digest[:]),
			SizeBytes: int64(len(file.data)),
		})
	}
	return artifacts, nil
}

func writeAtomic(destination string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(destination), ".review-report-*")
	if err != nil {
		return fmt.Errorf("create temporary report: %w", err)
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set report permissions: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write report: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync report: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close report: %w", err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("replace report: %w", err)
	}
	return nil
}
