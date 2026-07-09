//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestCategorize(t *testing.T) {
	cases := []struct {
		name   string
		metric string
		reason string
		want   FailureCategory
	}{
		{"response mismatch", "final_response_avg_score", "text mismatch", CategoryResponseMismatch},
		{"rouge mismatch", "rouge_avg", "rouge mismatch: low overlap", CategoryResponseMismatch},
		{"format from reason", "final_response_avg_score", "output must be valid JSON schema", CategoryFormatError},
		{"format chinese", "final_response_avg_score", "格式不合规", CategoryFormatError},
		{"knowledge recall", "final_response_avg_score", "missing knowledge recall / grounding", CategoryKnowledgeRecall},
		{"tool error", "tool_trajectory_avg_score", "expected tool call missing", CategoryToolError},
		{"tool arg error", "tool_trajectory_avg_score", "argument mismatch on query", CategoryToolArgError},
		{"route error", "tool_trajectory_avg_score", "wrong agent route", CategoryRouteError},
		{"other", "custom_metric", "something else", CategoryOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := categorize(tc.metric, tc.reason); got != tc.want {
				t.Fatalf("categorize(%q,%q)=%s want %s", tc.metric, tc.reason, got, tc.want)
			}
		})
	}
}

func TestAttributeCountsOnlyFailures(t *testing.T) {
	result := evalR(0.33,
		caseR("c1", metricR("final_response_avg_score", 0, status.EvalStatusFailed, "text mismatch")),
		caseR("c2", metricR("final_response_avg_score", 1, status.EvalStatusPassed, "")),
		caseR("c3", metricR("tool_trajectory_avg_score", 0, status.EvalStatusFailed, "argument mismatch")),
	)
	got := Attribute(result)
	if got.Baseline[CategoryResponseMismatch] != 1 {
		t.Fatalf("responseMismatch=%d want 1", got.Baseline[CategoryResponseMismatch])
	}
	if got.Baseline[CategoryToolArgError] != 1 {
		t.Fatalf("toolArgError=%d want 1", got.Baseline[CategoryToolArgError])
	}
	if len(got.Details) != 2 {
		t.Fatalf("details=%d want 2 (passed case excluded)", len(got.Details))
	}
}

func TestAttributeNil(t *testing.T) {
	got := Attribute(nil)
	if len(got.Baseline) != 0 || len(got.Details) != 0 {
		t.Fatalf("nil result should yield empty attribution")
	}
}

func TestSeverityCounts(t *testing.T) {
	rounds := []engine.RoundResult{
		lossRound(1, nil, false, 0, promptiter.LossSeverityP1, promptiter.LossSeverityP1, promptiter.LossSeverityP2),
	}
	got := severityCounts(rounds)
	if got["P1"] != 2 || got["P2"] != 1 {
		t.Fatalf("severity counts=%v want P1=2 P2=1", got)
	}
}
