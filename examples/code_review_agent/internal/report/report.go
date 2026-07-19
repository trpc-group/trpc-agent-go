//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package report aggregates the outputs of the code review pipeline
// (findings, sandbox runs, permission decisions, artifacts, and telemetry)
// into a single ReportData and renders it in two formats: a machine-readable
// JSON document and a human-readable Markdown document.
//
// All sensitive content (finding evidence and recommendations) is run through
// the redact package before being placed in ReportData so that neither the
// in-memory structure nor the rendered files contain plaintext secrets.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/telemetry"
)

// Conclusion is the overall verdict for a review task.
type Conclusion string

const (
	ConclusionPass        Conclusion = "pass"
	ConclusionNeedsReview Conclusion = "needs_human_review"
	ConclusionFail        Conclusion = "fail"
)

// ReportData is the aggregated, renderable output of a single review task.
type ReportData struct {
	TaskID              string
	Conclusion          Conclusion
	GeneratedAt         string
	Review              *review.Report
	SandboxRuns         []sandbox.RunResult
	PermissionDecisions []store.PermissionDecision
	Artifacts           []store.Artifact
	Metrics             telemetry.Summary
	SeverityStats       map[string]int // severity -> count
	TotalFindings       int
	TotalWarnings       int
	NeedsHumanReview    int
	PermissionBlocked   int
}

// severityOrder defines the display rank of each severity (lower sorts first).
var severityOrder = map[string]int{
	"critical": 0,
	"high":     1,
	"medium":   2,
	"low":      3,
}

// severityRank returns the display rank for a severity string. Unknown
// severities sort last (after "low").
func severityRank(sev string) int {
	if r, ok := severityOrder[sev]; ok {
		return r
	}
	return len(severityOrder)
}

// orderedSeverities returns the canonical severities in display order.
func orderedSeverities() []string {
	return []string{"critical", "high", "medium", "low"}
}

// Build aggregates all pipeline outputs into a ReportData. It applies
// redaction to finding evidence and recommendations so that the returned
// ReportData (and any rendered files) contain no plaintext secrets.
func Build(
	taskID string,
	rev *review.Report,
	runs []sandbox.RunResult,
	perms []store.PermissionDecision,
	arts []store.Artifact,
	metrics telemetry.Summary,
) *ReportData {
	redacted := redactReport(rev)
	stats := computeSeverityStats(redacted.Findings)
	blocked := countPermissionBlocked(perms)
	conclusion := computeConclusion(redacted, runs, len(redacted.NeedsHumanReview), blocked)

	return &ReportData{
		TaskID:              taskID,
		Conclusion:          conclusion,
		GeneratedAt:         time.Now().UTC().Format(time.RFC3339),
		Review:              redacted,
		SandboxRuns:         runs,
		PermissionDecisions: perms,
		Artifacts:           arts,
		Metrics:             metrics,
		SeverityStats:       stats,
		TotalFindings:       len(redacted.Findings),
		TotalWarnings:       len(redacted.Warnings),
		NeedsHumanReview:    len(redacted.NeedsHumanReview),
		PermissionBlocked:   blocked,
	}
}

// redactReport returns a deep copy of rev with all finding evidence and
// recommendation fields run through redact.MustText. The original rev is
// not mutated.
func redactReport(rev *review.Report) *review.Report {
	if rev == nil {
		return nil
	}
	out := &review.Report{
		TaskID:           rev.TaskID,
		Findings:         make([]review.Finding, len(rev.Findings)),
		Warnings:         make([]review.Warning, len(rev.Warnings)),
		NeedsHumanReview: make([]review.Finding, len(rev.NeedsHumanReview)),
	}
	for i, f := range rev.Findings {
		f.Evidence = redact.MustText(f.Evidence)
		f.Recommendation = redact.MustText(f.Recommendation)
		out.Findings[i] = f
	}
	for i, w := range rev.Warnings {
		w.Finding.Evidence = redact.MustText(w.Finding.Evidence)
		w.Finding.Recommendation = redact.MustText(w.Finding.Recommendation)
		out.Warnings[i] = w
	}
	for i, f := range rev.NeedsHumanReview {
		f.Evidence = redact.MustText(f.Evidence)
		f.Recommendation = redact.MustText(f.Recommendation)
		out.NeedsHumanReview[i] = f
	}
	return out
}

// computeSeverityStats counts findings by severity.
func computeSeverityStats(findings []review.Finding) map[string]int {
	stats := make(map[string]int, 4)
	for _, f := range findings {
		stats[f.Severity]++
	}
	return stats
}

// countPermissionBlocked returns the number of permission decisions whose
// Action is "deny" or "ask".
func countPermissionBlocked(perms []store.PermissionDecision) int {
	blocked := 0
	for _, p := range perms {
		if p.Action == "deny" || p.Action == "ask" {
			blocked++
		}
	}
	return blocked
}

// computeConclusion derives the overall conclusion from the review output,
// sandbox runs, needs-human-review count, and permission-blocked count.
func computeConclusion(
	rev *review.Report,
	runs []sandbox.RunResult,
	needsHumanReview, permissionBlocked int,
) Conclusion {
	for _, f := range rev.Findings {
		if f.Severity == "critical" {
			return ConclusionFail
		}
	}
	for _, r := range runs {
		if r.Status == sandbox.StatusFailed || r.Status == sandbox.StatusTimeout {
			return ConclusionNeedsReview
		}
	}
	if needsHumanReview > 0 || permissionBlocked > 0 {
		return ConclusionNeedsReview
	}
	return ConclusionPass
}

// ToJSON writes the report as indented JSON to <outDir>/<reportFileName>.
// The filename includes the task id (see reportFileName) so concurrent or
// repeated runs do not clobber each other's reports. It creates outDir if
// it does not exist and returns the absolute path of the written file.
func (r *ReportData) ToJSON(outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("report: create out dir: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("report: marshal json: %w", err)
	}
	path := filepath.Join(outDir, r.reportFileName("json"))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("report: write json: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("report: resolve json abs path: %w", err)
	}
	return abs, nil
}

// ToMarkdown writes the report as Markdown to <outDir>/<reportFileName>.
// The filename includes the task id (see reportFileName) so concurrent or
// repeated runs do not clobber each other's reports. It creates outDir if
// it does not exist and returns the absolute path of the written file.
func (r *ReportData) ToMarkdown(outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("report: create out dir: %w", err)
	}
	path := filepath.Join(outDir, r.reportFileName("md"))
	if err := os.WriteFile(path, []byte(r.markdown()), 0o644); err != nil {
		return "", fmt.Errorf("report: write markdown: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("report: resolve markdown abs path: %w", err)
	}
	return abs, nil
}

// reportFileName returns the filename for the report with the given
// extension. When TaskID is set, the file is named
// "review_report_<sanitizedTaskID>.<ext>" so concurrent or repeated runs do
// not clobber each other's reports. When TaskID is empty, the legacy fixed
// name "review_report.<ext>" is used. The task id is sanitized to a
// filesystem-safe charset ([A-Za-z0-9._-]) so unusual ids cannot produce
// paths that escape outDir via traversal or separators.
func (r *ReportData) reportFileName(ext string) string {
	base := "review_report"
	if id := sanitizeTaskIDForFile(r.TaskID); id != "" {
		return base + "_" + id + "." + ext
	}
	return base + "." + ext
}

// sanitizeTaskIDForFile returns a filesystem-safe representation of taskID
// suitable for use in artifact filenames. Characters outside [A-Za-z0-9._-]
// (notably path separators and traversal sequences) are replaced with '_'.
// An empty taskID yields an empty string so callers can fall back to the
// legacy fixed filename.
func sanitizeTaskIDForFile(taskID string) string {
	if taskID == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(taskID))
	for _, r := range taskID {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// WriteAll writes both the JSON and Markdown reports and returns
// (jsonPath, mdPath, error).
func (r *ReportData) WriteAll(outDir string) (string, string, error) {
	jsonPath, err := r.ToJSON(outDir)
	if err != nil {
		return "", "", err
	}
	mdPath, err := r.ToMarkdown(outDir)
	if err != nil {
		return "", "", err
	}
	return jsonPath, mdPath, nil
}

// PredictedPaths returns the absolute file paths that ToJSON and ToMarkdown
// will write to, without actually writing. This lets callers populate the
// Artifacts field (which references the report files themselves) before
// serialization so the written JSON/Markdown includes the artifact list.
// The returned paths match what WriteAll will return as long as outDir is
// not renamed between the two calls.
func (r *ReportData) PredictedPaths(outDir string) (jsonPath, mdPath string) {
	abs, err := filepath.Abs(outDir)
	if err != nil {
		abs = outDir
	}
	return filepath.Join(abs, r.reportFileName("json")),
		filepath.Join(abs, r.reportFileName("md"))
}

// markdown renders the full Markdown report. Section rendering is delegated
// to helper writers so that this function stays well under the complexity
// budget.
func (r *ReportData) markdown() string {
	var b strings.Builder
	writeHeader(&b, r)
	writeFindingsSummary(&b, r.SeverityStats)
	writeFindingsTable(&b, findingsOf(r.Review))
	writeWarningsSection(&b, r.Review)
	writeNeedsHumanReviewSection(&b, r.Review)
	writeGovernanceSection(&b, r.PermissionDecisions, r.PermissionBlocked)
	writeSandboxTable(&b, r.SandboxRuns)
	writeMetricsSection(&b, r.Metrics)
	writeArtifactsSection(&b, r.Artifacts)
	writeRecommendationsSection(&b, findingsOf(r.Review))
	return b.String()
}

// findingsOf safely extracts the confirmed findings slice from rev.
func findingsOf(rev *review.Report) []review.Finding {
	if rev == nil {
		return nil
	}
	return rev.Findings
}

// writeHeader writes the report title and top-level metadata.
func writeHeader(b *strings.Builder, r *ReportData) {
	fmt.Fprintln(b, "# Code Review Report")
	fmt.Fprintln(b)
	fmt.Fprintf(b, "- **Task ID:** %s\n", r.TaskID)
	fmt.Fprintf(b, "- **Generated At:** %s\n", r.GeneratedAt)
	fmt.Fprintf(b, "- **Conclusion:** %s\n", string(r.Conclusion))
	fmt.Fprintln(b)
}

// writeFindingsSummary writes the severity -> count table.
func writeFindingsSummary(b *strings.Builder, stats map[string]int) {
	fmt.Fprintln(b, "## Findings Summary")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "| Severity | Count |")
	fmt.Fprintln(b, "| --- | --- |")
	for _, sev := range orderedSeverities() {
		fmt.Fprintf(b, "| %s | %d |\n", sev, stats[sev])
	}
	fmt.Fprintln(b)
}

// writeFindingsTable writes the per-finding table, sorted by severity
// (critical first). Equal-severity findings preserve their input order
// (stable sort).
func writeFindingsTable(b *strings.Builder, findings []review.Finding) {
	fmt.Fprintln(b, "## Findings")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "| Severity | Location | RuleID | Title | Recommendation |")
	fmt.Fprintln(b, "| --- | --- | --- | --- | --- |")
	sorted := make([]review.Finding, len(findings))
	copy(sorted, findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		return severityRank(sorted[i].Severity) < severityRank(sorted[j].Severity)
	})
	for _, f := range sorted {
		loc := fmt.Sprintf("%s:%d", f.File, f.Line)
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n",
			f.Severity, loc, f.RuleID, f.Title, f.Recommendation)
	}
	fmt.Fprintln(b)
}

// writeWarningsSection writes the low-confidence warnings list.
func writeWarningsSection(b *strings.Builder, rev *review.Report) {
	fmt.Fprintln(b, "## Warnings")
	fmt.Fprintln(b)
	if rev == nil || len(rev.Warnings) == 0 {
		fmt.Fprintln(b, "_No warnings._")
		fmt.Fprintln(b)
		return
	}
	for _, w := range rev.Warnings {
		fmt.Fprintf(b, "- %s:%d (%s) — %s (reason: %s)\n",
			w.Finding.File, w.Finding.Line, w.Finding.Severity,
			w.Finding.Title, w.Reason)
	}
	fmt.Fprintln(b)
}

// writeNeedsHumanReviewSection writes the critical low-confidence findings
// that require human review.
func writeNeedsHumanReviewSection(b *strings.Builder, rev *review.Report) {
	fmt.Fprintln(b, "## Needs Human Review")
	fmt.Fprintln(b)
	if rev == nil || len(rev.NeedsHumanReview) == 0 {
		fmt.Fprintln(b, "_No findings require human review._")
		fmt.Fprintln(b)
		return
	}
	for _, f := range rev.NeedsHumanReview {
		fmt.Fprintf(b, "- %s:%d (%s) — %s\n", f.File, f.Line, f.Severity, f.Title)
	}
	fmt.Fprintln(b)
}

// writeGovernanceSection writes the permission decisions table and the
// count of blocked decisions.
func writeGovernanceSection(b *strings.Builder, perms []store.PermissionDecision, blocked int) {
	fmt.Fprintln(b, "## Governance (Permission Decisions)")
	fmt.Fprintln(b)
	fmt.Fprintf(b, "Blocked decisions: %d\n", blocked)
	fmt.Fprintln(b)
	fmt.Fprintln(b, "| Command | Action | Reason |")
	fmt.Fprintln(b, "| --- | --- | --- |")
	for _, p := range perms {
		fmt.Fprintf(b, "| %s | %s | %s |\n", p.Command, p.Action, p.Reason)
	}
	fmt.Fprintln(b)
}

// writeSandboxTable writes the sandbox execution summary table. The Command
// column is rendered as "-" because sandbox.RunResult does not carry the
// originating command (it lives on RunSpec, which is not persisted here).
func writeSandboxTable(b *strings.Builder, runs []sandbox.RunResult) {
	fmt.Fprintln(b, "## Sandbox Execution Summary")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "| Command | Status | ExitCode | Duration | TimedOut | Truncated |")
	fmt.Fprintln(b, "| --- | --- | --- | --- | --- | --- |")
	for _, r := range runs {
		fmt.Fprintf(b, "| - | %s | %d | %s | %v | %v |\n",
			r.Status, r.ExitCode, r.Duration, r.TimedOut, r.Truncated)
	}
	fmt.Fprintln(b)
}

// writeMetricsSection writes the telemetry metrics section.
func writeMetricsSection(b *strings.Builder, m telemetry.Summary) {
	fmt.Fprintln(b, "## Metrics")
	fmt.Fprintln(b)
	fmt.Fprintf(b, "- **Total Duration:** %s\n", m.TotalDuration)
	fmt.Fprintf(b, "- **Sandbox Duration:** %s\n", m.SandboxDuration)
	fmt.Fprintf(b, "- **Tool Calls:** %d\n", m.ToolCalls)
	fmt.Fprintf(b, "- **Permission Blocked:** %d\n", m.PermissionBlocked)
	fmt.Fprintf(b, "- **Finding Count:** %d\n", m.FindingCount)
	fmt.Fprintln(b, "- **Severity Distribution:**")
	for _, sev := range orderedSeverities() {
		fmt.Fprintf(b, "  - %s: %d\n", sev, m.SeverityCounts[sev])
	}
	if len(m.ExceptionTypes) > 0 {
		fmt.Fprintln(b, "- **Exception Types:**")
		for _, typ := range sortedInt64Keys(m.ExceptionTypes) {
			fmt.Fprintf(b, "  - %s: %d\n", typ, m.ExceptionTypes[typ])
		}
	}
	fmt.Fprintln(b)
}

// writeArtifactsSection writes the artifacts list.
func writeArtifactsSection(b *strings.Builder, arts []store.Artifact) {
	fmt.Fprintln(b, "## Artifacts")
	fmt.Fprintln(b)
	if len(arts) == 0 {
		fmt.Fprintln(b, "_No artifacts._")
		fmt.Fprintln(b)
		return
	}
	for _, a := range arts {
		fmt.Fprintf(b, "- %s | %s | %d bytes\n", a.Name, a.Path, a.SizeBytes)
	}
	fmt.Fprintln(b)
}

// writeRecommendationsSection writes the executable recommendations list,
// one bullet per finding.
func writeRecommendationsSection(b *strings.Builder, findings []review.Finding) {
	fmt.Fprintln(b, "## Executable Recommendations")
	fmt.Fprintln(b)
	if len(findings) == 0 {
		fmt.Fprintln(b, "_No recommendations._")
		fmt.Fprintln(b)
		return
	}
	for _, f := range findings {
		fmt.Fprintf(b, "- [%s:%d] %s\n", f.File, f.Line, f.Recommendation)
	}
	fmt.Fprintln(b)
}

// sortedInt64Keys returns the keys of a map[string]int64 sorted alphabetically.
func sortedInt64Keys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
