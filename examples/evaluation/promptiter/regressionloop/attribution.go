//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"reflect"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type attributionCategory string

const (
	attributionFinalResponseMismatch          attributionCategory = "final_response_mismatch"
	attributionToolCallError                  attributionCategory = "tool_call_error"
	attributionToolParameterError             attributionCategory = "tool_parameter_error"
	attributionRouteError                     attributionCategory = "route_error"
	attributionFormatError                    attributionCategory = "format_error"
	attributionKnowledgeRetrievalInsufficient attributionCategory = "knowledge_retrieval_insufficient"
	attributionRuntimeError                   attributionCategory = "runtime_error"
	attributionEvaluationIncomplete           attributionCategory = "evaluation_incomplete"
	attributionUnclassifiedFailure            attributionCategory = "unclassified_failure"
)

type attribution struct {
	Category   attributionCategory `json:"category"`
	MetricName string              `json:"metricName,omitempty"`
	Evidence   string              `json:"evidence,omitempty"`
}

type caseAttribution struct {
	EvalCaseID string        `json:"evalCaseId"`
	Primary    attribution   `json:"primary"`
	Secondary  []attribution `json:"secondary,omitempty"`
}

func attributeCase(evalCase caseResult) caseAttribution {
	result := caseAttribution{EvalCaseID: evalCase.EvalCaseID}
	if evalCase.ExecutionError != "" {
		result.Primary = attribution{
			Category: attributionRuntimeError,
			Evidence: evalCase.ExecutionError,
		}
		return result
	}

	failed := failedMetrics(evalCase.Metrics)
	selectors := []func(metricResult) (attribution, bool){
		func(metric metricResult) (attribution, bool) {
			return toolAttribution(metric, evalCase.MetricEvidence)
		},
		categorySelector(attributionFormatError, "format", "json", "schema"),
		categorySelector(attributionRouteError, "route", "routing"),
		categorySelector(attributionKnowledgeRetrievalInsufficient, "retrieval", "knowledge"),
		categorySelector(attributionFinalResponseMismatch, "final_response", "final", "response", "quality", "rubric"),
		incompleteAttribution,
	}
	for _, selector := range selectors {
		for _, metric := range failed {
			if selected, ok := selector(metric); ok {
				result.Primary = selected
				result.Secondary = secondaryAttributions(failed, selected)
				return result
			}
		}
	}
	result.Primary = attribution{Category: attributionUnclassifiedFailure}
	if len(failed) != 0 {
		result.Primary.MetricName = failed[0].Name
		result.Primary.Evidence = failed[0].Reason
	}
	return result
}

func failedMetrics(metrics []metricResult) []metricResult {
	result := make([]metricResult, 0, len(metrics))
	for _, metric := range metrics {
		if metric.Status != "passed" {
			result = append(result, metric)
		}
	}
	return result
}

func toolAttribution(
	metric metricResult,
	evidence []*evalresult.EvalMetricResultPerInvocation,
) (attribution, bool) {
	if metric.Status != status.EvalStatusFailed {
		return attribution{}, false
	}
	name := strings.ToLower(metric.Name)
	if !strings.Contains(name, "tool") {
		return attribution{}, false
	}
	category := attributionToolCallError
	if hasMetricScopedArgumentMismatch(metric.Name, evidence) {
		category = attributionToolParameterError
	}
	return attribution{Category: category, MetricName: metric.Name, Evidence: metric.Reason}, true
}

func hasMetricScopedArgumentMismatch(
	metricName string,
	evidence []*evalresult.EvalMetricResultPerInvocation,
) bool {
	for _, item := range evidence {
		if item == nil || !failedEvidenceMetric(metricName, item.EvalMetricResults) ||
			item.ActualInvocation == nil || item.ExpectedInvocation == nil {
			continue
		}
		actual := item.ActualInvocation.Tools
		expected := item.ExpectedInvocation.Tools
		if len(actual) != len(expected) {
			continue
		}
		matchedNames := true
		argumentMismatch := false
		for i := range actual {
			if actual[i] == nil || expected[i] == nil || actual[i].Name != expected[i].Name {
				matchedNames = false
				break
			}
			if !reflect.DeepEqual(actual[i].Arguments, expected[i].Arguments) {
				argumentMismatch = true
			}
		}
		if matchedNames && argumentMismatch {
			return true
		}
	}
	return false
}

func failedEvidenceMetric(name string, metrics []*evalresult.EvalMetricResult) bool {
	for _, metric := range metrics {
		if metric != nil && metric.MetricName == name && metric.EvalStatus == status.EvalStatusFailed {
			return true
		}
	}
	return false
}

func categorySelector(category attributionCategory, names ...string) func(metricResult) (attribution, bool) {
	return func(metric metricResult) (attribution, bool) {
		if metric.Status != status.EvalStatusFailed {
			return attribution{}, false
		}
		name := strings.ToLower(metric.Name)
		for _, candidate := range names {
			if strings.Contains(name, candidate) {
				return attribution{Category: category, MetricName: metric.Name, Evidence: metric.Reason}, true
			}
		}
		return attribution{}, false
	}
}

func incompleteAttribution(metric metricResult) (attribution, bool) {
	if metric.Status == "unknown" || metric.Status == "not_evaluated" || metric.Status == "" {
		return attribution{
			Category:   attributionEvaluationIncomplete,
			MetricName: metric.Name,
			Evidence:   metric.Reason,
		}, true
	}
	return attribution{}, false
}

func secondaryAttributions(metrics []metricResult, primary attribution) []attribution {
	result := make([]attribution, 0, len(metrics)-1)
	for _, metric := range metrics {
		if metric.Name == primary.MetricName {
			continue
		}
		result = append(result, attribution{
			Category:   attributionUnclassifiedFailure,
			MetricName: metric.Name,
			Evidence:   metric.Reason,
		})
	}
	return result
}
