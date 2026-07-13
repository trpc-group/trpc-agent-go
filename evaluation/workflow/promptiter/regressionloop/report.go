//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriteReports writes JSON and Markdown reports to disk.
func WriteReports(report *Report, jsonPath, markdownPath string) error {
	if report == nil {
		return fmt.Errorf("report is nil")
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		return fmt.Errorf("create json report dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(markdownPath), 0o755); err != nil {
		return fmt.Errorf("create markdown report dir: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	if err := os.WriteFile(markdownPath, []byte(MarkdownReport(report)), 0o644); err != nil {
		return fmt.Errorf("write markdown report: %w", err)
	}
	return nil
}

// MarkdownReport renders a human-readable audit report.
func MarkdownReport(report *Report) string {
	var buf bytes.Buffer
	decision := "REJECT"
	if report.GateDecision.Accepted {
		decision = "ACCEPT"
	}
	fmt.Fprintf(&buf, "# Optimization Report\n\n")
	fmt.Fprintf(&buf, "Decision: **%s**\n\n", decision)
	fmt.Fprintf(&buf, "App: %s\n\n", markdownInlineCode(report.Run.AppName))
	fmt.Fprintf(&buf, "Seed: `%d`\n\n", report.Run.Seed)
	fmt.Fprintf(&buf, "Runner: `%s`\n\n", report.Run.Runner.Mode)
	fmt.Fprintf(&buf, "Duration: `%dms`\n\n", report.Run.DurationMS)
	fmt.Fprintf(&buf, "## Score Summary\n\n")
	fmt.Fprintf(&buf, "| Phase | Train | Validation |\n")
	fmt.Fprintf(&buf, "| --- | ---: | ---: |\n")
	fmt.Fprintf(&buf, "| Baseline | %.2f | %.2f |\n", report.Baseline.Train.Score, report.Baseline.Validation.Score)
	for _, candidate := range report.Candidates {
		fmt.Fprintf(&buf, "| Candidate %d | %.2f | %.2f |\n", candidate.Round, candidate.Train.Score, candidate.Validation.Score)
	}
	fmt.Fprintf(&buf, "\n## Gate Decision\n\n")
	fmt.Fprintf(&buf, "- Accepted: `%t`\n", report.GateDecision.Accepted)
	fmt.Fprintf(&buf, "- Validation score delta: `%.2f`\n", report.GateDecision.ScoreDelta)
	for _, reason := range report.GateDecision.Reasons {
		fmt.Fprintf(&buf, "- Reason: %s\n", markdownText(reason))
	}
	fmt.Fprintf(&buf, "\n## Validation Delta\n\n")
	fmt.Fprintf(&buf, "| Case | Baseline | Candidate | Delta | Transition | New hard fail | Critical regression |\n")
	fmt.Fprintf(&buf, "| --- | ---: | ---: | ---: | --- | --- | --- |\n")
	for _, delta := range report.Delta.Cases {
		fmt.Fprintf(&buf, "| %s | %.2f | %.2f | %.2f | `%s` | `%t` | `%t` |\n",
			markdownTableCell(delta.EvalID),
			delta.BaselineScore,
			delta.CandidateScore,
			delta.ScoreDelta,
			delta.Transition,
			delta.NewHardFail,
			delta.CriticalRegression,
		)
	}
	fmt.Fprintf(&buf, "\n## Failure Attribution\n\n")
	for _, key := range sortedStatKeys(report.FailureAttributionStats) {
		fmt.Fprintf(&buf, "- `%s`: %d\n", key, report.FailureAttributionStats[key])
	}
	fmt.Fprintf(&buf, "\n## Candidate Rounds\n\n")
	for _, candidate := range report.Candidates {
		fmt.Fprintf(&buf, "### Round %d\n\n", candidate.Round)
		fmt.Fprintf(&buf, "- Accepted: `%t`\n", candidate.GateDecision.Accepted)
		fmt.Fprintf(&buf, "- Prompt:\n\n%s\n", markdownCodeBlock(candidate.Prompt))
		for _, reason := range candidate.GateDecision.Reasons {
			fmt.Fprintf(&buf, "- Gate reason: %s\n", markdownText(reason))
		}
		fmt.Fprintf(&buf, "\n")
	}
	fmt.Fprintf(&buf, "## Cost and Latency\n\n")
	fmt.Fprintf(&buf, "- Calls: `%d`\n", report.CostSummary.Calls)
	fmt.Fprintf(&buf, "- Estimated cost: `%.4f`\n", report.CostSummary.EstimatedCost)
	fmt.Fprintf(&buf, "- Total latency: `%dms`\n", report.LatencySummary.TotalMS)
	fmt.Fprintf(&buf, "\n## Artifacts\n\n")
	for _, artifact := range report.Artifacts {
		fmt.Fprintf(&buf, "- %s\n", markdownInlineCode(artifact))
	}
	return buf.String()
}

func markdownText(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "`", "\\`")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	return strings.ReplaceAll(value, "\n", "<br>")
}

func markdownTableCell(value string) string {
	return markdownText(value)
}

func markdownInlineCode(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	fence := strings.Repeat("`", longestBacktickRun(value)+1)
	if len(fence) < 1 {
		fence = "`"
	}
	return fence + value + fence
}

func markdownCodeBlock(value string) string {
	fenceLength := longestBacktickRun(value) + 1
	if fenceLength < 3 {
		fenceLength = 3
	}
	fence := strings.Repeat("`", fenceLength)
	return fence + "text\n" + value + "\n" + fence
}

func longestBacktickRun(value string) int {
	longest := 0
	current := 0
	for _, r := range value {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	return longest
}

// BuildAttributionStats counts categories across all report evaluations.
func BuildAttributionStats(baseline EvaluationPair, candidates []CandidateRound) map[string]int {
	stats := map[string]int{}
	addEvaluationStats(stats, baseline.Train)
	addEvaluationStats(stats, baseline.Validation)
	for _, candidate := range candidates {
		addEvaluationStats(stats, candidate.Train)
		addEvaluationStats(stats, candidate.Validation)
	}
	return stats
}

// SumCost returns total cost across baseline and candidates.
func SumCost(baseline EvaluationPair, candidates []CandidateRound) CostSummary {
	total := CostSummary{}
	addCost(&total, baseline.Train.Cost)
	addCost(&total, baseline.Validation.Cost)
	for _, candidate := range candidates {
		addCost(&total, candidate.Cost)
	}
	return total
}

// SumLatency returns total latency across baseline and candidates.
func SumLatency(baseline EvaluationPair, candidates []CandidateRound) LatencySummary {
	total := LatencySummary{}
	total.TotalMS += baseline.Train.Latency.TotalMS + baseline.Validation.Latency.TotalMS
	for _, candidate := range candidates {
		total.TotalMS += candidate.Latency.TotalMS
	}
	return total
}

func addEvaluationStats(stats map[string]int, summary EvaluationSummary) {
	for _, c := range summary.Cases {
		for _, attribution := range c.Attributions {
			stats[string(attribution.Category)]++
		}
	}
}

func addCost(total *CostSummary, cost CostSummary) {
	total.Calls += cost.Calls
	total.EstimatedCost += cost.EstimatedCost
}

func sortedStatKeys(stats map[string]int) []string {
	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
