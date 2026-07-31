//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
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

func writeReports(outputDir string, report *optimizationReport) error {
	if report == nil {
		return fmt.Errorf("optimization report is nil")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal optimization report: %w", err)
	}
	jsonData = append(jsonData, '\n')
	if err := os.WriteFile(filepath.Join(outputDir, "optimization_report.json"), jsonData, 0o644); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(outputDir, "optimization_report.md"),
		[]byte(markdownReport(report)),
		0o644,
	); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

func markdownReport(report *optimizationReport) string {
	var output strings.Builder
	fmt.Fprintln(&output, "# PromptIter Regression Report")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Mode: `%s`\n", report.Metadata.Mode)
	fmt.Fprintf(&output, "- Model: `%s`\n", report.Metadata.Model.Name)
	fmt.Fprintf(&output, "- Seed: `%d`\n", report.Metadata.Seed)
	fmt.Fprintf(&output, "- Decision: **%s**\n", report.Decision)
	fmt.Fprintf(&output, "- Accepted candidate: `%s`\n", valueOrNone(report.AcceptedCandidateID))
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Baseline")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "Train score: **%.3f**; validation score: **%.3f**.\n", report.Baseline.Train.Score, report.Baseline.Validation.Score)
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Candidate rounds")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "| Round | Candidate | Train | Validation | Accepted | Reasons |")
	fmt.Fprintln(&output, "|---:|---|---:|---:|:---:|---|")
	for _, round := range report.Rounds {
		fmt.Fprintf(
			&output, "| %d | `%s` | %.3f | %.3f | %t | %s |\n",
			round.Round, round.CandidateID, round.Train.Score, round.Validation.Score,
			round.Gate.Accepted, strings.Join(round.Gate.Reasons, "; "),
		)
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Validation deltas")
	for _, round := range report.Rounds {
		fmt.Fprintln(&output)
		fmt.Fprintf(&output, "### Round %d — `%s`\n\n", round.Round, round.CandidateID)
		for _, delta := range round.ValidationDelta {
			fmt.Fprintf(&output, "- `%s`: %s (%.3f → %.3f)\n", delta.CaseID, delta.Status, delta.BaselineScore, delta.CandidateScore)
		}
		categories := make([]string, 0, len(round.AttributionSummary))
		for category := range round.AttributionSummary {
			categories = append(categories, category)
		}
		sort.Strings(categories)
		if len(categories) > 0 {
			fmt.Fprintln(&output, "- Failure attribution:")
			for _, category := range categories {
				fmt.Fprintf(&output, "  - `%s`: %d\n", category, round.AttributionSummary[category])
			}
		}
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Accepted prompt")
	fmt.Fprintln(&output)
	fence := codeFence(report.AcceptedPrompt)
	fmt.Fprintf(&output, "%stext\n%s\n%s\n", fence, report.AcceptedPrompt, fence)
	return output.String()
}

// codeFence returns a backtick fence long enough to enclose content verbatim.
func codeFence(content string) string {
	longest, current := 0, 0
	for _, char := range content {
		if char == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	if longest < 3 {
		return "```"
	}
	return strings.Repeat("`", longest+1)
}

func valueOrNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}
