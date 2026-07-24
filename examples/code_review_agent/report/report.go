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

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sanitize"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// maxArtifactBytes caps a single report artifact so oversized reviews
// cannot exhaust disk or database storage.
const maxArtifactBytes = 2 << 20 // 2 MiB

// Write writes review_report.json and review_report.md.
func Write(outDir string, r review.ReviewReport) ([]review.Artifact, error) {
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, err
	}
	jsonPath := filepath.Join(outDir, "review_report.json")
	mdPath := filepath.Join(outDir, "review_report.md")
	manifestPath := filepath.Join(outDir, "artifact_manifest.json")
	safeReport := sanitize.Report(r)
	md := []byte(markdown(safeReport))
	if len(md) > maxArtifactBytes {
		return nil, fmt.Errorf("markdown report is %d bytes, exceeds artifact limit of %d bytes", len(md), maxArtifactBytes)
	}
	mdArtifact := artifact("markdown_report", mdPath, md)
	// The JSON report cannot contain its own checksum without a recursive
	// definition. It records the Markdown artifact; the sidecar manifest is
	// authoritative for both report files.
	safeReport.Artifacts = []review.Artifact{mdArtifact}
	data, err := json.MarshalIndent(safeReport, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > maxArtifactBytes {
		return nil, fmt.Errorf("json report is %d bytes, exceeds artifact limit of %d bytes", len(data), maxArtifactBytes)
	}
	jsonArtifact := artifact("json_report", jsonPath, data)
	manifest, err := json.MarshalIndent([]review.Artifact{jsonArtifact, mdArtifact}, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(manifest) > maxArtifactBytes {
		return nil, fmt.Errorf("artifact manifest is %d bytes, exceeds artifact limit of %d bytes", len(manifest), maxArtifactBytes)
	}
	manifestArtifact := artifact("artifact_manifest", manifestPath, manifest)

	written := make([]string, 0, 3)
	for _, file := range []struct {
		path string
		body []byte
	}{{jsonPath, data}, {mdPath, md}, {manifestPath, manifest}} {
		if err := writeAtomic(file.path, file.body); err != nil {
			for _, path := range written {
				_ = os.Remove(path)
			}
			return nil, err
		}
		written = append(written, file.path)
	}
	return []review.Artifact{jsonArtifact, mdArtifact, manifestArtifact}, nil
}

func writeAtomic(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".code-review-report-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpPath, path)
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
	fmt.Fprintf(&b, "- model duration: %dms\n", r.Metrics.ModelDurationMS)
	fmt.Fprintf(&b, "- tool calls: %d\n", r.Metrics.ToolCallCount)
	fmt.Fprintf(&b, "- model calls: %d\n", r.Metrics.ModelCallCount)
	fmt.Fprintf(&b, "- permission denies: %d\n", r.Metrics.PermissionDenyCount)
	fmt.Fprintf(&b, "- permission intercepts: %d\n", r.Metrics.PermissionInterceptCount)
	fmt.Fprintf(&b, "- blocked commands: %d\n", r.Metrics.BlockedCommandCount)
	fmt.Fprintf(&b, "- skipped commands: %d\n", r.Metrics.SkippedCommandCount)
	fmt.Fprintf(&b, "- warnings: %d\n", r.Metrics.WarningCount)
	fmt.Fprintf(&b, "- needs human review: %d\n", r.Metrics.NeedsHumanReviewCount)
	exceptionKeys := make([]string, 0, len(r.Metrics.ExceptionCounts))
	for key := range r.Metrics.ExceptionCounts {
		exceptionKeys = append(exceptionKeys, key)
	}
	sort.Strings(exceptionKeys)
	for _, key := range exceptionKeys {
		fmt.Fprintf(&b, "- exception %s: %d\n", key, r.Metrics.ExceptionCounts[key])
	}
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
