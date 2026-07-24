//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "testing"

func TestCompareEvaluationsClassifiesCaseDeltas(t *testing.T) {
	baseline := evaluationSummary{
		Score: 0.48,
		Cases: []caseEvaluation{
			{CaseID: "new-pass", Score: 0.25, Passed: false},
			{CaseID: "new-fail", Score: 1.00, Passed: true},
			{CaseID: "improved", Score: 0.40, Passed: false},
			{CaseID: "regressed", Score: 0.80, Passed: false},
			{CaseID: "unchanged", Score: 0.50, Passed: false},
		},
	}
	candidate := evaluationSummary{
		Score: 0.63,
		Cases: []caseEvaluation{
			{CaseID: "new-pass", Score: 1.00, Passed: true},
			{CaseID: "new-fail", Score: 0.50, Passed: false},
			{CaseID: "improved", Score: 0.75, Passed: false},
			{CaseID: "regressed", Score: 0.60, Passed: false},
			{CaseID: "unchanged", Score: 0.50, Passed: false},
		},
	}

	delta, err := compareEvaluations(baseline, candidate)
	if err != nil {
		t.Fatalf("compareEvaluations returned error: %v", err)
	}
	if delta.ScoreDelta != 0.15 {
		t.Fatalf("score delta = %.2f, want 0.15", delta.ScoreDelta)
	}
	assertDeltaClass(t, delta, "new-pass", caseNewlyPassed)
	assertDeltaClass(t, delta, "new-fail", caseNewlyFailed)
	assertDeltaClass(t, delta, "improved", caseImproved)
	assertDeltaClass(t, delta, "regressed", caseRegressed)
	assertDeltaClass(t, delta, "unchanged", caseUnchanged)
	if delta.NewlyPassed != 1 || delta.NewlyFailed != 1 || delta.Improved != 1 || delta.Regressed != 1 {
		t.Fatalf("unexpected delta counts: %+v", delta)
	}
}

func TestCompareEvaluationsRejectsMismatchedCases(t *testing.T) {
	_, err := compareEvaluations(
		evaluationSummary{Cases: []caseEvaluation{{CaseID: "only-baseline"}}},
		evaluationSummary{Cases: []caseEvaluation{{CaseID: "only-candidate"}}},
	)
	if err == nil {
		t.Fatal("compareEvaluations returned nil error")
	}
}

func assertDeltaClass(t *testing.T, delta evaluationDelta, caseID string, want caseDeltaClass) {
	t.Helper()
	for _, item := range delta.Cases {
		if item.CaseID == caseID {
			if item.Class != want {
				t.Fatalf("case %q class = %q, want %q", caseID, item.Class, want)
			}
			return
		}
	}
	t.Fatalf("case %q not found", caseID)
}
