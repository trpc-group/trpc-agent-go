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
	r = redactedReport(r)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func redactedReport(in review.Report) review.Report {
	out := in
	out.Task.Error = redactString(in.Task.Error)
	out.Summary = redactString(in.Summary)
	out.Plan.Model = redactString(in.Plan.Model)
	out.Plan.Provider = redactString(in.Plan.Provider)
	out.Plan.Source = redactString(in.Plan.Source)
	out.Plan.Skill = redactString(in.Plan.Skill)
	out.Plan.Runtime = redactString(in.Plan.Runtime)
	out.Plan.Commands = redactStrings(in.Plan.Commands)
	out.Plan.RuleSources = redactStrings(in.Plan.RuleSources)
	out.ChangedFiles = make([]review.DiffFile, len(in.ChangedFiles))
	for index, file := range in.ChangedFiles {
		out.ChangedFiles[index] = file
		out.ChangedFiles[index].OldPath = redactString(file.OldPath)
		out.ChangedFiles[index].NewPath = redactString(file.NewPath)
		out.ChangedFiles[index].PackageDir = redactString(file.PackageDir)
		out.ChangedFiles[index].Hunks = make([]review.DiffHunk, len(file.Hunks))
		for hunkIndex, hunk := range file.Hunks {
			out.ChangedFiles[index].Hunks[hunkIndex] = hunk
			out.ChangedFiles[index].Hunks[hunkIndex].Lines = make([]review.DiffLine, len(hunk.Lines))
			for lineIndex, line := range hunk.Lines {
				out.ChangedFiles[index].Hunks[hunkIndex].Lines[lineIndex] = line
				out.ChangedFiles[index].Hunks[hunkIndex].Lines[lineIndex].Content = redactString(line.Content)
			}
		}
	}
	out.Findings = make([]review.Finding, len(in.Findings))
	for index, finding := range in.Findings {
		out.Findings[index] = finding
		out.Findings[index].Title = redactString(finding.Title)
		out.Findings[index].Category = redactString(finding.Category)
		out.Findings[index].File = redactString(finding.File)
		out.Findings[index].Evidence = redactString(finding.Evidence)
		out.Findings[index].Recommendation = redactString(finding.Recommendation)
		out.Findings[index].Source = redactString(finding.Source)
		out.Findings[index].RuleID = redactString(finding.RuleID)
		out.Findings[index].Status = redactString(finding.Status)
		out.Findings[index].Fingerprint = redactString(finding.Fingerprint)
	}
	out.SandboxRuns = make([]review.SandboxRun, len(in.SandboxRuns))
	for index, run := range in.SandboxRuns {
		out.SandboxRuns[index] = run
		out.SandboxRuns[index].Runtime = redactString(run.Runtime)
		out.SandboxRuns[index].Command = redactString(run.Command)
		out.SandboxRuns[index].Status = redactString(run.Status)
		out.SandboxRuns[index].StdoutRedacted = redactString(run.StdoutRedacted)
		out.SandboxRuns[index].StderrRedacted = redactString(run.StderrRedacted)
		out.SandboxRuns[index].ErrorType = redactString(run.ErrorType)
	}
	out.PermissionDecisions = make([]review.PermissionDecisionRecord, len(in.PermissionDecisions))
	for index, decision := range in.PermissionDecisions {
		out.PermissionDecisions[index] = decision
		out.PermissionDecisions[index].ToolName = redactString(decision.ToolName)
		out.PermissionDecisions[index].Command = redactString(decision.Command)
		out.PermissionDecisions[index].FrameworkAction = redactString(decision.FrameworkAction)
		out.PermissionDecisions[index].SafetyDecision = redactString(decision.SafetyDecision)
		out.PermissionDecisions[index].RiskLevel = redactString(decision.RiskLevel)
		out.PermissionDecisions[index].RuleID = redactString(decision.RuleID)
		out.PermissionDecisions[index].Reason = redactString(decision.Reason)
	}
	out.Artifacts = append([]review.ArtifactRecord(nil), in.Artifacts...)
	for index, artifact := range out.Artifacts {
		out.Artifacts[index].Kind = redactString(artifact.Kind)
		out.Artifacts[index].Path = redactString(artifact.Path)
		out.Artifacts[index].MimeType = redactString(artifact.MimeType)
		out.Artifacts[index].SHA256 = redactString(artifact.SHA256)
	}
	out.Conclusion = redactString(in.Conclusion)
	return out
}

func redactString(value string) string {
	return redact.Text(value).Text
}

func redactStrings(values []string) []string {
	if values == nil {
		return nil
	}
	redacted := make([]string, len(values))
	for index, value := range values {
		redacted[index] = redactString(value)
	}
	return redacted
}

// Markdown renders a redacted Markdown report.
func Markdown(r review.Report) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "Task `%s` finished with status `%s`.\n\n", r.Task.ID, r.Task.Status)
	fmt.Fprintf(&b, "## Summary\n\n%s\n\n", r.Summary)
	writeFindingsSummary(&b, r)
	fmt.Fprintf(&b, "## Model Plan\n\n")
	if r.Plan.Model == "" {
		fmt.Fprintf(&b, "No model plan recorded.\n\n")
	} else {
		fmt.Fprintf(&b, "- model: %s\n", r.Plan.Model)
		fmt.Fprintf(&b, "- provider: %s\n", r.Plan.Provider)
		fmt.Fprintf(&b, "- source: %s\n", r.Plan.Source)
		fmt.Fprintf(&b, "- skill: %s\n", r.Plan.Skill)
		fmt.Fprintf(&b, "- runtime: %s\n", r.Plan.Runtime)
		fmt.Fprintf(&b, "- commands: %s\n", strings.Join(r.Plan.Commands, ", "))
		fmt.Fprintf(&b, "- rules: %s\n\n", strings.Join(r.Plan.RuleSources, ", "))
	}
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
	writeFixRecommendations(&b, r.Findings)
	writeHumanReview(&b, r.Findings)
	fmt.Fprintf(&b, "## Governance\n\n")
	if len(r.PermissionDecisions) == 0 {
		fmt.Fprintf(&b, "No permission decisions recorded.\n\n")
	} else {
		fmt.Fprintf(&b, "Blocked or escalated decisions: %d.\n\n", blockedDecisionCount(r.PermissionDecisions))
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
		fmt.Fprintf(&b, "Sandbox duration: %d ms. Output is redacted and capped.\n\n", r.Metrics.SandboxDurationMillis)
		for _, run := range r.SandboxRuns {
			fmt.Fprintf(&b, "- `%s` runtime=%s status=%s exit=%d error=%s truncated=%t\n",
				run.Command, run.Runtime, run.Status, run.ExitCode, run.ErrorType, run.OutputTruncated)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Metrics\n\n")
	fmt.Fprintf(&b, "- findings: %d\n", r.Metrics.FindingCount)
	fmt.Fprintf(&b, "- permission blocks: %d\n", r.Metrics.PermissionBlockedCount)
	fmt.Fprintf(&b, "- redactions: %d\n", r.Metrics.RedactionCount)
	fmt.Fprintf(&b, "- total duration ms: %d\n", r.Metrics.TotalDurationMillis)
	fmt.Fprintf(&b, "- sandbox duration ms: %d\n", r.Metrics.SandboxDurationMillis)
	fmt.Fprintf(&b, "- tool calls: %d\n", r.Metrics.ToolCallCount)
	fmt.Fprintf(&b, "- severity distribution: %s\n", r.Metrics.SeverityDistributionJSON)
	fmt.Fprintf(&b, "- error distribution: %s\n", r.Metrics.ErrorDistributionJSON)
	fmt.Fprintf(&b, "\nConclusion: %s\n", r.Conclusion)
	return []byte(redact.Text(b.String()).Text)
}

func writeFindingsSummary(b *strings.Builder, r review.Report) {
	fmt.Fprintf(b, "## Findings Summary\n\n")
	fmt.Fprintf(b, "| Severity | Count |\n")
	fmt.Fprintf(b, "| --- | ---: |\n")
	for _, severity := range []string{review.SeverityCritical, review.SeverityHigh, review.SeverityMedium, review.SeverityLow} {
		fmt.Fprintf(b, "| %s | %d |\n", severity, r.Metrics.SeverityDistribution[severity])
	}
	fmt.Fprintf(b, "\n")

	categoryCounts := map[string]int{}
	for _, finding := range r.Findings {
		categoryCounts[finding.Category]++
	}
	if len(categoryCounts) == 0 {
		fmt.Fprintf(b, "No finding categories recorded.\n\n")
		return
	}
	fmt.Fprintf(b, "| Category | Count |\n")
	fmt.Fprintf(b, "| --- | ---: |\n")
	for _, category := range sortedIntKeys(categoryCounts) {
		fmt.Fprintf(b, "| %s | %d |\n", category, categoryCounts[category])
	}
	fmt.Fprintf(b, "\n")
}

func writeFixRecommendations(b *strings.Builder, findings []review.Finding) {
	fmt.Fprintf(b, "## Fix Recommendations\n\n")
	if len(findings) == 0 {
		fmt.Fprintf(b, "No fixes required.\n\n")
		return
	}
	seen := map[string]bool{}
	for _, finding := range findings {
		key := finding.RuleID + ":" + finding.Recommendation
		if finding.Recommendation == "" || seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(b, "- `%s`: %s\n", finding.RuleID, finding.Recommendation)
	}
	if len(seen) == 0 {
		fmt.Fprintf(b, "No executable recommendations recorded.\n")
	}
	fmt.Fprintf(b, "\n")
}

func writeHumanReview(b *strings.Builder, findings []review.Finding) {
	fmt.Fprintf(b, "## Human Review\n\n")
	var items []review.Finding
	for _, finding := range findings {
		if finding.Status == review.FindingStatusNeedsHumanReview || finding.Status == review.FindingStatusWarning {
			items = append(items, finding)
		}
	}
	if len(items) == 0 {
		fmt.Fprintf(b, "No warnings or needs-human-review findings.\n\n")
		return
	}
	for _, finding := range items {
		fmt.Fprintf(b, "- `%s` %s:%d status=%s confidence=%.2f title=%s\n",
			finding.RuleID, finding.File, finding.Line, finding.Status, finding.Confidence, finding.Title)
	}
	fmt.Fprintf(b, "\n")
}

func blockedDecisionCount(decisions []review.PermissionDecisionRecord) int {
	count := 0
	for _, decision := range decisions {
		if decision.Blocked || decision.FrameworkAction == "ask" || decision.SafetyDecision == "needs_human_review" {
			count++
		}
	}
	return count
}

func sortedIntKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
