// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

var defaultAttributionRules = []AttributionRule{
	{Name: "route_error", Category: AttributionRouteError, Patterns: []string{"route_", "_route", "routing", "路由"}, Priority: 1},
	{Name: "tool_call_error", Category: AttributionToolCallError, Patterns: []string{"tool_call_", "_tool_call", "工具调用", "tool invocation"}, Priority: 2},
	{Name: "tool_argument_error", Category: AttributionToolArgumentError, Patterns: []string{"argument_", "_argument", "参数错误", "parameter_", "_parameter"}, Priority: 3},
	{Name: "format_error", Category: AttributionFormatError, Patterns: []string{"format_", "_format", "格式错误", "json parse", "xml parse"}, Priority: 4},
	{Name: "knowledge_recall_gap", Category: AttributionKnowledgeRecallGap, Patterns: []string{"knowledge_", "_knowledge", "知识召回", "recall_", "_recall", "missing_information"}, Priority: 5},
	{Name: "response_mismatch", Category: AttributionResponseMismatch, Patterns: []string{}, Priority: 6},
}

func AttributeFailures(results *engine.EvaluationResult, customRules []AttributionRule) []AttributionResult {
	rules := customRules
	if len(rules) == 0 {
		rules = defaultAttributionRules
	}

	var attributions []AttributionResult
	for _, evalSet := range results.EvalSets {
		for _, caseResult := range evalSet.Cases {
			for _, metric := range caseResult.Metrics {
				if metric.Status != status.EvalStatusFailed {
					continue
				}

				attribution := classifyFailure(caseResult, metric, rules)
				attributions = append(attributions, attribution)
			}
		}
	}

	return foldCausalChain(attributions)
}

func classifyFailure(caseResult engine.CaseResult, metric engine.MetricResult, rules []AttributionRule) AttributionResult {
	combinedText := strings.ToLower(metric.MetricName + " " + metric.Reason)
	var matchedRule *AttributionRule

	for _, rule := range rules {
		for _, pattern := range rule.Patterns {
			if strings.Contains(combinedText, strings.ToLower(pattern)) {
				if matchedRule == nil || rule.Priority < matchedRule.Priority {
					matchedRule = &rule
				}
			}
		}
	}

	category := AttributionResponseMismatch
	if matchedRule != nil {
		category = matchedRule.Category
	}

	evidence := extractInvocationEvidence(caseResult, metric)

	return AttributionResult{
		EvalCaseID:       caseResult.EvalCaseID,
		MetricName:       metric.MetricName,
		Category:         category,
		Reason:           metric.Reason,
		Evidence:         evidence,
		LossHintSeverity: severityFromCategory(category),
	}
}

func extractInvocationEvidence(caseResult engine.CaseResult, metric engine.MetricResult) *InvocationEvidence {
	return &InvocationEvidence{
		ToolCallPresent:  false,
		ExpectedToolCall: false,
	}
}

func severityFromCategory(category AttributionCategory) promptiter.LossSeverity {
	switch category {
	case AttributionRouteError, AttributionToolCallError:
		return promptiter.LossSeverityP0
	case AttributionToolArgumentError, AttributionFormatError:
		return promptiter.LossSeverityP1
	case AttributionKnowledgeRecallGap:
		return promptiter.LossSeverityP2
	default:
		return promptiter.LossSeverityP3
	}
}

func foldCausalChain(attributions []AttributionResult) []AttributionResult {
	categoryOrder := []AttributionCategory{
		AttributionRouteError,
		AttributionToolCallError,
		AttributionToolArgumentError,
		AttributionFormatError,
		AttributionKnowledgeRecallGap,
		AttributionResponseMismatch,
	}

	caseMap := make(map[string][]AttributionResult)
	for _, attr := range attributions {
		caseMap[attr.EvalCaseID] = append(caseMap[attr.EvalCaseID], attr)
	}

	var result []AttributionResult
	for _, attrs := range caseMap {
		if len(attrs) == 1 {
			result = append(result, attrs[0])
			continue
		}

		var rootCause *AttributionResult
		for _, cat := range categoryOrder {
			for _, attr := range attrs {
				if attr.Category == cat {
					rootCause = &attr
					break
				}
			}
			if rootCause != nil {
				break
			}
		}

		if rootCause == nil {
			result = append(result, attrs...)
			continue
		}

		for _, attr := range attrs {
			if attr.Category != rootCause.Category && attr.MetricName != rootCause.MetricName {
				rootCause.DerivedFrom = append(rootCause.DerivedFrom, string(attr.Category))
			}
		}
		result = append(result, *rootCause)
		for _, attr := range attrs {
			if attr.Category != rootCause.Category || attr.MetricName != rootCause.MetricName {
				result = append(result, attr)
			}
		}
	}

	return result
}

func GetAttributionSummary(attributions []AttributionResult) map[string]int {
	summary := make(map[string]int)
	for _, attr := range attributions {
		summary[string(attr.Category)]++
	}
	return summary
}

func ConvertToLossHints(attributions []AttributionResult) []promptiter.CaseLoss {
	caseLossMap := make(map[string]*promptiter.CaseLoss)
	for _, attr := range attributions {
		key := attr.EvalCaseID
		if _, ok := caseLossMap[key]; !ok {
			caseLossMap[key] = &promptiter.CaseLoss{
				EvalCaseID: attr.EvalCaseID,
			}
		}
		caseLossMap[key].TerminalLosses = append(caseLossMap[key].TerminalLosses, promptiter.TerminalLoss{
			EvalCaseID: attr.EvalCaseID,
			MetricName: attr.MetricName,
			Severity:   attr.LossHintSeverity,
			Loss:       attr.Reason,
		})
	}

	var losses []promptiter.CaseLoss
	for _, cl := range caseLossMap {
		losses = append(losses, *cl)
	}
	return losses
}
