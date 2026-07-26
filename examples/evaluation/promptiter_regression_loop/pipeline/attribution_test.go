//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestClassifyMetricAllCategories(t *testing.T) {
	tests := []struct {
		name   string
		crit   *criterion.Criterion
		reason string
		want   FailureCategory
	}{
		{"final response text mismatch", critFinalResponseText(), "", CategoryResponseMismatch},
		{"final response json invalid is format", critFinalResponseJSON(), "output is invalid json", CategoryFormatError},
		{"final response json wrong value is mismatch", critFinalResponseJSON(), "field value differs", CategoryResponseMismatch},
		{"tool trajectory missing call", critToolTrajectory(), "tool trajectory sequence mismatch (missing call)", CategoryToolCallError},
		{"tool trajectory arg", critToolTrajectory(), "argument mismatch on the called tool", CategoryToolArgError},
		{"tool trajectory route", critToolTrajectory(), "routed to the wrong tool name", CategoryRouteError},
		{"llm judge knowledge", critLLMJudge(), "rubric: answer is missing information", CategoryKnowledgeRecall},
		{"llm judge parse is format", critLLMJudge(), "judge response failed to parse", CategoryFormatError},
		{"no criterion falls back to reason", nil, "tool call missing in trajectory", CategoryToolCallError},
		{"no criterion no signal is unknown", nil, "", CategoryUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := makeCase("c", false, 0, tc.crit, tc.reason)
			got := classifyMetric(c.MetricResults[0], tc.reason)
			if got != tc.want {
				t.Fatalf("classifyMetric(%q) = %q, want %q", tc.reason, got, tc.want)
			}
		})
	}
}

// TestAttributeCaseReadsReasonFromRetainedPerRunDetails reproduces REAL evaluator output:
// aggregateCaseRuns copies Criterion but NOT Details into the aggregated MetricResults, so the
// driver metric has nil Details and the reason lives only on the per-run EvalCaseResults. Attribution
// must recover the reason from there, otherwise the three reason-disambiguated categories collapse to
// their criterion-type default (tool_arg/route → tool_call, format → mismatch/knowledge).
func TestAttributeCaseReadsReasonFromRetainedPerRunDetails(t *testing.T) {
	tests := []struct {
		name   string
		crit   *criterion.Criterion
		reason string
		want   FailureCategory
	}{
		{"tool arg", critToolTrajectory(), "argument mismatch on the called tool", CategoryToolArgError},
		{"tool route", critToolTrajectory(), "routed to the wrong tool name", CategoryRouteError},
		{"format json", critFinalResponseJSON(), "output is invalid json", CategoryFormatError},
		{"format judge", critLLMJudge(), "judge response failed to parse", CategoryFormatError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &evaluation.EvaluationCaseResult{
				EvalCaseID:    "c",
				OverallStatus: status.EvalStatusFailed,
				// Aggregated metric as the framework builds it: Criterion set, Details nil.
				MetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "m", EvalStatus: status.EvalStatusFailed, Criterion: tc.crit},
				},
				// Per-run result retains the reason.
				EvalCaseResults: []*evalresult.EvalCaseResult{
					{OverallEvalMetricResults: []*evalresult.EvalMetricResult{
						{MetricName: "m", EvalStatus: status.EvalStatusFailed, Criterion: tc.crit,
							Details: &evalresult.EvalMetricResultDetails{Reason: tc.reason}},
					}},
				},
			}
			attrs := AttributeResult(makeResult("s", c))
			if attrs[0].Category != tc.want {
				t.Fatalf("category = %q, want %q (reason %q was not recovered from per-run details)", attrs[0].Category, tc.want, tc.reason)
			}
			if attrs[0].Reason != tc.reason {
				t.Errorf("reason = %q, want %q", attrs[0].Reason, tc.reason)
			}
		})
	}
}

func TestAttributeResultPassingCaseHasNoCategory(t *testing.T) {
	result := makeResult("s",
		makeCase("pass_case", true, 1.0, critFinalResponseText(), ""),
		makeCase("fail_case", false, 0.0, critFinalResponseText(), ""),
	)
	attrs := AttributeResult(result)
	if len(attrs) != 2 {
		t.Fatalf("expected 2 attributions, got %d", len(attrs))
	}
	if !attrs[0].Passed || attrs[0].Category != CategoryNone {
		t.Errorf("passing case: got passed=%t category=%q, want passed=true category=none", attrs[0].Passed, attrs[0].Category)
	}
	if attrs[1].Passed || attrs[1].Category != CategoryResponseMismatch {
		t.Errorf("failing case: got passed=%t category=%q, want passed=false category=response_mismatch", attrs[1].Passed, attrs[1].Category)
	}
}

func TestAttributeResultNilIsNil(t *testing.T) {
	if got := AttributeResult(nil); got != nil {
		t.Fatalf("AttributeResult(nil) = %v, want nil", got)
	}
}

func TestAttributeResultScoreIsMeanOfMetrics(t *testing.T) {
	result := makeResult("s", makeCase("c", false, 0.25, critFinalResponseText(), ""))
	attrs := AttributeResult(result)
	if attrs[0].Score != 0.25 {
		t.Fatalf("score = %v, want 0.25", attrs[0].Score)
	}
}

func TestAttributionStatsCountsPerCategoryExcludingPasses(t *testing.T) {
	result := makeResult("s",
		makeCase("p", true, 1.0, critFinalResponseText(), ""),
		makeCase("a", false, 0.0, critFinalResponseText(), ""),
		makeCase("b", false, 0.0, critFinalResponseText(), ""),
		makeCase("c", false, 0.0, critToolTrajectory(), "argument mismatch on the called tool"),
	)
	stats := AttributionStats(result)
	got := map[FailureCategory]int{}
	for _, s := range stats {
		got[s.Category] = s.Count
	}
	if got[CategoryResponseMismatch] != 2 {
		t.Errorf("response_mismatch count = %d, want 2", got[CategoryResponseMismatch])
	}
	if got[CategoryToolArgError] != 1 {
		t.Errorf("tool_arg_error count = %d, want 1", got[CategoryToolArgError])
	}
	if _, ok := got[CategoryNone]; ok {
		t.Errorf("passing cases must not appear in stats: %v", got)
	}
	// Deterministic ordering by category name.
	for i := 1; i < len(stats); i++ {
		if stats[i-1].Category > stats[i].Category {
			t.Errorf("stats not sorted by category: %v", stats)
		}
	}
}

func TestAttributionStatsEmptyWhenAllPass(t *testing.T) {
	result := makeResult("s", makeCase("p", true, 1.0, critFinalResponseText(), ""))
	if stats := AttributionStats(result); len(stats) != 0 {
		t.Fatalf("expected empty stats, got %v", stats)
	}
}
