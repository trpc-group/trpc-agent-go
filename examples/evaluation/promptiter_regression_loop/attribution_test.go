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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// testMetrics builds a metric set covering every criterion family used by
// category derivation.
func testMetrics() []*metric.EvalMetric {
	return []*metric.EvalMetric{
		{
			MetricName: "final_response_avg_score",
			Criterion: &criterion.Criterion{
				FinalResponse: &finalresponse.FinalResponseCriterion{
					Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
				},
			},
		},
		{
			MetricName: "tool_trajectory_avg_score",
			Criterion: &criterion.Criterion{
				ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{},
			},
		},
		{
			MetricName: "json_structure",
			Criterion: &criterion.Criterion{
				FinalResponse: &finalresponse.FinalResponseCriterion{
					JSON: &criterionjson.JSONCriterion{},
				},
			},
		},
		{
			MetricName: "format_rubric",
			Criterion: &criterion.Criterion{
				LLMJudge: &llm.LLMCriterion{
					Rubrics: []*llm.Rubric{{ID: "fmt", Type: "FORMAT_COMPLIANCE"}},
				},
			},
		},
		{
			MetricName: "knowledge_rubric",
			Criterion: &criterion.Criterion{
				LLMJudge: &llm.LLMCriterion{
					Rubrics: []*llm.Rubric{{ID: "recall", Type: "KNOWLEDGE_RECALL"}},
				},
			},
		},
		{
			MetricName: "quality_rubric",
			Criterion: &criterion.Criterion{
				LLMJudge: &llm.LLMCriterion{
					Rubrics: []*llm.Rubric{
						{ID: "fmt", Type: "FORMAT_COMPLIANCE"},
						{ID: "story", Type: "FINAL_RESPONSE_QUALITY"},
					},
				},
			},
		},
	}
}

func failedMetric(name, reason string) MetricSnapshot {
	return MetricSnapshot{Name: name, Score: 0, Status: status.EvalStatusFailed, Reason: reason}
}

func failedSnapshot(caseID string, metrics ...MetricSnapshot) CaseSnapshot {
	return CaseSnapshot{
		EvalSetID:  "train",
		EvalCaseID: caseID,
		Pass:       false,
		Metrics:    metrics,
	}
}

func invocationWithTools(tools ...*evalset.Tool) []*evalset.Invocation {
	return []*evalset.Invocation{{InvocationID: "inv-1", Tools: tools}}
}

func TestAttributeSixCategories(t *testing.T) {
	attributor := NewAttributor(testMetrics(), map[string]string{
		"router_check": string(CauseRouteError),
	})
	tests := []struct {
		name     string
		snapshot CaseSnapshot
		expected []*evalset.Invocation
		want     FailureCategory
	}{
		{
			name:     "final response mismatch",
			snapshot: failedSnapshot("c1", failedMetric("final_response_avg_score", "text mismatch")),
			want:     CauseFinalResponseMismatch,
		},
		{
			name: "tool call error via trajectory diff",
			snapshot: func() CaseSnapshot {
				s := failedSnapshot("c2", failedMetric("tool_trajectory_avg_score", ""))
				s.ActualInvocations = invocationWithTools(&evalset.Tool{Name: "query_logistics"})
				return s
			}(),
			expected: invocationWithTools(&evalset.Tool{Name: "query_order"}),
			want:     CauseToolCallError,
		},
		{
			name: "tool argument error via trajectory diff",
			snapshot: func() CaseSnapshot {
				s := failedSnapshot("c3", failedMetric("tool_trajectory_avg_score", ""))
				s.ActualInvocations = invocationWithTools(
					&evalset.Tool{Name: "query_order", Arguments: map[string]any{"order_id": "WRONG"}},
				)
				return s
			}(),
			expected: invocationWithTools(
				&evalset.Tool{Name: "query_order", Arguments: map[string]any{"order_id": "ORD-1"}},
			),
			want: CauseToolArgumentError,
		},
		{
			name:     "route error via hint",
			snapshot: failedSnapshot("c4", failedMetric("router_check", "routed to wrong sub-agent")),
			want:     CauseRouteError,
		},
		{
			name:     "format error via json criterion",
			snapshot: failedSnapshot("c5", failedMetric("json_structure", "invalid json")),
			want:     CauseFormatError,
		},
		{
			name:     "knowledge recall gap via rubric type",
			snapshot: failedSnapshot("c6", failedMetric("knowledge_rubric", "missing citation")),
			want:     CauseKnowledgeRecallGap,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attribution := attributor.Attribute(tt.snapshot, tt.expected)
			require.NotNil(t, attribution)
			require.NotEmpty(t, attribution.RootCauses)
			assert.Equal(t, tt.want, attribution.RootCauses[0].Category)
			assert.NotEmpty(t, attribution.RootCauses[0].Evidence,
				"every failure must carry explainable evidence")
		})
	}
}

// TestAttributeCausalFolding: route + tool + response failures on one case
// must fold into a single chain with route as the only root, and only the
// root goes into loss hints.
func TestAttributeCausalFolding(t *testing.T) {
	attributor := NewAttributor(testMetrics(), map[string]string{
		"router_check": string(CauseRouteError),
	})
	snapshot := failedSnapshot(
		"cascade",
		failedMetric("router_check", "routed to billing agent instead of order agent"),
		failedMetric("tool_trajectory_avg_score", "number of tool calls mismatch: actual(0) != expected(1)"),
		failedMetric("final_response_avg_score", "text mismatch"),
	)
	attribution := attributor.Attribute(snapshot, nil)
	require.NotNil(t, attribution)

	require.Len(t, attribution.RootCauses, 1, "route error must be the only root")
	assert.Equal(t, CauseRouteError, attribution.RootCauses[0].Category)
	require.Len(t, attribution.Chain, 3)
	assert.Empty(t, attribution.Chain[0].DerivedFrom)
	assert.Equal(t, CauseRouteError, attribution.Chain[1].DerivedFrom)
	assert.Equal(t, CauseRouteError, attribution.Chain[2].DerivedFrom)
	assert.Contains(t, attribution.ChainSummary(), "root: route_error")
	assert.Contains(t, attribution.ChainSummary(), "cascaded to")

	hints := buildLossHints([]CaseAttribution{*attribution})
	require.Len(t, hints, 1, "only the root cause becomes a loss hint")
	assert.Equal(t, "router_check", hints[0].MetricName)
	assert.Equal(t, "cascade", hints[0].EvalCaseID)
	assert.Contains(t, hints[0].Reason, "route_error")
	assert.Contains(t, hints[0].Reason, "cascaded to")
}

// TestAttributeParallelRoots: format and knowledge failures have equal causal
// rank and stay as independent roots.
func TestAttributeParallelRoots(t *testing.T) {
	attributor := NewAttributor(testMetrics(), nil)
	snapshot := failedSnapshot(
		"parallel",
		failedMetric("format_rubric", "markdown in plain-text channel"),
		failedMetric("knowledge_rubric", "missing fact"),
	)
	attribution := attributor.Attribute(snapshot, nil)
	require.NotNil(t, attribution)
	require.Len(t, attribution.RootCauses, 2)
	categories := []FailureCategory{
		attribution.RootCauses[0].Category,
		attribution.RootCauses[1].Category,
	}
	assert.Contains(t, categories, CauseFormatError)
	assert.Contains(t, categories, CauseKnowledgeRecallGap)

	hints := buildLossHints([]CaseAttribution{*attribution})
	assert.Len(t, hints, 2, "both parallel roots become hints")
}

// TestAttributeFallback: unknown metrics fall back to final response mismatch
// with the raw reason as evidence, and passing cases attribute to nil.
func TestAttributeFallback(t *testing.T) {
	attributor := NewAttributor(nil, nil)
	attribution := attributor.Attribute(failedSnapshot(
		"unknown", failedMetric("mystery_metric", "some reason"),
	), nil)
	require.NotNil(t, attribution)
	require.Len(t, attribution.RootCauses, 1)
	assert.Equal(t, CauseFinalResponseMismatch, attribution.RootCauses[0].Category)
	assert.Equal(t, "some reason", attribution.RootCauses[0].Evidence)

	assert.Nil(t, attributor.Attribute(CaseSnapshot{Pass: true}, nil))
}

// TestAttributeToolReasonFallback: without trajectory data the reason text
// still splits call versus argument errors.
func TestAttributeToolReasonFallback(t *testing.T) {
	attributor := NewAttributor(testMetrics(), nil)
	call := attributor.Attribute(failedSnapshot(
		"no-invocations",
		failedMetric("tool_trajectory_avg_score", "tool trajectory mismatch: validate tool counts: number of tool calls mismatch: actual(0) != expected(1)"),
	), nil)
	require.NotNil(t, call)
	assert.Equal(t, CauseToolCallError, call.RootCauses[0].Category)

	arguments := attributor.Attribute(failedSnapshot(
		"args",
		failedMetric("tool_trajectory_avg_score", "match tools: tool id x mismatch: arguments mismatch"),
	), nil)
	require.NotNil(t, arguments)
	assert.Equal(t, CauseToolArgumentError, arguments.RootCauses[0].Category)
}

// TestMetricCategoryHintOverride: hints beat criterion-derived categories.
func TestMetricCategoryHintOverride(t *testing.T) {
	attributor := NewAttributor(testMetrics(), map[string]string{
		"final_response_avg_score": string(CauseKnowledgeRecallGap),
	})
	attribution := attributor.Attribute(failedSnapshot(
		"hinted", failedMetric("final_response_avg_score", "missing facts"),
	), nil)
	require.NotNil(t, attribution)
	assert.Equal(t, CauseKnowledgeRecallGap, attribution.RootCauses[0].Category)
}

// TestAttributionStats counts root causes by category.
func TestAttributionStats(t *testing.T) {
	stats := AttributionStats([]CaseAttribution{
		{RootCauses: []FailureCause{{Category: CauseToolCallError}}},
		{RootCauses: []FailureCause{{Category: CauseToolCallError}, {Category: CauseFormatError}}},
	})
	assert.Equal(t, 2, stats[CauseToolCallError])
	assert.Equal(t, 1, stats[CauseFormatError])
}

// TestDeriveRubricCategoryMixed: mixed rubric types stay response-quality.
func TestDeriveRubricCategoryMixed(t *testing.T) {
	attributor := NewAttributor(testMetrics(), nil)
	attribution := attributor.Attribute(failedSnapshot(
		"mixed", failedMetric("quality_rubric", "rubric failed"),
	), nil)
	require.NotNil(t, attribution)
	assert.Equal(t, CauseFinalResponseMismatch, attribution.RootCauses[0].Category)
}
