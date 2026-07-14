//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"math"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestCompareAllTransitions(t *testing.T) {
	baseline := testEvaluation(0.5, map[string]testCaseState{
		"new-pass": {0, false}, "new-fail": {1, true}, "improved": {0.2, false},
		"regressed": {0.8, true}, "same-pass": {1, true}, "same-fail": {0, false},
	})
	candidate := testEvaluation(0.6, map[string]testCaseState{
		"new-pass": {1, true}, "new-fail": {0, false}, "improved": {0.6, false},
		"regressed": {0.4, true}, "same-pass": {1, true}, "same-fail": {0, false},
	})
	want := map[string]Transition{
		"new-pass": TransitionNewlyPassed, "new-fail": TransitionNewlyFailed,
		"improved": TransitionImproved, "regressed": TransitionRegressed,
		"same-pass": TransitionUnchangedPass, "same-fail": TransitionUnchangedFail,
	}
	got := Compare(baseline, candidate)
	if math.Abs(got.ScoreDelta-0.1) > 1e-9 {
		t.Fatalf("score delta = %v", got.ScoreDelta)
	}
	for _, item := range got.PerCase {
		if item.Transition != want[item.CaseID] {
			t.Errorf("%s transition = %s, want %s", item.CaseID, item.Transition, want[item.CaseID])
		}
	}
}

type testCaseState struct {
	score float64
	pass  bool
}

func testEvaluation(overall float64, states map[string]testCaseState) *engine.EvaluationResult {
	cases := make([]engine.CaseResult, 0, len(states))
	for id, state := range states {
		metricStatus := status.EvalStatusFailed
		reason := "failed"
		if state.pass {
			metricStatus = status.EvalStatusPassed
			reason = ""
		}
		cases = append(cases, engine.CaseResult{EvalCaseID: id, Metrics: []engine.MetricResult{{MetricName: "quality", Score: state.score, Status: metricStatus, Reason: reason}}})
	}
	return &engine.EvaluationResult{OverallScore: overall, EvalSets: []engine.EvalSetResult{{EvalSetID: "set", OverallScore: overall, Cases: cases}}}
}
