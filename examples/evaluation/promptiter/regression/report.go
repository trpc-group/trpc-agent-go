//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	reportJSONFile = "optimization_report.json"
	reportMDFile   = "optimization_report.md"
)

func writeReports(outputDir string, report optimizationReport) error {
	if outputDir == "" {
		return fmt.Errorf("output directory is empty")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON report: %w", err)
	}
	jsonData = append(jsonData, '\n')
	if err := os.WriteFile(filepath.Join(outputDir, reportJSONFile), jsonData, 0o644); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	markdown, err := renderMarkdown(report)
	if err != nil {
		return fmt.Errorf("render Markdown report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, reportMDFile), []byte(markdown), 0o644); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

func renderMarkdown(report optimizationReport) (string, error) {
	if report.SchemaVersion == "" {
		return "", fmt.Errorf("report schema version is empty")
	}
	var builder strings.Builder
	decision := "REJECT"
	if report.GateDecision.Accepted {
		decision = "ACCEPT"
	}
	fmt.Fprintln(&builder, "# Prompt Optimization Report")
	fmt.Fprintln(&builder)
	fmt.Fprintf(&builder, "- Run: `%s`\n", markdownCell(report.RunID))
	fmt.Fprintf(&builder, "- Decision: **%s**\n", decision)
	fmt.Fprintf(&builder, "- Candidate: `%s` (round %d)\n",
		markdownCell(report.Candidate.CandidateID), report.Candidate.Round)
	fmt.Fprintf(&builder, "- Engine: `%s`; model: `%s`; seed: `%d`\n",
		markdownCell(report.Runtime.Engine),
		markdownCell(report.Runtime.Model.Name),
		report.Runtime.Seed,
	)
	fmt.Fprintf(&builder, "- Train score: `%.4f` -> `%.4f`\n",
		report.Baseline.Train.Score,
		report.Candidate.Train.Score,
	)
	fmt.Fprintf(&builder, "- Validation score: `%.4f` -> `%.4f` (`%+.4f`)\n",
		report.Baseline.Validation.Score,
		report.Candidate.Validation.Score,
		report.Delta.ScoreDelta,
	)
	fmt.Fprintln(&builder)

	fmt.Fprintln(&builder, "## Gate Decision")
	fmt.Fprintln(&builder)
	for _, reason := range report.GateDecision.Reasons {
		fmt.Fprintf(&builder, "- %s\n", reason)
	}
	fmt.Fprintln(&builder)
	fmt.Fprintln(&builder, "| Check | Passed | Detail |")
	fmt.Fprintln(&builder, "| --- | --- | --- |")
	for _, check := range report.GateDecision.Checks {
		fmt.Fprintf(&builder, "| %s | %t | %s |\n",
			markdownCell(check.Name), check.Passed, markdownCell(check.Detail))
	}
	fmt.Fprintln(&builder)

	fmt.Fprintln(&builder, "## Validation Delta")
	fmt.Fprintln(&builder)
	fmt.Fprintln(&builder, "| Case | Baseline | Candidate | Delta | Classification |")
	fmt.Fprintln(&builder, "| --- | ---: | ---: | ---: | --- |")
	for _, item := range report.Delta.Cases {
		fmt.Fprintf(&builder, "| %s | %.4f | %.4f | %+.4f | %s |\n",
			markdownCell(item.CaseID),
			item.BaselineScore,
			item.CandidateScore,
			item.ScoreDelta,
			item.Class,
		)
	}
	fmt.Fprintln(&builder)
	fmt.Fprintf(&builder,
		"Newly passed: **%d**. Newly failed: **%d**. Improved: **%d**. Regressed: **%d**.\n",
		report.Delta.NewlyPassed,
		report.Delta.NewlyFailed,
		report.Delta.Improved,
		report.Delta.Regressed,
	)
	fmt.Fprintln(&builder)

	fmt.Fprintln(&builder, "## Optimization Rounds")
	fmt.Fprintln(&builder)
	fmt.Fprintln(&builder, "| Round | Candidate | Train | Validation | Delta | Decision | Reasons |")
	fmt.Fprintln(&builder, "| ---: | --- | ---: | ---: | ---: | --- | --- |")
	for _, round := range report.Rounds {
		roundDecision := "rejected"
		if round.Decision.Accepted {
			roundDecision = "accepted"
		}
		fmt.Fprintf(&builder, "| %d | %s | %.4f | %.4f | %+.4f | %s | %s |\n",
			round.Round,
			markdownCell(round.CandidateID),
			round.Train.Score,
			round.Validation.Score,
			round.Delta.ScoreDelta,
			roundDecision,
			markdownCell(strings.Join(round.Decision.Reasons, "; ")),
		)
	}
	fmt.Fprintln(&builder)

	fmt.Fprintln(&builder, "## Failure Attribution")
	fmt.Fprintln(&builder)
	writeAttributionTable(&builder, report.FailureAttribution)
	fmt.Fprintln(&builder)

	fmt.Fprintln(&builder, "## Cost and Latency")
	fmt.Fprintln(&builder)
	fmt.Fprintf(&builder,
		"Total: %d model calls, %d tool calls, %d tokens, estimated cost `$%.6f`, latency `%d ms`.\n",
		report.CostLatency.Total.ModelCalls,
		report.CostLatency.Total.ToolCalls,
		report.CostLatency.Total.TotalTokens,
		report.CostLatency.Total.EstimatedCostUSD,
		report.CostLatency.TotalLatencyMillis,
	)
	fmt.Fprintln(&builder)

	fmt.Fprintln(&builder, "## Recommended Prompt")
	fmt.Fprintln(&builder)
	fmt.Fprintln(&builder, "```text")
	recommendedPrompt := report.Baseline.Prompt
	if report.GateDecision.Accepted {
		recommendedPrompt = report.Candidate.Prompt
	}
	fmt.Fprintln(&builder, recommendedPrompt)
	fmt.Fprintln(&builder, "```")
	return builder.String(), nil
}

func writeAttributionTable(builder *strings.Builder, summary attributionSummary) {
	categories := make(map[failureCategory]struct{})
	for category := range summary.Baseline {
		categories[category] = struct{}{}
	}
	for category := range summary.Candidate {
		categories[category] = struct{}{}
	}
	ordered := make([]string, 0, len(categories))
	for category := range categories {
		ordered = append(ordered, string(category))
	}
	sort.Strings(ordered)
	fmt.Fprintln(builder, "| Category | Baseline | Candidate |")
	fmt.Fprintln(builder, "| --- | ---: | ---: |")
	for _, category := range ordered {
		key := failureCategory(category)
		fmt.Fprintf(builder, "| %s | %d | %d |\n",
			markdownCell(category),
			summary.Baseline[key],
			summary.Candidate[key],
		)
	}
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "\n", " ")
}
