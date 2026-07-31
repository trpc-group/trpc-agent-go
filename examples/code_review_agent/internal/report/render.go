//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package report renders review reports from one canonical DTO.
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/domain"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
)

// DTO is the canonical review report payload.
type DTO struct {
	TaskID           string           `json:"task_id"`
	Status           domain.Status    `json:"status"`
	Input            InputSummary     `json:"input"`
	Findings         []domain.Finding `json:"findings"`
	NeedsHumanReview []domain.Finding `json:"needs_human_review"`
	SandboxRuns      []sandbox.Result `json:"sandbox_runs"`
	Metrics          map[string]int   `json:"metrics"`
	Governance       []string         `json:"governance"`
	Artifacts        []string         `json:"artifacts"`
	ArtifactDetails  []Artifact       `json:"artifact_details"`
	Files            []string         `json:"files"`
	ParserWarnings   []string         `json:"parser_warnings"`
	Stats            Stats            `json:"stats"`
}

// Artifact describes a durable generated report artifact.
type Artifact struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"content_type"`
	Durable     bool   `json:"durable"`
}

// InputSummary is the redacted input summary persisted with the task.
type InputSummary struct {
	Kind       string `json:"kind"`
	Digest     string `json:"digest"`
	Files      int    `json:"files"`
	Hunks      int    `json:"hunks"`
	AddedLines int    `json:"added_lines"`
}

// Stats is the canonical aggregate summary for report and persistence sinks.
type Stats struct {
	Severity   map[string]int `json:"severity"`
	Tools      ToolStats      `json:"tools"`
	Sandbox    SandboxStats   `json:"sandbox"`
	Artifacts  int            `json:"artifacts"`
	Governance int            `json:"governance"`
}

// ToolStats summarizes sandbox command execution.
type ToolStats struct {
	Count          int            `json:"count"`
	DurationMS     int64          `json:"duration_ms"`
	ErrorByOutcome map[string]int `json:"error_by_outcome"`
}

// SandboxStats summarizes sandbox isolation and outcomes.
type SandboxStats struct {
	Runs       int  `json:"runs"`
	Failed     int  `json:"failed"`
	TimedOut   int  `json:"timed_out"`
	Truncated  int  `json:"truncated"`
	NonZero    int  `json:"non_zero"`
	Unisolated bool `json:"unisolated"`
}

// RenderJSON renders a stable, redacted JSON report.
func RenderJSON(dto DTO) ([]byte, error) {
	dto = normalizeDTO(redactDTO(dto))
	domain.SortFindings(dto.Findings)
	domain.SortFindings(dto.NeedsHumanReview)
	if dto.Metrics == nil {
		dto.Metrics = map[string]int{}
	}
	dto.Stats = BuildStats(dto)
	return json.MarshalIndent(dto, "", "  ")
}

// RenderMarkdown renders a redacted Markdown report.
func RenderMarkdown(dto DTO) string {
	dto = redactDTO(dto)
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "- Task: `%s`\n", escapeInline(dto.TaskID))
	fmt.Fprintf(&b, "- Status: `%s`\n", dto.Status)
	fmt.Fprintf(&b, "- Findings: `%d`\n", len(dto.Findings))
	fmt.Fprintf(&b, "- Human review: `%d`\n\n", len(dto.NeedsHumanReview))
	stats := BuildStats(dto)
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Severity: `%s`\n", formatCounts(stats.Severity))
	fmt.Fprintf(&b, "- Tools: `%d`, duration_ms `%d`, errors `%s`\n", stats.Tools.Count, stats.Tools.DurationMS, formatCounts(stats.Tools.ErrorByOutcome))
	fmt.Fprintf(&b, "- Sandbox: runs `%d`, non_zero `%d`, timed_out `%d`, truncated `%d`, unisolated `%t`\n", stats.Sandbox.Runs, stats.Sandbox.NonZero, stats.Sandbox.TimedOut, stats.Sandbox.Truncated, stats.Sandbox.Unisolated)
	fmt.Fprintf(&b, "- Governance decisions: `%d`\n", stats.Governance)
	fmt.Fprintf(&b, "- Artifacts: `%d`\n\n", stats.Artifacts)
	fmt.Fprintf(&b, "## Metrics\n\n")
	if len(dto.Metrics) == 0 {
		b.WriteString("None.\n\n")
	} else {
		keys := make([]string, 0, len(dto.Metrics))
		for key := range dto.Metrics {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "- `%s`: `%d`\n", escapeInline(key), dto.Metrics[key])
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Reviewed Files\n\n")
	if len(dto.Files) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, file := range dto.Files {
			fmt.Fprintf(&b, "- `%s`\n", escapeInline(file))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Parser Warnings\n\n")
	if len(dto.ParserWarnings) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, warning := range dto.ParserWarnings {
			fmt.Fprintf(&b, "- %s\n", escapeInline(warning))
		}
		b.WriteString("\n")
	}
	writeFindings(&b, "Findings", dto.Findings)
	writeFindings(&b, "Needs Human Review", dto.NeedsHumanReview)
	fmt.Fprintf(&b, "## Governance\n\n")
	if len(dto.Governance) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, decision := range dto.Governance {
			fmt.Fprintf(&b, "- `%s`\n", escapeInline(decision))
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "## Sandbox\n\n")
	if len(dto.SandboxRuns) == 0 {
		b.WriteString("No sandbox commands executed.\n\n")
	} else {
		for _, run := range dto.SandboxRuns {
			fmt.Fprintf(&b, "- `%s`: outcome `%s`, exit `%d`, timeout `%t`, truncated `%t`, duration_ms `%d`\n", escapeInline(run.CommandID), escapeInline(run.Outcome), run.ExitCode, run.TimedOut, run.Truncated, run.DurationMS)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Artifacts\n\n")
	if len(dto.ArtifactDetails) > 0 {
		for _, artifact := range dto.ArtifactDetails {
			fmt.Fprintf(&b, "- `%s`: `%s`, bytes `%d`, content_type `%s`, durable `%t`\n", escapeInline(artifact.Path), escapeInline(artifact.SHA256), artifact.Bytes, escapeInline(artifact.ContentType), artifact.Durable)
		}
	} else if len(dto.Artifacts) == 0 {
		b.WriteString("None.\n")
	} else {
		for _, artifact := range dto.Artifacts {
			fmt.Fprintf(&b, "- `%s`\n", escapeInline(artifact))
		}
	}
	return b.String()
}

// BuildStats computes deterministic aggregate report statistics.
func BuildStats(dto DTO) Stats {
	stats := Stats{
		Severity: map[string]int{},
		Tools: ToolStats{
			ErrorByOutcome: map[string]int{},
		},
		Artifacts:  artifactCount(dto),
		Governance: len(dto.Governance),
	}
	for _, f := range append(dto.Findings, dto.NeedsHumanReview...) {
		stats.Severity[string(f.Severity)]++
	}
	for _, run := range dto.SandboxRuns {
		stats.Tools.Count++
		stats.Tools.DurationMS += run.DurationMS
		stats.Sandbox.Runs++
		switch run.Outcome {
		case sandbox.OutcomeTimeout:
			stats.Tools.ErrorByOutcome["timeout"]++
			stats.Sandbox.TimedOut++
			stats.Sandbox.Failed++
		case sandbox.OutcomeDependencyUnavailable:
			stats.Tools.ErrorByOutcome[sandbox.OutcomeDependencyUnavailable]++
			stats.Sandbox.NonZero++
			stats.Sandbox.Failed++
		case sandbox.OutcomeNonZero:
			stats.Tools.ErrorByOutcome["nonzero"]++
			stats.Sandbox.NonZero++
			stats.Sandbox.Failed++
		default:
			stats.Tools.ErrorByOutcome["success"]++
		}
		if run.Truncated {
			stats.Sandbox.Truncated++
		}
	}
	if dto.Metrics["runtime_local"] > 0 {
		stats.Sandbox.Unisolated = true
	}
	return stats
}

func writeFindings(b *strings.Builder, title string, findings []domain.Finding) {
	fmt.Fprintf(b, "## %s\n\n", title)
	if len(findings) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("| Severity | Category | File | Line | Title | Evidence | Recommendation |\n")
	b.WriteString("| --- | --- | --- | ---: | --- | --- | --- |\n")
	for _, f := range findings {
		fmt.Fprintf(b, "| %s | %s | %s | %d | %s | %s | %s |\n",
			escapeCell(string(f.Severity)), escapeCell(f.Category), escapeCell(f.File), f.Line,
			escapeCell(f.Title), escapeCell(f.Evidence), escapeCell(f.Recommendation))
	}
	b.WriteString("\n")
}

func redactDTO(dto DTO) DTO {
	r := review.NewRedactor()
	redactFinding := func(f domain.Finding) domain.Finding {
		f.File = r.Redact(f.File)
		f.Title = r.Redact(f.Title)
		f.Evidence = r.Redact(f.Evidence)
		f.Recommendation = r.Redact(f.Recommendation)
		return f
	}
	for i := range dto.Findings {
		dto.Findings[i] = redactFinding(dto.Findings[i])
	}
	for i := range dto.NeedsHumanReview {
		dto.NeedsHumanReview[i] = redactFinding(dto.NeedsHumanReview[i])
	}
	for i := range dto.SandboxRuns {
		dto.SandboxRuns[i].Stdout = r.Redact(dto.SandboxRuns[i].Stdout)
		dto.SandboxRuns[i].Stderr = r.Redact(dto.SandboxRuns[i].Stderr)
	}
	for i := range dto.Governance {
		dto.Governance[i] = r.Redact(dto.Governance[i])
	}
	for i := range dto.ArtifactDetails {
		dto.ArtifactDetails[i].Path = r.Redact(dto.ArtifactDetails[i].Path)
		dto.ArtifactDetails[i].SHA256 = r.Redact(dto.ArtifactDetails[i].SHA256)
		dto.ArtifactDetails[i].ContentType = r.Redact(dto.ArtifactDetails[i].ContentType)
	}
	for i := range dto.Files {
		dto.Files[i] = r.Redact(dto.Files[i])
	}
	for i := range dto.ParserWarnings {
		dto.ParserWarnings[i] = r.Redact(dto.ParserWarnings[i])
	}
	sort.Strings(dto.Governance)
	sort.Strings(dto.Artifacts)
	sort.Slice(dto.ArtifactDetails, func(i, j int) bool { return dto.ArtifactDetails[i].Path < dto.ArtifactDetails[j].Path })
	sort.Strings(dto.Files)
	return dto
}

func escapeCell(s string) string {
	s = escapeInline(s)
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", "<br>")
	return s
}

func normalizeDTO(dto DTO) DTO {
	if dto.Findings == nil {
		dto.Findings = []domain.Finding{}
	}
	if dto.NeedsHumanReview == nil {
		dto.NeedsHumanReview = []domain.Finding{}
	}
	if dto.SandboxRuns == nil {
		dto.SandboxRuns = []sandbox.Result{}
	}
	if dto.Metrics == nil {
		dto.Metrics = map[string]int{}
	}
	if dto.Governance == nil {
		dto.Governance = []string{}
	}
	if dto.Artifacts == nil {
		dto.Artifacts = []string{}
	}
	if dto.ArtifactDetails == nil {
		dto.ArtifactDetails = []Artifact{}
	}
	if dto.Files == nil {
		dto.Files = []string{}
	}
	if dto.ParserWarnings == nil {
		dto.ParserWarnings = []string{}
	}
	return dto
}

func artifactCount(dto DTO) int {
	if len(dto.ArtifactDetails) > 0 {
		return len(dto.ArtifactDetails)
	}
	return len(dto.Artifacts)
}

func escapeInline(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '`':
			b.WriteString("\\`")
		case '\r', '\n', '\t':
			b.WriteByte(' ')
		default:
			if r < 0x20 {
				b.WriteByte(' ')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func formatCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, ",")
}
