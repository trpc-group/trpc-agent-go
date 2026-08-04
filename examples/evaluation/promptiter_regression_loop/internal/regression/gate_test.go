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

func TestDecideAcceptsValidationImprovement(t *testing.T) {
	baseline := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 0, passed: false},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	candidate := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 1, passed: true},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	decision, err := Decide(testGatePolicy(), GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if !decision.Accepted {
		t.Fatalf("decision = %+v, want accepted", decision)
	}
	if len(decision.Reasons) != 1 || decision.Reasons[0] != "candidate satisfies every configured release gate" {
		t.Fatalf("decision reasons = %v, want successful-gate audit reason", decision.Reasons)
	}
	if decision.ScoreDelta != 0.5 {
		t.Fatalf("score delta = %v, want 0.5", decision.ScoreDelta)
	}
}

func TestDecideRejectsNoGain(t *testing.T) {
	baseline := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 0, passed: false},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	decision, err := Decide(testGatePolicy(), GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: baseline,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Accepted || !reasonsContain(decision.Reasons, "below required") {
		t.Fatalf("decision = %+v, want insufficient-gain rejection", decision)
	}
}

func TestDecideRejectsOverfitCriticalRegression(t *testing.T) {
	original := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 0, passed: false},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	accepted := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 1, passed: true},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	overfit := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 1, passed: true},
		testCaseSpec{id: "security", score: 0, passed: false},
	)
	decision, err := Decide(testGatePolicy(), GateInput{
		OriginalBaseline: original, AcceptedBaseline: accepted, Candidate: overfit,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Accepted || len(decision.NewFailures) == 0 || len(decision.CriticalRegressions) == 0 {
		t.Fatalf("decision = %+v, want critical-regression rejection", decision)
	}
}

func TestDecideRejectsUnknownUsageWhenBudgetEnabled(t *testing.T) {
	baseline := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 0, passed: false},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	candidate := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 1, passed: true},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	candidate.Usage.Measured = false
	decision, err := Decide(testGatePolicy(), GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Accepted || !reasonsContain(decision.Reasons, "usage is not measured") {
		t.Fatalf("decision = %+v, want unknown-usage rejection", decision)
	}
}

func TestDecideRejectsUnknownOverallStatus(t *testing.T) {
	baseline := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 0, passed: false},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	candidate := testEvaluation("validation",
		testCaseSpec{id: "ordinary", score: 1, passed: true},
		testCaseSpec{id: "security", score: 1, passed: true},
	)
	candidate.OverallStatus = status.EvalStatusUnknown
	decision, err := Decide(testGatePolicy(), GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Accepted || !reasonsContain(decision.Reasons, "overall evaluation status") {
		t.Fatalf("decision = %+v, want unknown-status rejection", decision)
	}
}

func TestDecideRejectsIncompleteBaselineEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EvaluationResult)
	}{
		{
			name: "unknown metric",
			mutate: func(result *EvaluationResult) {
				result.Cases[0].Metrics[0].Status = status.EvalStatusUnknown
			},
		},
		{
			name: "incomplete trace",
			mutate: func(result *EvaluationResult) {
				result.Cases[0].Trace.Status = "incomplete"
			},
		},
		{
			name: "execution error",
			mutate: func(result *EvaluationResult) {
				result.Cases[0].ErrorMessage = "runner failed"
			},
		},
	}
	for _, baselineName := range []string{"original", "accepted"} {
		for _, test := range tests {
			t.Run(baselineName+"/"+test.name, func(t *testing.T) {
				original := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 0, passed: false})
				accepted := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 0, passed: false})
				if baselineName == "original" {
					test.mutate(original)
				} else {
					test.mutate(accepted)
				}
				candidate := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 1, passed: true})
				_, err := Decide(GatePolicy{MinValidationScoreGain: 0.2}, GateInput{
					OriginalBaseline: original, AcceptedBaseline: accepted, Candidate: candidate,
				})
				if err == nil || !strings.Contains(err.Error(), baselineName+" baseline") {
					t.Fatalf("Decide() error = %v, want invalid %s-baseline error", err, baselineName)
				}
			})
		}
	}
}

func TestDecideAllowsFailedBaselineQualityMetric(t *testing.T) {
	baseline := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 0, passed: false})
	candidate := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 1, passed: true})
	decision, err := Decide(GatePolicy{MinValidationScoreGain: 0.2}, GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if !decision.Accepted {
		t.Fatalf("decision = %+v, want ordinary failed baseline metric to remain comparable", decision)
	}
}

func TestDecideEnforcesExplicitZeroToolCallBudget(t *testing.T) {
	baseline := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 0, passed: false})
	candidate := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 1, passed: true})
	candidate.Usage.ToolCalls = 1
	decision, err := Decide(GatePolicy{
		MinValidationScoreGain: 0.2,
		MaxValidationToolCalls: intPointer(0),
	}, GateInput{OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Accepted || !reasonsContain(decision.Reasons, "tool calls 1 exceed budget 0") {
		t.Fatalf("decision = %+v, want explicit zero-budget rejection", decision)
	}
}

func TestDecideLeavesOmittedBudgetsDisabled(t *testing.T) {
	baseline := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 0, passed: false})
	candidate := testEvaluation("validation", testCaseSpec{id: "ordinary", score: 1, passed: true})
	candidate.Usage = Usage{TotalTokens: 1, ModelCalls: 1, ToolCalls: 1}
	decision, err := Decide(GatePolicy{MinValidationScoreGain: 0.2}, GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if !decision.Accepted {
		t.Fatalf("decision = %+v, want omitted budgets disabled", decision)
	}
}

func TestAddUsagePreservesMeasurementProvenance(t *testing.T) {
	got := AddUsage(Usage{TotalTokens: 10, Measured: true}, Usage{TotalTokens: 5, Measured: false})
	if got.TotalTokens != 15 || got.Measured {
		t.Fatalf("AddUsage() = %+v, want total 15 and unmeasured", got)
	}
	if got := normalizeTrace(nil).Usage; got.Measured {
		t.Fatalf("nil trace usage = %+v, want unmeasured", got)
	}
}

func testGatePolicy() GatePolicy {
	return GatePolicy{
		MinValidationScoreGain:    0.2,
		RejectNewFailures:         true,
		RejectCriticalRegressions: true,
		CriticalCaseIDs:           []string{"security"},
		MaxValidationTokens:       intPointer(100),
	}
}

func intPointer(value int) *int {
	return &value
}

func reasonsContain(reasons []string, value string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, value) {
			return true
		}
	}
	return false
}
