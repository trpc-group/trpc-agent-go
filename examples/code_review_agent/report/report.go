//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package report writes JSON and Markdown review reports.
package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// Write writes review_report.json and review_report.md.
func Write(outDir string, r review.ReviewReport) ([]review.Artifact, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	jsonPath := filepath.Join(outDir, "review_report.json")
	mdPath := filepath.Join(outDir, "review_report.md")
	safeReport := redactReport(r)
	data, err := json.MarshalIndent(safeReport, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return nil, err
	}
	md := []byte(redaction.RedactText(markdown(safeReport)))
	if err := os.WriteFile(mdPath, md, 0o644); err != nil {
		return nil, err
	}
	return []review.Artifact{
		artifact("json_report", jsonPath, data),
		artifact("markdown_report", mdPath, md),
	}, nil
}

func markdown(r review.ReviewReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "- Task: `%s`\n", r.Task.ID)
	fmt.Fprintf(&b, "- Status: `%s`\n", r.Task.Status)
	fmt.Fprintf(&b, "- Summary: %s\n", r.Summary)
	fmt.Fprintf(&b, "- Findings: %d\n", len(r.Findings))
	fmt.Fprintf(&b, "- Needs human review: %d\n\n", len(r.NeedsHumanReview))

	fmt.Fprintf(&b, "## Severity Summary\n\n")
	keys := make([]string, 0, len(r.Metrics.SeverityCounts))
	for k := range r.Metrics.SeverityCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %d\n", k, r.Metrics.SeverityCounts[k])
	}
	if len(keys) == 0 {
		fmt.Fprintf(&b, "- none: 0\n")
	}

	writeFindings(&b, "Findings", r.Findings)
	writeFindings(&b, "Needs Human Review", r.NeedsHumanReview)
	writeFindings(&b, "Warnings", r.Warnings)

	fmt.Fprintf(&b, "\n## Permission Decisions\n\n")
	if len(r.PermissionDecisions) == 0 {
		fmt.Fprintf(&b, "No external command decisions were needed.\n")
	} else {
		for _, d := range r.PermissionDecisions {
			fmt.Fprintf(&b, "- `%s`: **%s** - %s\n", d.Command, d.Decision, d.Reason)
		}
	}

	fmt.Fprintf(&b, "\n## Sandbox Runs\n\n")
	if len(r.SandboxRuns) == 0 {
		fmt.Fprintf(&b, "No sandbox checks were executed.\n")
	} else {
		for _, run := range r.SandboxRuns {
			fmt.Fprintf(&b, "- `%s`: %s, exit=%d, duration=%dms\n", run.Command, run.Status, run.ExitCode, run.DurationMS)
			if run.Error != "" {
				fmt.Fprintf(&b, "  - error: %s\n", run.Error)
			}
		}
	}

	fmt.Fprintf(&b, "\n## Metrics\n\n")
	fmt.Fprintf(&b, "- total duration: %dms\n", r.Metrics.TotalDurationMS)
	fmt.Fprintf(&b, "- sandbox duration: %dms\n", r.Metrics.SandboxDurationMS)
	fmt.Fprintf(&b, "- tool calls: %d\n", r.Metrics.ToolCallCount)
	fmt.Fprintf(&b, "- permission denies: %d\n", r.Metrics.PermissionDenyCount)
	return b.String()
}

func writeFindings(b *strings.Builder, title string, findings []review.Finding) {
	fmt.Fprintf(b, "\n## %s\n\n", title)
	if len(findings) == 0 {
		fmt.Fprintf(b, "None.\n")
		return
	}
	for _, f := range findings {
		fmt.Fprintf(b, "### [%s] %s\n\n", f.Severity, f.Title)
		fmt.Fprintf(b, "- File: `%s:%d`\n", f.File, f.Line)
		fmt.Fprintf(b, "- Rule: `%s`\n", f.RuleID)
		fmt.Fprintf(b, "- Category: `%s`\n", f.Category)
		fmt.Fprintf(b, "- Confidence: %.2f\n", f.Confidence)
		fmt.Fprintf(b, "- Evidence: `%s`\n", f.Evidence)
		fmt.Fprintf(b, "- Recommendation: %s\n\n", f.Recommendation)
	}
}

func redactReport(r review.ReviewReport) review.ReviewReport {
	out := r
	out.Task.InputSummary = redaction.RedactText(out.Task.InputSummary)
	out.Task.Error = redaction.RedactText(out.Task.Error)
	out.Summary = redaction.RedactText(out.Summary)
	out.Files = make([]review.ChangedFile, len(r.Files))
	copy(out.Files, r.Files)
	for i := range out.Files {
		out.Files[i].Hunks = make([]review.Hunk, len(r.Files[i].Hunks))
		copy(out.Files[i].Hunks, r.Files[i].Hunks)
		for j := range out.Files[i].Hunks {
			out.Files[i].Hunks[j].Lines = make([]review.DiffLine, len(r.Files[i].Hunks[j].Lines))
			copy(out.Files[i].Hunks[j].Lines, r.Files[i].Hunks[j].Lines)
			for k := range out.Files[i].Hunks[j].Lines {
				out.Files[i].Hunks[j].Lines[k].Content = redaction.RedactText(out.Files[i].Hunks[j].Lines[k].Content)
			}
		}
	}
	out.Findings = redactFindings(r.Findings)
	out.Warnings = redactFindings(r.Warnings)
	out.NeedsHumanReview = redactFindings(r.NeedsHumanReview)
	out.SandboxRuns = make([]review.SandboxRun, len(r.SandboxRuns))
	copy(out.SandboxRuns, r.SandboxRuns)
	for i := range out.SandboxRuns {
		out.SandboxRuns[i].Command = redaction.RedactText(out.SandboxRuns[i].Command)
		out.SandboxRuns[i].StdoutExcerpt = redaction.RedactText(out.SandboxRuns[i].StdoutExcerpt)
		out.SandboxRuns[i].StderrExcerpt = redaction.RedactText(out.SandboxRuns[i].StderrExcerpt)
		out.SandboxRuns[i].Error = redaction.RedactText(out.SandboxRuns[i].Error)
	}
	out.PermissionDecisions = make([]review.PermissionDecision, len(r.PermissionDecisions))
	copy(out.PermissionDecisions, r.PermissionDecisions)
	for i := range out.PermissionDecisions {
		out.PermissionDecisions[i].Command = redaction.RedactText(out.PermissionDecisions[i].Command)
		out.PermissionDecisions[i].Reason = redaction.RedactText(out.PermissionDecisions[i].Reason)
	}
	out.Artifacts = make([]review.Artifact, len(r.Artifacts))
	copy(out.Artifacts, r.Artifacts)
	for i := range out.Artifacts {
		out.Artifacts[i].Path = redaction.RedactText(out.Artifacts[i].Path)
	}
	return out
}

func redactFindings(in []review.Finding) []review.Finding {
	out := make([]review.Finding, len(in))
	copy(out, in)
	for i := range out {
		out[i].Title = redaction.RedactText(out[i].Title)
		out[i].Evidence = redaction.RedactText(out[i].Evidence)
		out[i].Recommendation = redaction.RedactText(out[i].Recommendation)
	}
	return out
}

func artifact(kind, path string, data []byte) review.Artifact {
	sum := sha256.Sum256(data)
	return review.Artifact{
		Kind:      kind,
		Path:      path,
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(data)),
	}
}
