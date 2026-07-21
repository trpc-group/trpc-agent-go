//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	metricllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/xml"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestAttributeUsesTypedTraceAndBilingualEvidence(t *testing.T) {
	result, catalog, expected := attributionFixture()
	attributed := Attribute(result, catalog)

	require.Len(t, attributed.Items, len(expected))
	correct := 0
	for _, item := range attributed.Items {
		assert.NotEmpty(t, item.Evidence)
		if expected[item.Metric] == item.Category {
			correct++
		}
	}
	assert.Equal(t, len(expected), correct)
	assert.Equal(t, len(expected), attributed.Summary.TotalFailures)
	assert.Equal(t, 1, attributed.Summary.FallbackFailures)
	assert.InDelta(t, 12.5, attributed.Summary.CategoryPercent[CategoryMetricFailure], scoreEpsilon)
}

func TestAttributionAccuracyMeetsRequiredThreshold(t *testing.T) {
	const requiredAccuracy = 0.75
	result, catalog, expected := attributionFixture()
	items := Attribute(result, catalog).Items
	correct := 0
	for _, item := range items {
		if item.Category == expected[item.Metric] {
			correct++
		}
	}
	accuracy := float64(correct) / float64(len(expected))
	assert.GreaterOrEqual(t, accuracy, requiredAccuracy)
}

func TestAttributeNilResultReturnsInitializedEmptySummary(t *testing.T) {
	result := Attribute(nil, AttributionCatalog{})
	assert.Empty(t, result.Items)
	assert.Empty(t, result.Summary.CategoryCounts)
	assert.Empty(t, result.Summary.CategoryPercent)
	assert.Zero(t, result.Summary.TotalFailures)
}

func TestCatalogFromMetricsDerivesCriterionKinds(t *testing.T) {
	metrics := []*metric.EvalMetric{
		{MetricName: "unknown"},
		{MetricName: "tool", Criterion: &criterion.Criterion{ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{}}},
		{MetricName: "text", Criterion: &criterion.Criterion{FinalResponse: &finalresponse.FinalResponseCriterion{}}},
		{MetricName: "json", Criterion: &criterion.Criterion{FinalResponse: &finalresponse.FinalResponseCriterion{JSON: &criterionjson.JSONCriterion{}}}},
		{MetricName: "xml", Criterion: &criterion.Criterion{FinalResponse: &finalresponse.FinalResponseCriterion{XML: &xml.XMLCriterion{}}}},
		{MetricName: "rouge", Criterion: &criterion.Criterion{FinalResponse: &finalresponse.FinalResponseCriterion{Rouge: &rouge.RougeCriterion{}}}},
		llmMetric("route", "路由选择正确"),
		llmMetric("knowledge", "knowledge retrieval quality"),
		llmMetric("rubric-unknown", "style quality"),
	}

	catalog, err := CatalogFromMetrics(metrics)
	require.NoError(t, err)
	assert.Equal(t, map[string]MetricKind{
		"unknown":        MetricKindUnknown,
		"tool":           MetricKindToolTrajectory,
		"text":           MetricKindFinalResponse,
		"json":           MetricKindJSON,
		"xml":            MetricKindXML,
		"rouge":          MetricKindRouge,
		"route":          MetricKindRoute,
		"knowledge":      MetricKindKnowledge,
		"rubric-unknown": MetricKindUnknown,
	}, catalog.MetricKinds)
}

func TestCatalogFromMetricsRejectsInvalidDefinitions(t *testing.T) {
	_, err := CatalogFromMetrics(nil)
	require.ErrorContains(t, err, "metrics are empty")
	_, err = CatalogFromMetrics([]*metric.EvalMetric{nil})
	require.ErrorContains(t, err, "is nil")
	_, err = CatalogFromMetrics([]*metric.EvalMetric{{MetricName: " "}})
	require.ErrorContains(t, err, "name is empty")
	_, err = CatalogFromMetrics([]*metric.EvalMetric{{MetricName: "same"}, {MetricName: "same"}})
	require.ErrorContains(t, err, "duplicate metric")
}

func TestMergeAttributionsCombinesSortsAndRecomputesPercentages(t *testing.T) {
	leftCase := caseWithNamedMetric("b", "tool", 0, status.EvalStatusFailed)
	rightCase := caseWithNamedMetric("a", "answer", 0, status.EvalStatusFailed)
	rightCase.EvalSetID = "another-set"
	left := Attribute(evaluationWithCases(leftCase), AttributionCatalog{
		MetricKinds: map[string]MetricKind{"tool": MetricKindToolTrajectory},
	})
	right := Attribute(evaluationWithCases(rightCase), AttributionCatalog{
		MetricKinds: map[string]MetricKind{"answer": MetricKindFinalResponse},
	})

	merged := MergeAttributions(left, right)
	require.Len(t, merged.Items, 2)
	assert.Equal(t, "another-set", merged.Items[0].EvalSetID)
	assert.Equal(t, 2, merged.Summary.TotalFailures)
	assert.Equal(t, 50.0, merged.Summary.CategoryPercent[CategoryToolCallError])
	assert.Equal(t, 50.0, merged.Summary.CategoryPercent[CategoryFinalResponseMismatch])
}

func TestAttributePrioritizesToolTraceErrorsAndTraceStatus(t *testing.T) {
	parameterCase := caseWithNamedMetric("parameter", "custom", 0, status.EvalStatusFailed)
	parameterCase.Metrics[0].Reason = "参数错误"
	parameterCase.Trace.Steps = []TraceStep{{StepID: "tool-1", NodeType: "tool", Error: "invalid input"}}
	toolCase := caseWithNamedMetric("tool", "custom", 0, status.EvalStatusFailed)
	toolCase.Trace.Steps = []TraceStep{{StepID: "tool-2", NodeType: "tool", Error: "service unavailable"}}
	statusCase := caseWithNamedMetric("status", "custom", 0, status.EvalStatusFailed)
	statusCase.Trace.Status = "incomplete"
	statusCase.Metrics[0].Reason = ""

	result := Attribute(evaluationWithCases(parameterCase, toolCase, statusCase), AttributionCatalog{})
	require.Len(t, result.Items, 3)
	byCase := make(map[string]AttributionCategory, len(result.Items))
	for _, item := range result.Items {
		byCase[item.CaseID] = item.Category
		assert.NotEmpty(t, item.Evidence[0].Reason)
	}
	assert.Equal(t, CategoryToolParameterError, byCase["parameter"])
	assert.Equal(t, CategoryToolCallError, byCase["tool"])
	assert.Equal(t, CategoryExecutionError, byCase["status"])
}

func TestToolInputTextDoesNotImplyParameterFailure(t *testing.T) {
	item := caseWithNamedMetric("tool", "custom", 0, status.EvalStatusFailed)
	item.Trace.Steps = []TraceStep{{
		StepID: "tool-stream", NodeType: "tool", Error: "tool input stream timeout",
	}}
	result := Attribute(evaluationWithCases(item), AttributionCatalog{})
	require.Len(t, result.Items, 1)
	assert.Equal(t, CategoryToolCallError, result.Items[0].Category)
}

func TestUntypedToolParameterFailuresUseSpecificCategory(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{name: "english", reason: "tool call has an invalid argument"},
		{name: "chinese", reason: "工具调用参数错误"},
		{name: "mixed format signal", reason: "tool parameter contains invalid JSON format"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := caseWithNamedMetric(test.name, "custom", 0, status.EvalStatusFailed)
			item.Metrics[0].Reason = test.reason
			result := Attribute(evaluationWithCases(item), AttributionCatalog{})
			require.Len(t, result.Items, 1)
			assert.Equal(t, CategoryToolParameterError, result.Items[0].Category)
		})
	}
}

func TestTypedMetricCategoryIsNotOverriddenByTraceError(t *testing.T) {
	item := caseWithNamedMetric("format", "schema", 0, status.EvalStatusFailed)
	item.Metrics[0].Reason = "invalid JSON output"
	item.Trace.Steps = []TraceStep{{
		StepID: "tool-1", NodeType: "tool", Error: "service unavailable",
	}}
	result := Attribute(evaluationWithCases(item), AttributionCatalog{
		MetricKinds: map[string]MetricKind{"schema": MetricKindJSON},
	})
	require.Len(t, result.Items, 1)
	assert.Equal(t, CategoryFormatError, result.Items[0].Category)
	require.Len(t, result.Items[0].Evidence, 2)
	assert.Equal(t, "trace_step", result.Items[0].Evidence[0].Source)
}

func TestEnglishKeywordsRequireWordBoundaries(t *testing.T) {
	item := caseWithNamedMetric("knowledge", "custom", 0, status.EvalStatusFailed)
	item.Metrics[0].Reason = "knowledge information missing"
	result := Attribute(evaluationWithCases(item), AttributionCatalog{})
	require.Len(t, result.Items, 1)
	assert.Equal(t, CategoryKnowledgeRecall, result.Items[0].Category)

	item = caseWithNamedMetric("format", "custom", 0, status.EvalStatusFailed)
	item.Metrics[0].Reason = "format_error"
	result = Attribute(evaluationWithCases(item), AttributionCatalog{})
	require.Len(t, result.Items, 1)
	assert.Equal(t, CategoryFormatError, result.Items[0].Category)
}

func TestAttributeExplainsNotEvaluatedMetricFromFailedTrace(t *testing.T) {
	item := caseWithNamedMetric("runtime", "not_run", 0, status.EvalStatusNotEvaluated)
	item.Trace.Status = "failed"
	item.Metrics[0].Reason = ""

	result := Attribute(evaluationWithCases(item), AttributionCatalog{})
	require.Len(t, result.Items, 1)
	assert.Equal(t, CategoryExecutionError, result.Items[0].Category)
	assert.NotEmpty(t, result.Items[0].Evidence)
	assert.Equal(t, 1, result.Summary.AttributedFailures)
}

func llmMetric(name, rubricText string) *metric.EvalMetric {
	return &metric.EvalMetric{
		MetricName: name,
		Criterion: &criterion.Criterion{LLMJudge: &metricllm.LLMCriterion{
			Rubrics: []*metricllm.Rubric{nil, {ID: name, Content: &metricllm.RubricContent{Text: rubricText}}},
		}},
	}
}

func attributionFixture() (*EvaluationResult, AttributionCatalog, map[string]AttributionCategory) {
	specs := []struct {
		metric   string
		reason   string
		kind     MetricKind
		expected AttributionCategory
	}{
		{metric: "arguments", reason: "arguments mismatch", kind: MetricKindToolTrajectory, expected: CategoryToolParameterError},
		{metric: "trajectory", reason: "wrong tool call", kind: MetricKindToolTrajectory, expected: CategoryToolCallError},
		{metric: "route", reason: "代理选择错误", kind: MetricKindRoute, expected: CategoryRouteError},
		{metric: "schema", reason: "invalid output", kind: MetricKindJSON, expected: CategoryFormatError},
		{metric: "recall", reason: "知识召回不足", kind: MetricKindKnowledge, expected: CategoryKnowledgeRecall},
		{metric: "answer", reason: "text differs", kind: MetricKindRouge, expected: CategoryFinalResponseMismatch},
		{metric: "runtime", reason: "runner stopped", kind: MetricKindUnknown, expected: CategoryExecutionError},
		{metric: "custom", reason: "custom threshold missed", kind: MetricKindUnknown, expected: CategoryMetricFailure},
	}
	cases := make([]CaseResult, 0, len(specs))
	kinds := make(map[string]MetricKind, len(specs))
	expected := make(map[string]AttributionCategory, len(specs))
	for _, spec := range specs {
		item := caseWithNamedMetric(spec.metric, spec.metric, 0, status.EvalStatusFailed)
		if spec.metric == "runtime" {
			item.Trace.Status = "failed"
			item.Trace.Steps = []TraceStep{{StepID: "step-runtime", NodeType: "llm", Error: "runner stopped"}}
		}
		cases = append(cases, item)
		kinds[spec.metric] = spec.kind
		expected[spec.metric] = spec.expected
	}
	return evaluationWithCases(cases...), AttributionCatalog{MetricKinds: kinds}, expected
}
