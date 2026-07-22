// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type testCaseSpec struct {
	id        string
	score     float64
	passed    bool
	threshold float64
}

func testEvaluation(evalSetID string, specs ...testCaseSpec) *EvaluationResult {
	result := &EvaluationResult{
		EvalSetID:     evalSetID,
		Cases:         make([]CaseResult, 0, len(specs)),
		Usage:         Usage{Measured: true},
		OverallStatus: status.EvalStatusPassed,
	}
	for _, spec := range specs {
		metricStatus := status.EvalStatusFailed
		if spec.passed {
			metricStatus = status.EvalStatusPassed
		} else {
			result.OverallStatus = status.EvalStatusFailed
		}
		threshold := spec.threshold
		if threshold == 0 {
			threshold = 0.5
		}
		result.Cases = append(result.Cases, CaseResult{
			EvalSetID: evalSetID,
			CaseID:    spec.id,
			Score:     spec.score,
			Passed:    spec.passed,
			Metrics: []MetricResult{{
				Name: "quality", Score: spec.score, Threshold: threshold, Status: metricStatus,
			}},
			Trace: Trace{Status: "completed", Usage: Usage{Measured: true}},
		})
		result.OverallScore += spec.score
	}
	if len(specs) > 0 {
		result.OverallScore /= float64(len(specs))
	}
	return result
}

func TestCompareClassifiesEveryTransition(t *testing.T) {
	baseline := testEvaluation("validation",
		testCaseSpec{id: "new-pass", score: 0, passed: false},
		testCaseSpec{id: "new-fail", score: 1, passed: true},
		testCaseSpec{id: "improved", score: 0.2, passed: false},
		testCaseSpec{id: "declined", score: 0.8, passed: true},
		testCaseSpec{id: "unchanged", score: 1, passed: true},
	)
	candidate := testEvaluation("validation",
		testCaseSpec{id: "new-pass", score: 1, passed: true},
		testCaseSpec{id: "new-fail", score: 0, passed: false},
		testCaseSpec{id: "improved", score: 0.4, passed: false},
		testCaseSpec{id: "declined", score: 0.6, passed: true},
		testCaseSpec{id: "unchanged", score: 1, passed: true},
	)
	delta, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	want := map[string]DeltaKind{
		"new-pass": DeltaNewPass, "new-fail": DeltaNewFail, "improved": DeltaImproved,
		"declined": DeltaDeclined, "unchanged": DeltaUnchanged,
	}
	for _, evalCase := range delta.Cases {
		if evalCase.Kind != want[evalCase.CaseID] {
			t.Errorf("case %q kind = %q, want %q", evalCase.CaseID, evalCase.Kind, want[evalCase.CaseID])
		}
		if len(evalCase.Metrics) != 1 || evalCase.Metrics[0].Kind != want[evalCase.CaseID] {
			t.Errorf("case %q metric transition was not preserved", evalCase.CaseID)
		}
	}
}

func TestCompareRejectsIncompleteOrChangedEvidence(t *testing.T) {
	baseline := testEvaluation("validation", testCaseSpec{id: "case-1", score: 1, passed: true})
	tests := []struct {
		name   string
		mutate func(*EvaluationResult)
		want   string
	}{
		{name: "missing case", mutate: func(result *EvaluationResult) { result.Cases = nil }, want: "has no cases"},
		{name: "duplicate case", mutate: func(result *EvaluationResult) { result.Cases = append(result.Cases, result.Cases[0]) }, want: "duplicate case"},
		{name: "missing metric", mutate: func(result *EvaluationResult) { result.Cases[0].Metrics = nil }, want: "has no metrics"},
		{name: "duplicate metric", mutate: func(result *EvaluationResult) {
			result.Cases[0].Metrics = append(result.Cases[0].Metrics, result.Cases[0].Metrics[0])
		}, want: "duplicate metric"},
		{name: "threshold drift", mutate: func(result *EvaluationResult) { result.Cases[0].Metrics[0].Threshold = 0.7 }, want: "threshold changed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := testEvaluation("validation", testCaseSpec{id: "case-1", score: 1, passed: true})
			test.mutate(candidate)
			_, err := Compare(baseline, candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compare() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
