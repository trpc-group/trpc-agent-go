//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"strings"
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

func TestAttributeEmptyReasonGetsFallback(t *testing.T) {
	result := evalR(0.0, caseR("c1", metricR("final_response_avg_score", 0, status.EvalStatusFailed, "")))
	got := Attribute(result)
	if len(got.Details) != 1 {
		t.Fatalf("want 1 detail, got %d", len(got.Details))
	}
	if strings.TrimSpace(got.Details[0].Reason) == "" {
		t.Fatalf("empty evaluator reason must get a non-empty fallback reason")
	}
}

// TestAttributionBenchmarkAccuracy runs the classifier over independent,
// varied-phrasing evaluator reasons (Chinese/English synonyms, conflicting
// signals, unknown cases) and asserts a proxy accuracy >= 0.75. It deliberately
// includes ambiguous cases the heuristic gets wrong, so the score is honest.
func TestAttributionBenchmarkAccuracy(t *testing.T) {
	cases := []struct {
		metric string
		reason string
		want   FailureCategory
	}{
		{"final_response_avg_score", "final response does not match the expected answer", CategoryResponseMismatch},
		{"final_response_avg_score", "生成的回复与参考答案不一致", CategoryResponseMismatch},
		{"rouge_zh_avg", "低 ROUGE 重叠，覆盖不足", CategoryResponseMismatch},
		{"final_response_avg_score", "output is not valid JSON", CategoryFormatError},
		{"final_response_avg_score", "回复包含 markdown，应输出纯文本", CategoryFormatError},
		{"xml_structure", "XML 结构缺少必需字段", CategoryFormatError},
		{"tool_trajectory_avg_score", "expected a tool call but none was made", CategoryToolError},
		{"tool_trajectory_avg_score", "agent 未调用工具就直接作答", CategoryToolError},
		{"tool_trajectory_avg_score", "called the wrong tool for this step", CategoryToolError},
		{"tool_trajectory_avg_score", "工具参数 city 传错了", CategoryToolArgError},
		{"tool_trajectory_avg_score", "tool invoked with an invalid argument value", CategoryToolArgError},
		{"tool_trajectory_avg_score", "转交给了错误的下游节点", CategoryRouteError},
		{"tool_trajectory_avg_score", "request was misrouted to the summary node", CategoryRouteError},
		{"route_check", "wrong agent handled the query", CategoryRouteError},
		{"grounding_avg", "答案缺乏检索到的知识支撑", CategoryKnowledgeRecall},
		{"hallucination_avg", "response is unsupported by the retrieved context", CategoryKnowledgeRecall},
		{"final_response_avg_score", "模型凭空编造了事实", CategoryKnowledgeRecall},
		{"custom_metric", "unexpected internal error during scoring", CategoryOther},
		{"custom_metric", "评测超时", CategoryOther},
		// Deliberately ambiguous (heuristic likely misclassifies these):
		{"final_response_avg_score", "response format looks fine but the number is wrong", CategoryResponseMismatch},
		{"tool_trajectory_avg_score", "参数没问题，但工具选错了", CategoryToolError},
	}
	correct := 0
	for _, c := range cases {
		if categorize(c.metric, c.reason) == c.want {
			correct++
		}
	}
	acc := float64(correct) / float64(len(cases))
	t.Logf("attribution proxy accuracy: %.2f (%d/%d)", acc, correct, len(cases))
	if acc < 0.75 {
		t.Fatalf("attribution proxy accuracy %.2f < 0.75", acc)
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
