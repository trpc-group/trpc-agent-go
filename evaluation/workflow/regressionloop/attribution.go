//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regressionloop

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// metricNameRules maps evaluator name substrings to failure categories.
// The first match wins; order matters.
var metricNameRules = []struct {
	Contains string
	Category FailureCategory
}{
	{"tool_trajectory", FailureToolCallError},
	{"final_response", FailureFinalResponseMismatch},
	{"hallucination", FailureHallucination},
	{"knowledge_recall", FailureKnowledgeRecall},
	{"rubric_knowledge", FailureKnowledgeRecall},
}

// reasonKeywordRules maps reason-text keywords to failure categories.
// These are checked only when metric-name matching falls through to
// FailureQualityBelowThreshold or FailureUnknown.
var reasonKeywordRules = []struct {
	Contains string
	Category FailureCategory
}{
	// Tool-related signals.
	{"wrong tool", FailureToolCallError},
	{"wrong function", FailureToolCallError},
	{"did not call", FailureToolCallError},
	{"did not invoke", FailureToolCallError},
	{"missing tool call", FailureToolCallError},
	{"unexpected tool", FailureToolCallError},
	{"incorrect tool", FailureToolCallError},
	{"tool not called", FailureToolCallError},
	{"no tool", FailureToolCallError},

	// Tool argument signals.
	{"wrong argument", FailureToolArgumentError},
	{"incorrect argument", FailureToolArgumentError},
	{"missing argument", FailureToolArgumentError},
	{"invalid argument", FailureToolArgumentError},
	{"wrong parameter", FailureToolArgumentError},
	{"incorrect parameter", FailureToolArgumentError},
	{"malformed argument", FailureToolArgumentError},
	{"bad argument", FailureToolArgumentError},

	// Hallucination signals (must precede format to avoid "information" matching "format").
	{"hallucinat", FailureHallucination},
	{"fabricat", FailureHallucination},
	{"made up", FailureHallucination},
	{"not supported by", FailureHallucination},
	{"not grounded", FailureHallucination},
	{"invented", FailureHallucination},

	// Route / dispatch signals.
	{"wrong agent", FailureRouteError},
	{"wrong route", FailureRouteError},
	{"routed to wrong", FailureRouteError},
	{"incorrect agent", FailureRouteError},
	{"should have been routed", FailureRouteError},
	{"dispatched to wrong", FailureRouteError},

	// Knowledge / recall signals.
	{"knowledge", FailureKnowledgeRecall},
	{"does not know", FailureKnowledgeRecall},
	{"does not recall", FailureKnowledgeRecall},
	{"missing information", FailureKnowledgeRecall},
	{"missing context", FailureKnowledgeRecall},
	{"could not recall", FailureKnowledgeRecall},
	{"lacks knowledge", FailureKnowledgeRecall},
	{"insufficient context", FailureKnowledgeRecall},

	// Format signals (placed after hallucination/knowledge to avoid substring
	// false positives — e.g. "information" contains "format").
	{"invalid json", FailureFormatError},
	{"malformed json", FailureFormatError},
	{"invalid xml", FailureFormatError},
	{"invalid format", FailureFormatError},
	{"output format", FailureFormatError},
	{"response format", FailureFormatError},
	{"wrong format", FailureFormatError},
	{"expected format", FailureFormatError},
	{"too long", FailureFormatError},
	{"too short", FailureFormatError},
	{"length", FailureFormatError},
}

// AttributionInput holds the data needed to classify one failed metric.
type AttributionInput struct {
	EvalSetID  string
	EvalCaseID string
	MetricName string
	Score      float64
	Reason     string
	Status     string
}

// AttributeFailure classifies a single failed metric into a failure category
// using the metric name and failure reason as classification signals.
//
// Classification strategy:
//  1. Match metric name against known evaluator names (tool_trajectory, final_response, etc.)
//  2. If the metric name is a known evaluator, use that category directly.
//  3. Otherwise, scan the reason text for keyword patterns.
//  4. If nothing matches, classify as quality_below_threshold if score > 0, or unknown.
func AttributeFailure(input AttributionInput) FailureAttribution {
	reason := strings.ToLower(input.Reason)
	metricName := strings.ToLower(input.MetricName)

	// Phase 1: Match by metric name (evaluator name).
	for _, rule := range metricNameRules {
		if strings.Contains(metricName, rule.Contains) {
			return FailureAttribution{
				EvalSetID:   input.EvalSetID,
				EvalCaseID:  input.EvalCaseID,
				MetricName:  input.MetricName,
				Category:    rule.Category,
				Reason:      input.Reason,
				Score:       input.Score,
				Explanation: "classified by metric evaluator name: " + input.MetricName,
			}
		}
	}

	// Phase 2: Match by reason keyword patterns.
	for _, rule := range reasonKeywordRules {
		if strings.Contains(reason, rule.Contains) {
			return FailureAttribution{
				EvalSetID:   input.EvalSetID,
				EvalCaseID:  input.EvalCaseID,
				MetricName:  input.MetricName,
				Category:    rule.Category,
				Reason:      input.Reason,
				Score:       input.Score,
				Explanation: "classified by reason keyword: " + rule.Contains,
			}
		}
	}

	// Phase 3: Fallback classification.
	if input.Score > 0 {
		return FailureAttribution{
			EvalSetID:   input.EvalSetID,
			EvalCaseID:  input.EvalCaseID,
			MetricName:  input.MetricName,
			Category:    FailureQualityBelowThreshold,
			Reason:      input.Reason,
			Score:       input.Score,
			Explanation: "score above zero but below threshold; no specific pattern matched",
		}
	}
	return FailureAttribution{
		EvalSetID:   input.EvalSetID,
		EvalCaseID:  input.EvalCaseID,
		MetricName:  input.MetricName,
		Category:    FailureUnknown,
		Reason:      input.Reason,
		Score:       input.Score,
		Explanation: "no classification rule matched",
	}
}

// AttributeFailures classifies all failed metrics from a set of evaluation results.
// It returns a flat list of attributions across all eval sets and cases.
func AttributeFailures(results []CaseEvalResult) []FailureAttribution {
	var attributions []FailureAttribution
	for _, cr := range results {
		for _, m := range cr.Metrics {
			if m.Status != string(status.EvalStatusFailed) {
				continue
			}
			attributions = append(attributions, AttributeFailure(AttributionInput{
				EvalSetID:  cr.EvalSetID,
				EvalCaseID: cr.EvalCaseID,
				MetricName: m.MetricName,
				Score:      m.Score,
				Reason:     m.Reason,
				Status:     m.Status,
			}))
		}
	}
	return attributions
}

// AttributionSummary groups attribution counts by category.
type AttributionSummary struct {
	Category FailureCategory `json:"category"`
	Count    int             `json:"count"`
	Cases    []string        `json:"cases"`
}

// SummarizeAttributions aggregates attributions by category for reporting.
func SummarizeAttributions(attributions []FailureAttribution) []AttributionSummary {
	index := make(map[FailureCategory]*AttributionSummary)
	var order []FailureCategory
	for _, a := range attributions {
		s, ok := index[a.Category]
		if !ok {
			s = &AttributionSummary{Category: a.Category}
			index[a.Category] = s
			order = append(order, a.Category)
		}
		s.Count++
		caseRef := a.EvalSetID + "/" + a.EvalCaseID
		// Deduplicate case references.
		found := false
		for _, existing := range s.Cases {
			if existing == caseRef {
				found = true
				break
			}
		}
		if !found {
			s.Cases = append(s.Cases, caseRef)
		}
	}
	result := make([]AttributionSummary, 0, len(order))
	for _, cat := range order {
		result = append(result, *index[cat])
	}
	return result
}

// CaseEvalResult is a simplified view of one evaluated case for attribution purposes.
// It mirrors the fields from engine.CaseResult that are relevant to failure classification.
type CaseEvalResult struct {
	EvalSetID  string
	EvalCaseID string
	Metrics    []MetricInfo
}

// MetricInfo holds the relevant fields from a metric evaluation result.
type MetricInfo struct {
	MetricName string
	Score      float64
	Status     string
	Reason     string
}
