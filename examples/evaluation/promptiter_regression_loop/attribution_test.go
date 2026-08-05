//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttributeCase_FormatError(t *testing.T) {
	caseScore := CaseScore{
		EvalCaseID: "c1",
		Passed:     false,
		Score:      0.0,
		Metrics: []MetricScore{
			{MetricName: "headline_format_validity", Passed: false, Score: 0, Reason: "actual output is not valid JSON"},
			{MetricName: "headline_exact_match", Passed: false, Score: 0, Reason: "source and target do not match"},
		},
	}
	attr := AttributeCase(caseScore)
	require.NotNil(t, attr)
	require.Equal(t, CategoryFormatError, attr.Category)
	require.GreaterOrEqual(t, attr.Confidence, 0.9)
}

func TestAttributeCase_FinalResponseMismatch(t *testing.T) {
	caseScore := CaseScore{
		EvalCaseID: "c2",
		Passed:     false,
		Score:      0.5,
		Metrics: []MetricScore{
			{MetricName: "headline_format_validity", Passed: true, Score: 1, Reason: ""},
			{MetricName: "headline_exact_match", Passed: false, Score: 0, Reason: "source and target do not match"},
		},
	}
	attr := AttributeCase(caseScore)
	require.NotNil(t, attr)
	require.Equal(t, CategoryFinalResponseMismatch, attr.Category)
}

func TestAttributeCase_ToolCallError(t *testing.T) {
	caseScore := CaseScore{
		EvalCaseID: "c3",
		Passed:     false,
		Metrics: []MetricScore{
			{MetricName: "tool_trajectory_avg_score", Passed: false, Score: 0, Reason: "expected tool call not found in trajectory"},
		},
	}
	attr := AttributeCase(caseScore)
	require.NotNil(t, attr)
	require.Equal(t, CategoryToolCallError, attr.Category)
}

func TestAttributeCase_ToolArgError(t *testing.T) {
	caseScore := CaseScore{
		EvalCaseID: "c4",
		Passed:     false,
		Metrics: []MetricScore{
			{MetricName: "tool_argument_validity", Passed: false, Score: 0, Reason: "wrong parameter value"},
		},
	}
	attr := AttributeCase(caseScore)
	require.NotNil(t, attr)
	require.Equal(t, CategoryToolArgError, attr.Category)
}

func TestAttributeCase_RouteError(t *testing.T) {
	caseScore := CaseScore{
		EvalCaseID: "c5",
		Passed:     false,
		Metrics: []MetricScore{
			{MetricName: "router_decision", Passed: false, Score: 0, Reason: "routed to wrong agent"},
		},
	}
	attr := AttributeCase(caseScore)
	require.NotNil(t, attr)
	require.Equal(t, CategoryRouteError, attr.Category)
}

func TestAttributeCase_KnowledgeRecall(t *testing.T) {
	caseScore := CaseScore{
		EvalCaseID: "c6",
		Passed:     false,
		Metrics: []MetricScore{
			{MetricName: "knowledge_recall_score", Passed: false, Score: 0, Reason: "missing cited facts"},
		},
	}
	attr := AttributeCase(caseScore)
	require.NotNil(t, attr)
	require.Equal(t, CategoryKnowledgeRecall, attr.Category)
}

func TestAttributeCase_ReasonFallback(t *testing.T) {
	// Unknown metric name, but the failure reason mentions JSON format.
	caseScore := CaseScore{
		EvalCaseID: "c7",
		Passed:     false,
		Metrics: []MetricScore{
			{MetricName: "custom_metric", Passed: false, Score: 0, Reason: "output failed to parse as JSON schema"},
		},
	}
	attr := AttributeCase(caseScore)
	require.NotNil(t, attr)
	require.Equal(t, CategoryFormatError, attr.Category)
	require.InDelta(t, 0.7, attr.Confidence, 1e-9)
}

func TestAttributeCase_OtherWhenNoSignal(t *testing.T) {
	caseScore := CaseScore{
		EvalCaseID: "c8",
		Passed:     false,
		Metrics: []MetricScore{
			{MetricName: "mystery_metric", Passed: false, Score: 0, Reason: ""},
		},
	}
	attr := AttributeCase(caseScore)
	require.NotNil(t, attr)
	require.Equal(t, CategoryOther, attr.Category)
	require.InDelta(t, 0.5, attr.Confidence, 1e-9)
}

func TestAttributeCase_PassedCaseIsNil(t *testing.T) {
	caseScore := CaseScore{
		EvalCaseID: "c9",
		Passed:     true,
		Score:      1.0,
		Metrics: []MetricScore{
			{MetricName: "headline_format_validity", Passed: true, Score: 1},
		},
	}
	require.Nil(t, AttributeCase(caseScore))
}

func TestAttributionDistribution(t *testing.T) {
	attributions := []CaseAttribution{
		{EvalCaseID: "a", Category: CategoryFormatError},
		{EvalCaseID: "b", Category: CategoryFormatError},
		{EvalCaseID: "c", Category: CategoryFinalResponseMismatch},
	}
	distribution := AttributionDistribution(attributions)
	require.Equal(t, 2, distribution[CategoryFormatError])
	require.Equal(t, 1, distribution[CategoryFinalResponseMismatch])
}

func TestAttributeAll_SkipsPassed(t *testing.T) {
	cases := []CaseScore{
		{EvalCaseID: "p1", Passed: true, Score: 1.0},
		{EvalCaseID: "f1", Passed: false, Score: 0.0, Metrics: []MetricScore{
			{MetricName: "headline_format_validity", Passed: false},
		}},
	}
	attributions := AttributeAll(cases)
	require.Len(t, attributions, 1)
	require.Equal(t, "f1", attributions[0].EvalCaseID)
}
