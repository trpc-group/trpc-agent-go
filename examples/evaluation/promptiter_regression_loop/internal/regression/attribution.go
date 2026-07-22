// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// MetricKind describes the strongest deterministic signal a metric provides.
type MetricKind string

const (
	// MetricUnknown means the metric provides no known attribution signal.
	MetricUnknown MetricKind = "unknown"
	// MetricFinalResponse means the metric evaluates the final response.
	MetricFinalResponse MetricKind = "final_response"
	// MetricToolTrajectory means the metric evaluates tool-call behavior.
	MetricToolTrajectory MetricKind = "tool_trajectory"
	// MetricFormat means the metric evaluates output structure or formatting.
	MetricFormat MetricKind = "format"
	// MetricRoute means the metric evaluates routing or agent handoff.
	MetricRoute MetricKind = "route"
	// MetricKnowledge means the metric evaluates retrieval or grounding.
	MetricKnowledge MetricKind = "knowledge"
)

// AttributionCatalog maps configured metric names to deterministic signals.
type AttributionCatalog struct {
	MetricKinds map[string]MetricKind
}

// CatalogFromMetrics derives attribution signals from the configured metric
// names and evaluators. Business-specific metrics still receive a documented
// metric_failure fallback instead of an invented diagnosis.
func CatalogFromMetrics(metrics []*metric.EvalMetric) (AttributionCatalog, error) {
	catalog := AttributionCatalog{MetricKinds: make(map[string]MetricKind, len(metrics))}
	for _, item := range metrics {
		if item == nil {
			return AttributionCatalog{}, errors.New("metric configuration contains nil metric")
		}
		name := strings.TrimSpace(item.MetricName)
		if name == "" {
			return AttributionCatalog{}, errors.New("metric configuration contains empty name")
		}
		if _, ok := catalog.MetricKinds[name]; ok {
			return AttributionCatalog{}, fmt.Errorf("duplicate metric configuration %q", name)
		}
		catalog.MetricKinds[name] = inferMetricKind(name + " " + item.EvaluatorName)
	}
	return catalog, nil
}

// AttributeFailures classifies every non-passing metric and always supplies
// at least one human-readable reason.
func AttributeFailures(result *EvaluationResult, catalog AttributionCatalog) AttributionResult {
	output := AttributionResult{
		Items:   []Attribution{},
		Summary: AttributionSummary{CategoryCounts: make(map[FailureCategory]int)},
	}
	if result == nil {
		return output
	}
	for _, evalCase := range result.Cases {
		for _, metricResult := range evalCase.Metrics {
			if metricResult.Status == status.EvalStatusPassed {
				continue
			}
			category := classifyFailure(evalCase, metricResult, catalog.MetricKinds[metricResult.Name])
			reason := strings.TrimSpace(metricResult.Reason)
			if reason == "" {
				reason = fallbackFailureReason(evalCase, metricResult)
			}
			item := Attribution{
				EvalSetID: evalCase.EvalSetID,
				CaseID:    evalCase.CaseID,
				Metric:    metricResult.Name,
				Category:  category,
				Reason:    reason,
				Evidence:  attributionEvidence(evalCase, metricResult),
			}
			output.Items = append(output.Items, item)
			output.Summary.TotalFailures++
			if category != FailureMetric {
				output.Summary.AttributedFailures++
			}
			output.Summary.CategoryCounts[category]++
		}
	}
	sort.Slice(output.Items, func(i, j int) bool {
		left, right := output.Items[i], output.Items[j]
		if left.EvalSetID != right.EvalSetID {
			return left.EvalSetID < right.EvalSetID
		}
		if left.CaseID != right.CaseID {
			return left.CaseID < right.CaseID
		}
		return left.Metric < right.Metric
	})
	return output
}

// MergeAttributions combines phase-level results without losing category counts.
func MergeAttributions(results ...AttributionResult) AttributionResult {
	merged := AttributionResult{
		Items:   []Attribution{},
		Summary: AttributionSummary{CategoryCounts: make(map[FailureCategory]int)},
	}
	for _, result := range results {
		merged.Items = append(merged.Items, result.Items...)
		merged.Summary.TotalFailures += result.Summary.TotalFailures
		merged.Summary.AttributedFailures += result.Summary.AttributedFailures
		for category, count := range result.Summary.CategoryCounts {
			merged.Summary.CategoryCounts[category] += count
		}
	}
	sort.Slice(merged.Items, func(i, j int) bool {
		left, right := merged.Items[i], merged.Items[j]
		if left.EvalSetID != right.EvalSetID {
			return left.EvalSetID < right.EvalSetID
		}
		if left.CaseID != right.CaseID {
			return left.CaseID < right.CaseID
		}
		return left.Metric < right.Metric
	})
	return merged
}

func classifyFailure(evalCase CaseResult, metricResult MetricResult, kind MetricKind) FailureCategory {
	if evalCase.ErrorMessage != "" || evalCase.Trace.Status == "failed" {
		return FailureExecution
	}
	search := strings.ToLower(metricResult.Name + " " + metricResult.Reason)
	switch {
	case containsAny(search, "route", "routing", "handoff", "transfer", "sub-agent", "subagent"):
		return FailureRoute
	case kind == MetricToolTrajectory && containsAny(search, "argument", "parameter", "args", "schema"):
		return FailureToolArgument
	case kind == MetricToolTrajectory || containsAny(search, "tool call", "tool selection", "wrong tool", "missing tool"):
		return FailureToolCall
	case kind == MetricFormat || containsAny(search, "json", "xml", "schema", "structured output", "format"):
		return FailureFormat
	case kind == MetricKnowledge || containsAny(search, "knowledge", "retrieval", "recall", "grounding", "citation"):
		return FailureKnowledge
	case kind == MetricRoute:
		return FailureRoute
	case kind == MetricFinalResponse || containsAny(search, "response", "answer", "rouge", "mismatch"):
		return FailureFinalResponse
	default:
		return FailureMetric
	}
}

func inferMetricKind(value string) MetricKind {
	value = strings.ToLower(value)
	switch {
	case containsAny(value, "tooltrajectory", "tool_trajectory", "tool trajectory"):
		return MetricToolTrajectory
	case containsAny(value, "route", "routing", "handoff", "transfer"):
		return MetricRoute
	case containsAny(value, "knowledge", "retrieval", "recall", "ground"):
		return MetricKnowledge
	case containsAny(value, "json", "xml", "schema", "format", "structured"):
		return MetricFormat
	case containsAny(value, "finalresponse", "final_response", "final response", "rouge", "text", "length"):
		return MetricFinalResponse
	default:
		return MetricUnknown
	}
}

func attributionEvidence(evalCase CaseResult, metricResult MetricResult) []string {
	evidence := make([]string, 0, 3)
	if reason := strings.TrimSpace(metricResult.Reason); reason != "" {
		evidence = append(evidence, "metric reason: "+reason)
	}
	if message := strings.TrimSpace(evalCase.ErrorMessage); message != "" {
		evidence = append(evidence, "execution error: "+message)
	}
	for _, step := range evalCase.Trace.Steps {
		if step.Error != "" {
			evidence = append(evidence, fmt.Sprintf("trace step %s: %s", step.StepID, step.Error))
			break
		}
	}
	if len(evidence) == 0 {
		evidence = append(evidence, fmt.Sprintf("metric %s returned status %s and score %.4f",
			metricResult.Name, metricResult.Status, metricResult.Score))
	}
	return evidence
}

func fallbackFailureReason(evalCase CaseResult, metricResult MetricResult) string {
	if evalCase.ErrorMessage != "" {
		return evalCase.ErrorMessage
	}
	return fmt.Sprintf("metric %s returned status %s with score %.4f",
		metricResult.Name, metricResult.Status, metricResult.Score)
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
