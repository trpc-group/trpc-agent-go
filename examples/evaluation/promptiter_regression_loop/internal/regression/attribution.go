//
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
	metricllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

const (
	percentageScale        = 100.0
	rubricEvidenceCapacity = 4
	traceFailureMetric     = "trace_execution"
)

// AttributionCatalog provides typed metric evidence.
type AttributionCatalog struct {
	MetricKinds map[string]MetricKind
}

// CatalogFromMetrics derives attribution evidence from metric criteria.
func CatalogFromMetrics(metrics []*metric.EvalMetric) (AttributionCatalog, error) {
	if len(metrics) == 0 {
		return AttributionCatalog{}, errors.New("metrics are empty")
	}
	catalog := AttributionCatalog{MetricKinds: make(map[string]MetricKind, len(metrics))}
	for index, item := range metrics {
		if item == nil {
			return AttributionCatalog{}, fmt.Errorf("metric %d is nil", index)
		}
		name := strings.TrimSpace(item.MetricName)
		if name == "" {
			return AttributionCatalog{}, fmt.Errorf("metric %d name is empty", index)
		}
		if _, ok := catalog.MetricKinds[name]; ok {
			return AttributionCatalog{}, fmt.Errorf("duplicate metric name %q", name)
		}
		catalog.MetricKinds[name] = metricKind(item)
	}
	return catalog, nil
}

func metricKind(item *metric.EvalMetric) MetricKind {
	if item == nil || item.Criterion == nil {
		return MetricKindUnknown
	}
	criterion := item.Criterion
	if criterion.ToolTrajectory != nil {
		return MetricKindToolTrajectory
	}
	if criterion.FinalResponse != nil {
		switch {
		case criterion.FinalResponse.JSON != nil:
			return MetricKindJSON
		case criterion.FinalResponse.XML != nil:
			return MetricKindXML
		case criterion.FinalResponse.Rouge != nil:
			return MetricKindRouge
		default:
			return MetricKindFinalResponse
		}
	}
	if criterion.LLMJudge != nil {
		return rubricKind(criterion.LLMJudge.Rubrics)
	}
	return MetricKindUnknown
}

func rubricKind(rubrics []*metricllm.Rubric) MetricKind {
	parts := make([]string, 0, len(rubrics)*rubricEvidenceCapacity)
	for _, rubric := range rubrics {
		if rubric == nil {
			continue
		}
		parts = append(parts, rubric.ID, rubric.Description, rubric.Type)
		if rubric.Content != nil {
			parts = append(parts, rubric.Content.Text)
		}
	}
	text := normalizeText(strings.Join(parts, " "))
	if containsAny(text, routeWords) {
		return MetricKindRoute
	}
	if containsAny(text, knowledgeWords) {
		return MetricKindKnowledge
	}
	return MetricKindUnknown
}

// Attribute classifies every non-passing metric in an evaluation result.
func Attribute(result *EvaluationResult, catalog AttributionCatalog) AttributionResult {
	output := AttributionResult{
		Items: make([]Attribution, 0),
		Summary: AttributionSummary{
			CategoryCounts:  make(map[AttributionCategory]int),
			CategoryPercent: make(map[AttributionCategory]float64),
		},
	}
	if result == nil {
		return output
	}
	for _, evalCase := range result.Cases {
		nonPassingMetrics := 0
		for _, metric := range evalCase.Metrics {
			if metric.Status == status.EvalStatusPassed {
				continue
			}
			nonPassingMetrics++
			item := attributeMetric(evalCase, metric, catalog.MetricKinds[metric.Name])
			output.Items = append(output.Items, item)
			output.Summary.TotalFailures++
			output.Summary.AttributedFailures++
			output.Summary.CategoryCounts[item.Category]++
			if item.Category == CategoryMetricFailure {
				output.Summary.FallbackFailures++
			}
		}
		if !evalCase.Passed && nonPassingMetrics == 0 {
			item := attributeTraceOnlyFailure(evalCase)
			output.Items = append(output.Items, item)
			output.Summary.TotalFailures++
			output.Summary.AttributedFailures++
			output.Summary.CategoryCounts[item.Category]++
			if item.Category == CategoryMetricFailure {
				output.Summary.FallbackFailures++
			}
		}
	}
	sortAttributions(output.Items)
	calculatePercentages(&output.Summary)
	return output
}

func attributeTraceOnlyFailure(evalCase CaseResult) Attribution {
	category := CategoryMetricFailure
	reason := "case failed without a non-passing metric"
	if traceIsFailure(evalCase.Trace.Status) {
		category = CategoryExecutionError
		reason = "trace status is " + evalCase.Trace.Status
	}
	evidence := []AttributionEvidence{{Source: "trace", Reason: reason}}
	for _, step := range evalCase.Trace.Steps {
		if strings.TrimSpace(step.Error) == "" {
			continue
		}
		evidence = append(evidence, AttributionEvidence{
			Source: "trace_step", StepID: step.StepID, Reason: step.Error,
		})
		break
	}
	return Attribution{
		EvalSetID: evalCase.EvalSetID, CaseID: evalCase.CaseID,
		Metric: traceFailureMetric, Category: category, Evidence: evidence,
	}
}

// MergeAttributions combines attribution from train and validation results.
func MergeAttributions(results ...AttributionResult) AttributionResult {
	merged := AttributionResult{
		Items: make([]Attribution, 0),
		Summary: AttributionSummary{
			CategoryCounts:  make(map[AttributionCategory]int),
			CategoryPercent: make(map[AttributionCategory]float64),
		},
	}
	for _, result := range results {
		merged.Items = append(merged.Items, result.Items...)
		merged.Summary.TotalFailures += result.Summary.TotalFailures
		merged.Summary.AttributedFailures += result.Summary.AttributedFailures
		merged.Summary.FallbackFailures += result.Summary.FallbackFailures
		for category, count := range result.Summary.CategoryCounts {
			merged.Summary.CategoryCounts[category] += count
		}
	}
	sortAttributions(merged.Items)
	calculatePercentages(&merged.Summary)
	return merged
}

func attributeMetric(evalCase CaseResult, metric MetricResult, kind MetricKind) Attribution {
	category := categoryFromMetric(kind, metric.Name, metric.Reason)
	traceCategory, traceEvidence := categoryFromTrace(evalCase.Trace, metric.Reason)
	if traceCategory != "" && (kind == "" || kind == MetricKindUnknown || kind == MetricKindToolTrajectory) {
		category = traceCategory
	}
	evidence := make([]AttributionEvidence, 0, len(traceEvidence)+1)
	evidence = append(evidence, traceEvidence...)
	evidence = append(evidence, AttributionEvidence{
		Source: "metric", Reason: nonEmptyReason(metric.Reason, "non-passing metric "+metric.Name),
	})
	return Attribution{
		EvalSetID: evalCase.EvalSetID,
		CaseID:    evalCase.CaseID,
		Metric:    metric.Name,
		Category:  category,
		Evidence:  evidence,
	}
}

func categoryFromTrace(trace TraceSummary, reason string) (AttributionCategory, []AttributionEvidence) {
	normalizedReason := normalizeText(reason)
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.Error) == "" {
			continue
		}
		evidence := []AttributionEvidence{{Source: "trace_step", StepID: step.StepID, Reason: step.Error}}
		if step.NodeType == "tool" {
			if containsAny(normalizedReason+" "+normalizeText(step.Error), parameterWords) {
				return CategoryToolParameterError, evidence
			}
			return CategoryToolCallError, evidence
		}
		return CategoryExecutionError, evidence
	}
	if trace.Status == "failed" || trace.Status == "incomplete" {
		return CategoryExecutionError, []AttributionEvidence{{
			Source: "trace", Reason: "trace status is " + trace.Status,
		}}
	}
	return "", nil
}

func categoryFromMetric(kind MetricKind, name, reason string) AttributionCategory {
	text := normalizeText(name + " " + reason)
	switch kind {
	case MetricKindToolTrajectory:
		if containsAny(text, parameterWords) {
			return CategoryToolParameterError
		}
		return CategoryToolCallError
	case MetricKindRoute:
		return CategoryRouteError
	case MetricKindJSON, MetricKindXML:
		return CategoryFormatError
	case MetricKindKnowledge:
		return CategoryKnowledgeRecall
	case MetricKindFinalResponse, MetricKindRouge:
		return CategoryFinalResponseMismatch
	default:
		return categoryFromWords(text)
	}
}

func categoryFromWords(text string) AttributionCategory {
	switch {
	case containsAny(text, routeWords):
		return CategoryRouteError
	case containsAny(text, formatWords):
		return CategoryFormatError
	case containsAny(text, knowledgeWords):
		return CategoryKnowledgeRecall
	case containsAny(text, toolWords):
		return CategoryToolCallError
	case containsAny(text, responseWords):
		return CategoryFinalResponseMismatch
	default:
		return CategoryMetricFailure
	}
}

var parameterWords = []string{
	"argument", "arguments", "parameter", "parameters", "input schema", "invalid input", "malformed input",
	"missing input field", "参数", "入参", "字段",
}

var routeWords = []string{
	"route", "router", "handoff", "agent selection", "路由", "转交", "代理选择",
}

var formatWords = []string{
	"json", "xml", "format", "parse", "structured", "格式", "解析", "结构化",
}

var knowledgeWords = []string{
	"knowledge", "recall", "retrieval", "grounding", "知识", "召回", "检索",
}

var toolWords = []string{
	"tool", "trajectory", "function call", "工具", "调用轨迹",
}

var responseWords = []string{
	"response", "rouge", "answer", "text mismatch", "回复", "答案", "文本不匹配",
}

func normalizeText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func containsAny(value string, words []string) bool {
	for _, word := range words {
		if keywordPresent(value, word) {
			return true
		}
	}
	return false
}

func keywordPresent(value, keyword string) bool {
	if keyword == "" {
		return false
	}
	if !isASCIIKeyword(keyword) {
		return strings.Contains(value, keyword)
	}
	for offset := 0; offset <= len(value)-len(keyword); {
		index := strings.Index(value[offset:], keyword)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(keyword)
		leftBoundary := start == 0 || !isASCIIWordByte(value[start-1])
		rightBoundary := end == len(value) || !isASCIIWordByte(value[end])
		if leftBoundary && rightBoundary {
			return true
		}
		offset = start + 1
	}
	return false
}

func isASCIIKeyword(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= 0x80 {
			return false
		}
	}
	return true
}

func isASCIIWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func nonEmptyReason(reason, fallback string) string {
	if strings.TrimSpace(reason) != "" {
		return strings.TrimSpace(reason)
	}
	return fallback
}

func sortAttributions(items []Attribution) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].EvalSetID != items[j].EvalSetID {
			return items[i].EvalSetID < items[j].EvalSetID
		}
		if items[i].CaseID != items[j].CaseID {
			return items[i].CaseID < items[j].CaseID
		}
		return items[i].Metric < items[j].Metric
	})
}

func calculatePercentages(summary *AttributionSummary) {
	if summary == nil || summary.TotalFailures == 0 {
		return
	}
	for category, count := range summary.CategoryCounts {
		summary.CategoryPercent[category] = percentageScale * float64(count) / float64(summary.TotalFailures)
	}
}
