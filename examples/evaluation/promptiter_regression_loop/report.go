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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func writeReports(outputDir string, report *OptimizationReport) error {
	if report == nil {
		return fmt.Errorf("report is nil")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json report: %w", err)
	}
	prefix := reportPrefix(report)
	if err := os.WriteFile(filepath.Join(outputDir, prefix+"optimization_report.json"), append(payload, '\n'), 0o600); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, prefix+"optimization_report.md"), []byte(RenderMarkdownReport(report)), 0o600); err != nil {
		return fmt.Errorf("write markdown report: %w", err)
	}
	return nil
}

func reportPrefix(report *OptimizationReport) string {
	if report == nil || report.Mode == "" {
		return ""
	}
	return report.Mode + "_"
}

// RenderMarkdownReport renders a human-readable audit report.
func RenderMarkdownReport(report *OptimizationReport) string {
	var b bytes.Buffer
	decision := "REJECT"
	if report.Gate.Accepted {
		decision = "ACCEPT"
	}
	fmt.Fprintf(&b, "# PromptIter Regression Loop Report\n\n")
	fmt.Fprintf(&b, "- Run ID: `%s`\n", report.RunID)
	fmt.Fprintf(&b, "- App: `%s`\n", report.AppName)
	fmt.Fprintf(&b, "- Mode: `%s`\n", report.Mode)
	fmt.Fprintf(&b, "- Data source: `%s`\n", report.DataSource)
	fmt.Fprintf(&b, "- Decision: **%s**\n", decision)
	fmt.Fprintf(&b, "- Target surface: `%s`\n", report.TargetSurfaceID)
	fmt.Fprintf(&b, "- Engine: `%s` (`%s`)\n\n", report.FakeEngine.Name, report.FakeEngine.Model)

	fmt.Fprintf(&b, "## Score Summary\n\n")
	fmt.Fprintf(&b, "| Split | Baseline | Candidate | Delta |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|\n")
	fmt.Fprintf(&b, "| Train | %.4f | %.4f | %.4f |\n",
		report.BaselineTrain.OverallScore,
		report.Candidate.TrainEvaluation.OverallScore,
		report.Candidate.TrainEvaluation.OverallScore-report.BaselineTrain.OverallScore,
	)
	fmt.Fprintf(&b, "| Validation | %.4f | %.4f | %.4f |\n\n",
		report.Delta.BaselineScore,
		report.Delta.CandidateScore,
		report.Delta.ScoreDelta,
	)

	fmt.Fprintf(&b, "## Gate Decision\n\n")
	for _, reason := range report.Gate.Reasons {
		fmt.Fprintf(&b, "- %s\n", reason)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Validation Case Delta\n\n")
	fmt.Fprintf(&b, "| Case | Critical | Baseline | Candidate | Delta | Transition |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---|\n")
	for _, evalCase := range report.Delta.Cases {
		fmt.Fprintf(&b, "| `%s` | %t | %.4f | %.4f | %.4f | %s |\n",
			evalCase.CaseID,
			evalCase.Critical,
			evalCase.BaselineScore,
			evalCase.CandidateScore,
			evalCase.ScoreDelta,
			evalCase.Transition,
		)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Validation Output Evidence\n\n")
	fmt.Fprintf(&b, "| Case | Baseline actual | Baseline tools | Candidate actual | Candidate tools |\n")
	fmt.Fprintf(&b, "|---|---|---:|---|---:|\n")
	baselineCases := indexCasesByID(report.BaselineValidation.Cases)
	candidateCases := indexCasesByID(report.Candidate.ValidationEvaluation.Cases)
	for _, evalCase := range report.Delta.Cases {
		baseline := baselineCases[evalCase.CaseID]
		candidate := candidateCases[evalCase.CaseID]
		fmt.Fprintf(&b, "| `%s` | %s | %d | %s | %d |\n",
			evalCase.CaseID,
			markdownCell(messageText(baseline.Actual.FinalResponse), 140),
			len(baseline.Actual.Tools),
			markdownCell(messageText(candidate.Actual.FinalResponse), 140),
			len(candidate.Actual.Tools),
		)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Failure Attribution\n\n")
	writeFailureMap(&b, "Train", report.FailureAttribution.Train)
	writeFailureMap(&b, "Validation", report.FailureAttribution.Validation)
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Audit Summary\n\n")
	fmt.Fprintf(&b, "- Candidate: `%s`\n", report.Candidate.ID)
	fmt.Fprintf(&b, "- Calls: `%d`\n", report.Cost.TotalCalls)
	fmt.Fprintf(&b, "- Estimated cost: `$%.6f`\n", report.Cost.EstimatedUSD)
	fmt.Fprintf(&b, "- Duration: `%d ms`\n", report.Latency.DurationMs)
	fmt.Fprintf(&b, "- Seed: `%d`\n", report.Seed)
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "The candidate is not automatically safe to publish unless the gate decision is ACCEPT.\n")
	return b.String()
}

func indexCasesByID(cases []CaseResult) map[string]CaseResult {
	index := make(map[string]CaseResult, len(cases))
	for _, evalCase := range cases {
		index[evalCase.CaseID] = evalCase
	}
	return index
}

func markdownCell(value string, limit int) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return "_empty_"
	}
	if limit > 0 && len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}

func writeFailureMap(b *bytes.Buffer, title string, counts map[string]int) {
	fmt.Fprintf(b, "### %s\n\n", title)
	if len(counts) == 0 {
		fmt.Fprintf(b, "- none\n\n")
		return
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(b, "- `%s`: %d\n", key, counts[key])
	}
	fmt.Fprintf(b, "\n")
}

func outputDir(input *LoadedInput) string {
	return resolvePath(input.ConfigDir, strings.TrimSpace(input.Config.OutputDir))
}
