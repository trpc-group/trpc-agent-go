//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// BuildMetrics calculates report metrics from the durable records.
func BuildMetrics(taskID string, started time.Time, findings []review.Finding, runs []review.SandboxRun, decisions []review.PermissionDecisionRecord, redactions int) review.ReviewMetrics {
	metrics := review.ReviewMetrics{
		TaskID:                 taskID,
		ToolCallCount:          len(runs),
		FindingCount:           len(findings),
		SeverityDistribution:   map[string]int{},
		ErrorDistribution:      map[string]int{},
		RedactionCount:         redactions,
		TotalDurationMillis:    time.Since(started).Milliseconds(),
		SandboxDurationMillis:  0,
		PermissionBlockedCount: 0,
	}
	for _, finding := range findings {
		metrics.SeverityDistribution[finding.Severity]++
	}
	for _, run := range runs {
		metrics.SandboxDurationMillis += run.DurationMillis
		if run.ErrorType != "" {
			metrics.ErrorDistribution[run.ErrorType]++
		}
	}
	for _, decision := range decisions {
		if decision.Blocked {
			metrics.PermissionBlockedCount++
		}
	}
	metrics.SeverityDistributionJSON = mustJSON(metrics.SeverityDistribution)
	metrics.ErrorDistributionJSON = mustJSON(metrics.ErrorDistribution)
	return metrics
}

// JSON renders a redacted pretty JSON report.
func JSON(r review.Report) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	redacted := redact.Text(buf.String())
	return []byte(redacted.Text), nil
}

// Markdown renders a redacted Markdown report.
func Markdown(r review.Report) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "Task `%s` finished with status `%s`.\n\n", r.Task.ID, r.Task.Status)
	fmt.Fprintf(&b, "## Summary\n\n%s\n\n", r.Summary)
	fmt.Fprintf(&b, "## Findings\n\n")
	if len(r.Findings) == 0 {
		fmt.Fprintf(&b, "No findings.\n\n")
	} else {
		for _, finding := range r.Findings {
			fmt.Fprintf(&b, "- **%s** `%s` %s:%d - %s\n", finding.Severity, finding.RuleID, finding.File, finding.Line, finding.Title)
			fmt.Fprintf(&b, "  Evidence: `%s`\n", strings.TrimSpace(finding.Evidence))
			fmt.Fprintf(&b, "  Recommendation: %s\n", finding.Recommendation)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Governance\n\n")
	if len(r.PermissionDecisions) == 0 {
		fmt.Fprintf(&b, "No permission decisions recorded.\n\n")
	} else {
		for _, decision := range r.PermissionDecisions {
			fmt.Fprintf(&b, "- `%s` action=%s safety=%s risk=%s blocked=%t reason=%s\n",
				decision.ToolName, decision.FrameworkAction, decision.SafetyDecision, decision.RiskLevel, decision.Blocked, decision.Reason)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Sandbox\n\n")
	if len(r.SandboxRuns) == 0 {
		fmt.Fprintf(&b, "No sandbox runs recorded.\n\n")
	} else {
		for _, run := range r.SandboxRuns {
			fmt.Fprintf(&b, "- `%s` runtime=%s status=%s exit=%d error=%s\n", run.Command, run.Runtime, run.Status, run.ExitCode, run.ErrorType)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Metrics\n\n")
	fmt.Fprintf(&b, "- findings: %d\n", r.Metrics.FindingCount)
	fmt.Fprintf(&b, "- permission blocks: %d\n", r.Metrics.PermissionBlockedCount)
	fmt.Fprintf(&b, "- redactions: %d\n", r.Metrics.RedactionCount)
	fmt.Fprintf(&b, "\nConclusion: %s\n", r.Conclusion)
	return []byte(redact.Text(b.String()).Text)
}

// Write writes JSON and Markdown reports and returns artifact records.
func Write(outDir string, r review.Report, now time.Time) ([]review.ArtifactRecord, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	records := []review.ArtifactRecord{
		{
			ID:        "json_report-" + r.Task.ID,
			TaskID:    r.Task.ID,
			Kind:      "json_report",
			Path:      filepath.Join(outDir, "review_report.json"),
			MimeType:  "application/json",
			CreatedAt: now.UTC(),
		},
		{
			ID:        "markdown_report-" + r.Task.ID,
			TaskID:    r.Task.ID,
			Kind:      "markdown_report",
			Path:      filepath.Join(outDir, "review_report.md"),
			MimeType:  "text/markdown",
			CreatedAt: now.UTC(),
		},
	}
	if len(r.Artifacts) == 0 {
		r.Artifacts = []review.ArtifactRecord{
			{
				ID:        records[0].ID,
				TaskID:    records[0].TaskID,
				Kind:      records[0].Kind,
				Path:      filepath.Base(records[0].Path),
				MimeType:  records[0].MimeType,
				CreatedAt: records[0].CreatedAt,
			},
			{
				ID:        records[1].ID,
				TaskID:    records[1].TaskID,
				Kind:      records[1].Kind,
				Path:      filepath.Base(records[1].Path),
				MimeType:  records[1].MimeType,
				CreatedAt: records[1].CreatedAt,
			},
		}
	}
	jsonBytes, err := JSON(r)
	if err != nil {
		return nil, err
	}
	mdBytes := Markdown(r)
	artifacts := []struct {
		index int
		data  []byte
	}{
		{0, jsonBytes},
		{1, mdBytes},
	}
	for _, artifact := range artifacts {
		record := &records[artifact.index]
		if err := os.WriteFile(record.Path, artifact.data, 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", filepath.Base(record.Path), err)
		}
		sum := sha256.Sum256(artifact.data)
		record.SHA256 = hex.EncodeToString(sum[:])
	}
	return records, nil
}

func mustJSON(v map[string]int) string {
	if v == nil {
		return "{}"
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]int, len(v))
	for _, k := range keys {
		ordered[k] = v[k]
	}
	b, err := json.Marshal(ordered)
	if err != nil {
		return "{}"
	}
	return string(b)
}
