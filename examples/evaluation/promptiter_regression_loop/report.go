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

func WriteReports(report *OptimizationReport, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "optimization_report.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outputDir, "optimization_report.md"), []byte(MarkdownReport(report)), 0o644)
}

func MarkdownReport(r *OptimizationReport) string {
	var b strings.Builder
	b.WriteString("# Prompt Optimization Regression Report\n\n")
	fmt.Fprintf(&b, "- **Decision:** %s\n", decision(r.Accepted))
	fmt.Fprintf(&b, "- **Selected candidate:** %s\n", valueOr(r.SelectedCandidate, "none"))
	fmt.Fprintf(&b, "- **Baseline train score:** %.3f\n", r.BaselineTrain.OverallScore)
	fmt.Fprintf(&b, "- **Baseline validation score:** %.3f\n", r.BaselineValidation.OverallScore)
	fmt.Fprintf(&b, "- **Runtime:** %d ms\n- **Seed:** %d\n- **Engine:** %s (%s)\n\n", r.DurationMS, r.Seed, r.Engine.Type, r.Engine.Model)
	b.WriteString("## Reproducibility configuration\n\n")
	fmt.Fprintf(&b, "- Pass threshold: %.3f\n", r.Metrics.PassThreshold)
	fmt.Fprintf(&b, "- Metric weights (response / tool / format): %.3f / %.3f / %.3f\n", r.Metrics.ResponseWeight, r.Metrics.ToolWeight, r.Metrics.FormatWeight)
	fmt.Fprintf(&b, "- Minimum validation gain: %.3f\n", r.Gate.MinValidationGain)
	fmt.Fprintf(&b, "- No new hard fails: %t\n", r.Gate.NoNewHardFails)
	fmt.Fprintf(&b, "- Critical cases: %s\n", valueOr(strings.Join(r.Gate.CriticalCaseIDs, ", "), "none"))
	fmt.Fprintf(&b, "- Maximum cost increase: %s\n", optionalFloat(r.Gate.MaxCostIncrease))
	fmt.Fprintf(&b, "- Maximum tool calls: %s\n\n", optionalInt(r.Gate.MaxToolCalls))
	b.WriteString("## Failure attribution\n\n| Category | Count |\n|---|---:|\n")
	keys := make([]string, 0, len(r.AttributionCounts))
	for key := range r.AttributionCounts {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&b, "| %s | %d |\n", key, r.AttributionCounts[Attribution(key)])
	}
	b.WriteString("\n## Candidate rounds\n")
	for _, round := range r.Rounds {
		fmt.Fprintf(&b, "\n### Round %d: %s\n\n", round.Round, round.CandidateID)
		fmt.Fprintf(&b, "- Train score: %.3f\n- Validation score: %.3f\n- Validation delta: %+.3f\n- Newly passed / failed: %d / %d\n- Gate: %s — %s\n- Cost: %.3f; tool calls: %d; latency: %d ms\n\n", round.Train.OverallScore, round.Validation.OverallScore, round.Delta.ScoreDelta, round.Delta.NewlyPassed, round.Delta.NewlyFailed, decision(round.Gate.Accepted), strings.Join(round.Gate.Reasons, "; "), round.Validation.TotalCost, round.Validation.ToolCalls, round.Validation.LatencyMS)
		b.WriteString("| Case | Baseline | Candidate | Delta | Change |\n|---|---:|---:|---:|---|\n")
		for _, c := range round.Delta.Cases {
			change := "unchanged"
			if c.NewlyPassed {
				change = "new pass"
			}
			if c.NewlyFailed {
				change = "new fail"
			}
			fmt.Fprintf(&b, "| %s | %.3f | %.3f | %+.3f | %s |\n", c.CaseID, c.BaselineScore, c.CandidateScore, c.ScoreDelta, change)
		}
	}
	b.WriteString("\n## Final decision\n\n")
	for _, reason := range r.DecisionReasons {
		fmt.Fprintf(&b, "- %s\n", reason)
	}
	if r.Accepted {
		b.WriteString("\nThe candidate generalizes on the held-out validation set and is safe to consider for source-prompt write-back after human review.\n")
	} else {
		b.WriteString("\nNo prompt should be written back. Keep the baseline and inspect the rejected-round audit records.\n")
	}
	return b.String()
}

func decision(accepted bool) string {
	if accepted {
		return "ACCEPT"
	}
	return "REJECT"
}
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func optionalFloat(value *float64) string {
	if value == nil {
		return "disabled"
	}
	return fmt.Sprintf("%.3f", *value)
}

func optionalInt(value *int) string {
	if value == nil {
		return "disabled"
	}
	return fmt.Sprint(*value)
}
