//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"strings"
)

// InsightAggregator produces the cross-case AttributionInsights after per-case
// attribution. With the LLM-cost optimization, aggregation itself is always
// deterministic (pattern counts + a template summary): the natural-language
// summary and the overall fix direction are produced by the Narrator's enhanced
// (LLM) path in a single merged call, avoiding a separate aggregation LLM round
// trip. This keeps the failure counts exact and the number of LLM calls minimal.
type InsightAggregator interface {
	Aggregate(ctx context.Context, failures []CaseFailure) *AttributionInsights
	Method() string
}

// ruleInsightAggregator is the deterministic aggregator: it computes pattern counts
// from the real failures (never trusting an LLM to count) and emits a template
// summary. Always available, even offline.
type ruleInsightAggregator struct{}

func (ruleInsightAggregator) Method() string { return "rule" }

func (ruleInsightAggregator) Aggregate(_ context.Context, failures []CaseFailure) *AttributionInsights {
	total := len(failures)
	patterns := buildPatterns(failures, total)
	summary := ruleInsightSummary(patterns, total)
	return &AttributionInsights{
		Method:   "rule",
		Summary:  summary,
		Patterns: patterns,
	}
}

// buildPatterns computes the per-category failure distribution deterministically
// from the real failures. The example reason is taken from the first failure of
// that category so the report always shows a concrete, real case.
func buildPatterns(failures []CaseFailure, total int) []FailurePattern {
	counts := map[FailureCategory]int{}
	example := map[FailureCategory]string{}
	for _, f := range failures {
		counts[f.Category]++
		if _, ok := example[f.Category]; !ok && strings.TrimSpace(f.Reason) != "" {
			example[f.Category] = f.Reason
		}
	}
	patterns := make([]FailurePattern, 0, len(counts))
	for cat, n := range counts {
		ratio := 0.0
		if total > 0 {
			ratio = float64(n) / float64(total)
		}
		patterns = append(patterns, FailurePattern{
			Category: cat,
			Count:    n,
			Ratio:    ratio,
			Example:  example[cat],
		})
	}
	// Stable order: largest count first, then category name.
	sortPatterns(patterns)
	return patterns
}

func sortPatterns(p []FailurePattern) {
	for i := 0; i < len(p); i++ {
		for j := i + 1; j < len(p); j++ {
			if p[j].Count > p[i].Count || (p[j].Count == p[i].Count && p[j].Category < p[i].Category) {
				p[i], p[j] = p[j], p[i]
			}
		}
	}
}

func ruleInsightSummary(patterns []FailurePattern, total int) string {
	if total == 0 {
		return "无失败 case，归因聚合不适用。"
	}
	if len(patterns) == 1 {
		return fmt.Sprintf("全部 %d 个失败均集中在 %s。", total, patterns[0].Category)
	}
	top := patterns[0]
	return fmt.Sprintf("共 %d 个失败，主要分布在 %s（%d 个，占比 %.0f%%）。",
		total, top.Category, top.Count, top.Ratio*100)
}

// buildInsightAggregator always returns the deterministic aggregator. Kept as a
// factory for symmetry with the other layers; the LLM enhancement for insights
// lives in the Narrator's EnhancedReporter.
func buildInsightAggregator(_ regressionConfig) (InsightAggregator, error) {
	return ruleInsightAggregator{}, nil
}
