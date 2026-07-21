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

// maxArtifactBytes caps a single report artifact so oversized reviews
// cannot exhaust disk or database storage.
const maxArtifactBytes = 2 << 20 // 2 MiB

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
	if len(data) > maxArtifactBytes {
		return nil, fmt.Errorf("json report is %d bytes, exceeds artifact limit of %d bytes", len(data), maxArtifactBytes)
	}
	md := []byte(redaction.RedactText(markdown(safeReport)))
	if len(md) > maxArtifactBytes {
		return nil, fmt.Errorf("markdown report is %d bytes, exceeds artifact limit of %d bytes", len(md), maxArtifactBytes)
	}
	// Validate both artifacts before writing either so a markdown
	// failure cannot leave an orphaned JSON report behind.
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(mdPath, md, 0o644); err != nil {
		_ = os.Remove(jsonPath)
		return nil, err
	}
	return []review.Artifact{
		artifact("json_report", jsonPath, data),
		artifact("markdown_report", mdPath, md),
	}, nil
}

// markdown renders the review report as human-readable markdown.
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

	fmt.Fprintf(&b, "\n## Filter Decisions\n\n")
	if len(r.FilterDecisions) == 0 {
		fmt.Fprintf(&b, "No noise-control filter decisions were recorded.\n")
	} else {
		for _, d := range r.FilterDecisions {
			fmt.Fprintf(&b, "- `%s` at `%s:%d` (%s): **%s** - %s\n",
				d.RuleID, d.File, d.Line, d.Stage, d.Decision, d.Reason)
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

// writeFindings appends one findings section to the markdown report.
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

// redactReport applies secret redaction to every report field.
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
	out.FilterDecisions = make([]review.FilterDecision, len(r.FilterDecisions))
	copy(out.FilterDecisions, r.FilterDecisions)
	for i := range out.FilterDecisions {
		out.FilterDecisions[i].Reason = redaction.RedactText(out.FilterDecisions[i].Reason)
	}
	out.Artifacts = make([]review.Artifact, len(r.Artifacts))
	copy(out.Artifacts, r.Artifacts)
	for i := range out.Artifacts {
		out.Artifacts[i].Path = redaction.RedactText(out.Artifacts[i].Path)
	}
	return out
}

// redactFindings returns a copy of findings with redacted text fields.
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

// artifact describes a produced file with its checksum and size.
func artifact(kind, path string, data []byte) review.Artifact {
	sum := sha256.Sum256(data)
	return review.Artifact{
		Kind:      kind,
		Path:      path,
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(data)),
	}
}
