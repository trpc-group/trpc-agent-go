//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	reviewStatusCompleted = "completed"

	reviewConclusionPass             = "pass"
	reviewConclusionFindings         = "findings"
	reviewConclusionNeedsHumanReview = "needs_human_review"

	artifactKindJSONReport     = "review_report_json"
	artifactKindMarkdownReport = "review_report_markdown"
)

type reportPaths struct {
	JSON     string `json:"json,omitempty"`
	Markdown string `json:"markdown,omitempty"`
}

type reviewReport struct {
	TaskID      string           `json:"task_id"`
	Status      string           `json:"status"`
	Conclusion  string           `json:"conclusion"`
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  time.Time        `json:"finished_at"`
	DurationMS  int64            `json:"duration_ms"`
	Input       reportInput      `json:"input"`
	Runtime     reportRuntime    `json:"runtime"`
	Parse       reportParse      `json:"parse"`
	Rules       reportRules      `json:"rules"`
	Governance  reportGovernance `json:"governance"`
	Findings    []reviewFinding  `json:"findings"`
	Warnings    []reviewFinding  `json:"warnings"`
	Metrics     reportMetrics    `json:"metrics"`
	Artifacts   []reportArtifact `json:"artifacts,omitempty"`
	ReportPaths reportPaths      `json:"report_paths"`
}

type reportInput struct {
	Kind       string              `json:"kind"`
	Source     string              `json:"source"`
	DiffBytes  int                 `json:"diff_bytes"`
	DiffSHA256 string              `json:"diff_sha256"`
	Files      []reportFileSummary `json:"files,omitempty"`
}

type reportFileSummary struct {
	Path        string              `json:"path"`
	OldPath     string              `json:"old_path,omitempty"`
	NewPath     string              `json:"new_path,omitempty"`
	IsNew       bool                `json:"is_new,omitempty"`
	IsDeleted   bool                `json:"is_deleted,omitempty"`
	IsRename    bool                `json:"is_rename,omitempty"`
	IsBinary    bool                `json:"is_binary,omitempty"`
	PackageName string              `json:"package_name,omitempty"`
	Hunks       []reportHunkSummary `json:"hunks,omitempty"`
}

type reportHunkSummary struct {
	Header     string `json:"header"`
	OldStart   int    `json:"old_start"`
	OldCount   int    `json:"old_count"`
	NewStart   int    `json:"new_start"`
	NewCount   int    `json:"new_count"`
	AddedLines []int  `json:"added_lines,omitempty"`
}

type reportRuntime struct {
	Runtime           string `json:"runtime"`
	DryRun            bool   `json:"dry_run"`
	RuleOnly          bool   `json:"rule_only"`
	E2BTemplate       string `json:"e2b_template,omitempty"`
	EnableStaticcheck bool   `json:"enable_staticcheck"`
	OutputDir         string `json:"output_dir"`
	DBPath            string `json:"db_path"`
}

type reportParse struct {
	ChangedFiles    int      `json:"changed_files"`
	Hunks           int      `json:"hunks"`
	CandidateLines  int      `json:"candidate_lines"`
	Warnings        int      `json:"warnings"`
	WarningMessages []string `json:"warning_messages,omitempty"`
}

type reportRules struct {
	RuleMatches       int            `json:"rule_matches"`
	RuleWarnings      int            `json:"rule_warnings"`
	Findings          int            `json:"findings"`
	Warnings          int            `json:"warnings"`
	NeedsHumanReview  bool           `json:"needs_human_review"`
	SuppressedMatches int            `json:"suppressed_matches"`
	FindingRuleIDs    []string       `json:"finding_rule_ids"`
	WarningRuleIDs    []string       `json:"warning_rule_ids"`
	SeverityCounts    map[string]int `json:"severity_counts"`
}

type reportGovernance struct {
	SkillName           string               `json:"skill_name,omitempty"`
	SkillDigest         string               `json:"skill_digest,omitempty"`
	CommandsPlanned     int                  `json:"commands_planned"`
	CommandsAllowed     int                  `json:"commands_allowed"`
	CommandsBlocked     int                  `json:"commands_blocked"`
	PermissionBlocks    int                  `json:"permission_blocks"`
	FilterDecisions     []governanceDecision `json:"filter_decisions,omitempty"`
	PermissionDecisions []governanceDecision `json:"permission_decisions,omitempty"`
	SandboxRuns         []sandboxRun         `json:"sandbox_runs,omitempty"`
}

type reportMetrics struct {
	TotalDurationMS   int64          `json:"total_duration_ms"`
	SandboxDurationMS int64          `json:"sandbox_duration_ms"`
	ToolCalls         int            `json:"tool_calls"`
	PermissionBlocks  int            `json:"permission_blocks"`
	Findings          int            `json:"findings"`
	Warnings          int            `json:"warnings"`
	SeverityCounts    map[string]int `json:"severity_counts"`
	SuppressedMatches int            `json:"suppressed_matches"`
	Redactions        int            `json:"redactions"`
	ExceptionCounts   map[string]int `json:"exception_counts,omitempty"`
}

type reportArtifact struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

func buildReviewReport(
	taskID string,
	cfg config,
	input reviewInput,
	parsed parsedDiff,
	governance governanceResult,
	finalized finalizedFindings,
	parseWarningMessages []string,
	ruleMatches int,
	ruleWarnings int,
	redactions int,
	started time.Time,
	finished time.Time,
) reviewReport {
	totalRedactions := redactions
	redact := func(value string) string {
		redacted := redactText(value)
		totalRedactions += redacted.Count
		return redacted.Text
	}
	durationMS := finished.Sub(started).Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}
	report := reviewReport{
		TaskID:     taskID,
		Status:     reviewStatusCompleted,
		StartedAt:  started,
		FinishedAt: finished,
		DurationMS: durationMS,
		Input: reportInput{
			Kind:       input.kind,
			Source:     redact(input.source),
			DiffBytes:  len(input.diff),
			DiffSHA256: diffSHA256(input.diff),
			Files:      reportFileSummaries(parsed, redact),
		},
		Runtime: reportRuntime{
			Runtime:           cfg.effectiveRuntime,
			DryRun:            cfg.dryRun,
			RuleOnly:          cfg.ruleOnly,
			E2BTemplate:       redact(cfg.e2bTemplate),
			EnableStaticcheck: cfg.enableStaticcheck,
			OutputDir:         redact(cfg.outputDir),
			DBPath:            redact(cfg.dbPath),
		},
		Parse: reportParse{
			ChangedFiles:    len(parsed.Files),
			Hunks:           parsed.hunkCount(),
			CandidateLines:  len(parsed.candidateLines()),
			Warnings:        len(parsed.Warnings),
			WarningMessages: append([]string(nil), parseWarningMessages...),
		},
		Rules: reportRules{
			RuleMatches:       ruleMatches,
			RuleWarnings:      ruleWarnings,
			Findings:          len(finalized.Findings),
			Warnings:          len(finalized.Warnings),
			NeedsHumanReview:  finalized.NeedsHumanReview,
			SuppressedMatches: finalized.SuppressedMatches,
			FindingRuleIDs:    append([]string(nil), finalized.FindingRuleIDs...),
			WarningRuleIDs:    append([]string(nil), finalized.WarningRuleIDs...),
			SeverityCounts:    cloneIntMap(finalized.SeverityCounts),
		},
		Governance: reportGovernance{
			SkillName:           governance.SkillName,
			SkillDigest:         governance.SkillDigest,
			CommandsPlanned:     governance.CommandsPlanned,
			CommandsAllowed:     governance.CommandsAllowed,
			CommandsBlocked:     governance.CommandsBlocked,
			PermissionBlocks:    governance.PermissionBlocks,
			FilterDecisions:     append([]governanceDecision(nil), governance.FilterDecisions...),
			PermissionDecisions: append([]governanceDecision(nil), governance.PermissionDecisions...),
			SandboxRuns:         append([]sandboxRun(nil), governance.SandboxRuns...),
		},
		Findings: append([]reviewFinding(nil), finalized.Findings...),
		Warnings: append([]reviewFinding(nil), finalized.Warnings...),
	}
	report.Conclusion = determineConclusion(report)
	report.Metrics = buildReportMetrics(report, totalRedactions)
	return report
}

func diffSHA256(diff []byte) string {
	sum := sha256.Sum256(diff)
	return hex.EncodeToString(sum[:])
}

func reportFileSummaries(parsed parsedDiff, redact func(string) string) []reportFileSummary {
	files := make([]reportFileSummary, 0, len(parsed.Files))
	for _, file := range parsed.Files {
		summary := reportFileSummary{
			Path:        redact(file.reviewPath()),
			OldPath:     redact(file.OldPath),
			NewPath:     redact(file.NewPath),
			IsNew:       file.IsNew,
			IsDeleted:   file.IsDeleted,
			IsRename:    file.IsRename,
			IsBinary:    file.IsBinary,
			PackageName: redact(file.PackageName),
		}
		for _, hunk := range file.Hunks {
			hunkSummary := reportHunkSummary{
				Header:   redact(hunk.Header),
				OldStart: hunk.OldStart,
				OldCount: hunk.OldCount,
				NewStart: hunk.NewStart,
				NewCount: hunk.NewCount,
			}
			for _, line := range hunk.Lines {
				if line.Kind == diffLineAdded && line.NewLine > 0 {
					hunkSummary.AddedLines = append(hunkSummary.AddedLines, line.NewLine)
				}
			}
			summary.Hunks = append(summary.Hunks, hunkSummary)
		}
		files = append(files, summary)
	}
	return files
}

func determineConclusion(report reviewReport) string {
	if report.Rules.NeedsHumanReview || report.Governance.CommandsBlocked > 0 ||
		report.Governance.PermissionBlocks > 0 {
		return reviewConclusionNeedsHumanReview
	}
	for _, run := range report.Governance.SandboxRuns {
		if sandboxRunNeedsWarning(run) {
			return reviewConclusionNeedsHumanReview
		}
	}
	if len(report.Findings) > 0 {
		return reviewConclusionFindings
	}
	return reviewConclusionPass
}

func buildReportMetrics(report reviewReport, redactions int) reportMetrics {
	sandboxDuration := int64(0)
	exceptions := map[string]int{}
	for _, run := range report.Governance.SandboxRuns {
		sandboxDuration += run.DurationMS
		if run.Skipped {
			exceptions["sandbox_skipped"]++
		}
		if run.TimedOut {
			exceptions["sandbox_timeout"]++
		}
		if run.ExitCode != 0 {
			exceptions["sandbox_exit_nonzero"]++
		}
		if strings.TrimSpace(run.Error) != "" {
			exceptions["sandbox_error"]++
		}
	}
	if report.Parse.Warnings > 0 {
		exceptions["parse_warning"] = report.Parse.Warnings
	}
	return reportMetrics{
		TotalDurationMS:   report.DurationMS,
		SandboxDurationMS: sandboxDuration,
		ToolCalls:         len(report.Governance.SandboxRuns),
		PermissionBlocks:  report.Governance.PermissionBlocks,
		Findings:          len(report.Findings),
		Warnings:          len(report.Warnings),
		SeverityCounts:    cloneIntMap(report.Rules.SeverityCounts),
		SuppressedMatches: report.Rules.SuppressedMatches,
		Redactions:        redactions,
		ExceptionCounts:   exceptions,
	}
}

func (r reviewReport) summary() reviewSummary {
	return reviewSummary{
		TaskID:            r.TaskID,
		Status:            r.Status,
		Conclusion:        r.Conclusion,
		InputKind:         r.Input.Kind,
		Source:            r.Input.Source,
		DiffBytes:         r.Input.DiffBytes,
		DiffSHA256:        r.Input.DiffSHA256,
		Runtime:           r.Runtime.Runtime,
		DryRun:            r.Runtime.DryRun,
		RuleOnly:          r.Runtime.RuleOnly,
		OutputDir:         r.Runtime.OutputDir,
		DBPath:            r.Runtime.DBPath,
		E2BTemplate:       r.Runtime.E2BTemplate,
		EnableStaticcheck: r.Runtime.EnableStaticcheck,
		ChangedFiles:      r.Parse.ChangedFiles,
		Hunks:             r.Parse.Hunks,
		CandidateLines:    r.Parse.CandidateLines,
		ParseWarnings:     r.Parse.Warnings,
		RuleMatches:       r.Rules.RuleMatches,
		RuleWarnings:      r.Rules.RuleWarnings,
		CommandsPlanned:   r.Governance.CommandsPlanned,
		CommandsAllowed:   r.Governance.CommandsAllowed,
		CommandsBlocked:   r.Governance.CommandsBlocked,
		PermissionBlocks:  r.Governance.PermissionBlocks,
		Findings:          len(r.Findings),
		Warnings:          len(r.Warnings),
		NeedsHumanReview:  r.Rules.NeedsHumanReview,
		SuppressedMatches: r.Rules.SuppressedMatches,
		Redactions:        r.Metrics.Redactions,
		FindingRuleIDs:    append([]string(nil), r.Rules.FindingRuleIDs...),
		WarningRuleIDs:    append([]string(nil), r.Rules.WarningRuleIDs...),
		SeverityCounts:    cloneIntMap(r.Rules.SeverityCounts),
		ReportPaths:       r.ReportPaths,
		DurationMS:        r.DurationMS,
	}
}

func writeReviewReportFiles(report *reviewReport, outputDir string) error {
	taskDir := filepath.Join(outputDir, report.TaskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	report.ReportPaths = reportPaths{
		JSON:     filepath.ToSlash(filepath.Join(taskDir, "review_report.json")),
		Markdown: filepath.ToSlash(filepath.Join(taskDir, "review_report.md")),
	}

	markdownBytes := []byte(renderMarkdownReport(*report))
	if err := os.WriteFile(filepath.FromSlash(report.ReportPaths.Markdown), markdownBytes, 0o600); err != nil {
		return fmt.Errorf("write markdown report: %w", err)
	}
	markdownArtifact := reportArtifactFromBytes(
		artifactKindMarkdownReport,
		report.ReportPaths.Markdown,
		markdownBytes,
	)

	jsonPlaceholder := reportArtifact{
		Kind: artifactKindJSONReport,
		Path: report.ReportPaths.JSON,
	}
	report.Artifacts = []reportArtifact{jsonPlaceholder, markdownArtifact}
	jsonBytes, err := marshalReportJSON(*report)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.FromSlash(report.ReportPaths.JSON), jsonBytes, 0o600); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	report.Artifacts[0] = reportArtifactFromBytes(
		artifactKindJSONReport,
		report.ReportPaths.JSON,
		jsonBytes,
	)
	return nil
}

func marshalReportJSON(report reviewReport) ([]byte, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render json report: %w", err)
	}
	return append(data, '\n'), nil
}

func reportArtifactFromBytes(kind string, path string, content []byte) reportArtifact {
	sum := sha256.Sum256(content)
	return reportArtifact{
		Kind:   kind,
		Path:   path,
		SHA256: hex.EncodeToString(sum[:]),
		Bytes:  int64(len(content)),
	}
}

func renderMarkdownReport(report reviewReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", report.TaskID)
	fmt.Fprintf(&b, "- Status: `%s`\n", report.Status)
	fmt.Fprintf(&b, "- Conclusion: `%s`\n", report.Conclusion)
	fmt.Fprintf(&b, "- Runtime: `%s`\n", report.Runtime.Runtime)
	fmt.Fprintf(&b, "- Diff SHA-256: `%s`\n", report.Input.DiffSHA256)
	fmt.Fprintf(&b, "- Duration: %d ms\n\n", report.DurationMS)

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Findings: %d\n", len(report.Findings))
	fmt.Fprintf(&b, "- Warnings: %d\n", len(report.Warnings))
	fmt.Fprintf(&b, "- Commands: planned %d, allowed %d, blocked %d\n",
		report.Governance.CommandsPlanned,
		report.Governance.CommandsAllowed,
		report.Governance.CommandsBlocked)
	fmt.Fprintf(&b, "- Permission blocks: %d\n\n", report.Governance.PermissionBlocks)

	writeFindingSection(&b, "Findings", report.Findings)
	writeFindingSection(&b, "Needs Human Review", report.Warnings)

	fmt.Fprintf(&b, "## Governance\n\n")
	writeDecisionLines(&b, "Filter decisions", report.Governance.FilterDecisions)
	writeDecisionLines(&b, "Permission decisions", report.Governance.PermissionDecisions)

	fmt.Fprintf(&b, "## Sandbox\n\n")
	if len(report.Governance.SandboxRuns) == 0 {
		fmt.Fprintf(&b, "No sandbox commands were run.\n\n")
	} else {
		for _, run := range report.Governance.SandboxRuns {
			status := "ok"
			if sandboxRunNeedsWarning(run) {
				status = "attention"
			}
			fmt.Fprintf(&b, "- `%s`: %s, exit %d, %d ms\n",
				run.Command, status, run.ExitCode, run.DurationMS)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Metrics\n\n")
	keys := sortedStringKeys(report.Metrics.ExceptionCounts)
	fmt.Fprintf(&b, "- Sandbox duration: %d ms\n", report.Metrics.SandboxDurationMS)
	fmt.Fprintf(&b, "- Redactions: %d\n", report.Metrics.Redactions)
	fmt.Fprintf(&b, "- Suppressed matches: %d\n", report.Metrics.SuppressedMatches)
	for _, key := range keys {
		fmt.Fprintf(&b, "- %s: %d\n", key, report.Metrics.ExceptionCounts[key])
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Reports\n\n")
	fmt.Fprintf(&b, "- JSON: `%s`\n", report.ReportPaths.JSON)
	fmt.Fprintf(&b, "- Markdown: `%s`\n", report.ReportPaths.Markdown)
	return b.String()
}

func writeFindingSection(b *strings.Builder, title string, findings []reviewFinding) {
	fmt.Fprintf(b, "## %s\n\n", title)
	if len(findings) == 0 {
		fmt.Fprintf(b, "None.\n\n")
		return
	}
	for _, finding := range findings {
		fmt.Fprintf(b, "- `%s` %s:%d %s\n",
			finding.Severity, finding.File, finding.Line, finding.Title)
		if finding.Recommendation != "" {
			fmt.Fprintf(b, "  Recommendation: %s\n", finding.Recommendation)
		}
	}
	fmt.Fprintf(b, "\n")
}

func writeDecisionLines(b *strings.Builder, title string, decisions []governanceDecision) {
	fmt.Fprintf(b, "### %s\n\n", title)
	if len(decisions) == 0 {
		fmt.Fprintf(b, "None.\n\n")
		return
	}
	for _, decision := range decisions {
		if decision.Reason == "" {
			fmt.Fprintf(b, "- `%s`: `%s`\n", decision.Command, decision.Decision)
			continue
		}
		fmt.Fprintf(b, "- `%s`: `%s` - %s\n", decision.Command, decision.Decision, decision.Reason)
	}
	fmt.Fprintf(b, "\n")
}

func sortedStringKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return map[string]int{}
	}
	clone := make(map[string]int, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
