//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"sort"
	"strings"
)

// FailureCategory classifies why one eval case failed.
type FailureCategory string

const (
	// CategoryFinalResponseMismatch means the final answer content does not match
	// the expected output (rouge / exact / contains failures).
	CategoryFinalResponseMismatch FailureCategory = "final_response_mismatch"
	// CategoryToolCallError means the agent called the wrong tool or failed to call one.
	CategoryToolCallError FailureCategory = "tool_call_error"
	// CategoryToolArgError means the agent called the right tool with wrong arguments.
	CategoryToolArgError FailureCategory = "tool_argument_error"
	// CategoryRouteError means the agent routed the request to the wrong branch/agent.
	CategoryRouteError FailureCategory = "route_error"
	// CategoryFormatError means the output did not conform to a required format or schema.
	CategoryFormatError FailureCategory = "format_error"
	// CategoryKnowledgeRecall means the answer lacked recalled/grounded knowledge.
	CategoryKnowledgeRecall FailureCategory = "knowledge_recall"
	// CategoryOther is the fallback when no signal matches.
	CategoryOther FailureCategory = "other"
)

// CaseAttribution records the failure cause for one eval case.
type CaseAttribution struct {
	EvalCaseID string          `json:"evalCaseId"`
	Category   FailureCategory `json:"category"`
	Reason     string          `json:"reason"`
	Confidence float64         `json:"confidence"`
}

// metricCategoryRule maps metric-name substrings to a failure category.
type metricCategoryRule struct {
	keyword  string
	category FailureCategory
}

// metricNameRules are applied in order against each failed metric name.
var metricNameRules = []metricCategoryRule{
	{"tool_trajectory", CategoryToolCallError},
	{"tool_call", CategoryToolCallError},
	{"trajectory", CategoryToolCallError},
	{"tool_argument", CategoryToolArgError},
	{"tool_arg", CategoryToolArgError},
	{"route", CategoryRouteError},
	{"router", CategoryRouteError},
	{"planner", CategoryRouteError},
	{"format", CategoryFormatError},
	{"json_schema", CategoryFormatError},
	{"schema", CategoryFormatError},
	{"json", CategoryFormatError},
	{"xml", CategoryFormatError},
	{"knowledge", CategoryKnowledgeRecall},
	{"recall", CategoryKnowledgeRecall},
	{"grounding", CategoryKnowledgeRecall},
	{"hallucination", CategoryKnowledgeRecall},
	{"final_response", CategoryFinalResponseMismatch},
	{"finalresponse", CategoryFinalResponseMismatch},
	{"headline", CategoryFinalResponseMismatch},
	{"rouge", CategoryFinalResponseMismatch},
	{"content", CategoryFinalResponseMismatch},
	{"response", CategoryFinalResponseMismatch},
}

// reasonCategoryRules are applied in order against the failure reason text.
var reasonCategoryRules = []metricCategoryRule{
	{"json", CategoryFormatError},
	{"schema", CategoryFormatError},
	{"format", CategoryFormatError},
	{"xml", CategoryFormatError},
	{"parse", CategoryFormatError},
	{"not valid", CategoryFormatError},
	{"tool", CategoryToolCallError},
	{"trajectory", CategoryToolCallError},
	{"argument", CategoryToolArgError},
	{"parameter", CategoryToolArgError},
	{"route", CategoryRouteError},
	{"knowledge", CategoryKnowledgeRecall},
	{"recall", CategoryKnowledgeRecall},
	{"grounding", CategoryKnowledgeRecall},
	{"hallucination", CategoryKnowledgeRecall},
	{"mismatch", CategoryFinalResponseMismatch},
	{"does not match", CategoryFinalResponseMismatch},
	{"does not contain", CategoryFinalResponseMismatch},
	{"not contain", CategoryFinalResponseMismatch},
	{"length", CategoryFinalResponseMismatch},
	{"rouge", CategoryFinalResponseMismatch},
}

// AttributeCase classifies the failure cause of one failed eval case. Passing
// cases are attributed to none and reported as nil.
func AttributeCase(caseScore CaseScore) *CaseAttribution {
	if caseScore.Passed {
		return nil
	}
	var reasonParts []string
	for _, metric := range caseScore.Metrics {
		if metric.Passed {
			continue
		}
		if reason := strings.TrimSpace(metric.Reason); reason != "" {
			reasonParts = append(reasonParts, reason)
		}
		if category, ok := classifyMetricName(metric.MetricName); ok {
			return &CaseAttribution{
				EvalCaseID: caseScore.EvalCaseID,
				Category:   category,
				Reason:     strings.Join(reasonParts, "; "),
				Confidence: 0.9,
			}
		}
	}
	// No metric name signal: fall back to scanning the failure reasons.
	joinedReason := strings.Join(reasonParts, " ")
	if category, ok := classifyReason(joinedReason); ok {
		return &CaseAttribution{
			EvalCaseID: caseScore.EvalCaseID,
			Category:   category,
			Reason:     joinedReason,
			Confidence: 0.7,
		}
	}
	reason := joinedReason
	if reason == "" {
		reason = "no failure detail provided"
	}
	return &CaseAttribution{
		EvalCaseID: caseScore.EvalCaseID,
		Category:   CategoryOther,
		Reason:     reason,
		Confidence: 0.5,
	}
}

// AttributeAll classifies every failed case in an evaluation result and returns
// the attributions in a stable order.
func AttributeAll(cases []CaseScore) []CaseAttribution {
	attributions := make([]CaseAttribution, 0, len(cases))
	for _, caseScore := range cases {
		if attr := AttributeCase(caseScore); attr != nil {
			attributions = append(attributions, *attr)
		}
	}
	return attributions
}

// AttributionDistribution summarizes how often each category occurred.
func AttributionDistribution(attributions []CaseAttribution) map[FailureCategory]int {
	distribution := make(map[FailureCategory]int)
	for _, attr := range attributions {
		distribution[attr.Category]++
	}
	return distribution
}

// classifyMetricName matches a metric name against the category rules.
func classifyMetricName(metricName string) (FailureCategory, bool) {
	lower := strings.ToLower(metricName)
	for _, rule := range metricNameRules {
		if strings.Contains(lower, rule.keyword) {
			return rule.category, true
		}
	}
	return "", false
}

// classifyReason matches a failure reason against the category rules.
func classifyReason(reason string) (FailureCategory, bool) {
	lower := strings.ToLower(reason)
	for _, rule := range reasonCategoryRules {
		if strings.Contains(lower, rule.keyword) {
			return rule.category, true
		}
	}
	return "", false
}

// sortedCategories returns the attribution distribution as a stable ordered slice.
func sortedCategories(distribution map[FailureCategory]int) []struct {
	Category FailureCategory
	Count    int
} {
	out := make([]struct {
		Category FailureCategory
		Count    int
	}, 0, len(distribution))
	for category, count := range distribution {
		out = append(out, struct {
			Category FailureCategory
			Count    int
		}{Category: category, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Category < out[j].Category
	})
	return out
}
