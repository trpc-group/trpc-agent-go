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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestGateRejectsRetainedHardFailAndCriticalFailure(t *testing.T) {
	decision := decide(gateInput{
		Policy: gatePolicy{
			MinGain:         0.1,
			MaxHardFailures: 0,
			Critical: []criticalRule{{
				EvalCaseID: "critical",
				MustPass:   true,
			}},
		},
		Baseline:  testSnapshot(0.5, testCase("critical", status.EvalStatusPassed, 0.5)),
		Candidate: testSnapshot(0.8, testCase("critical", status.EvalStatusFailed, 0.8)),
		Usage: usageSummary{
			ModelCalls: knownInt(1),
		},
	})

	assert.False(t, decision.Accepted)
	assert.ElementsMatch(t, []string{"hard_failures", "critical_case"}, failedCheckIDs(decision))
}

func TestGateTreatsExplicitZeroAsLimit(t *testing.T) {
	zero := 0
	decision := decide(gateInput{
		Policy: gatePolicy{
			MaxModelCalls: &zero,
		},
		Baseline:  testSnapshot(0.5, testCase("critical", status.EvalStatusPassed, 0.5)),
		Candidate: testSnapshot(0.5, testCase("critical", status.EvalStatusPassed, 0.5)),
		Usage: usageSummary{
			ModelCalls: knownInt(1),
		},
	})

	assert.False(t, decision.Accepted)
	assert.Contains(t, failedCheckIDs(decision), "model_calls")
}

func TestGateFailsClosedForUnknownBudgetMeasurement(t *testing.T) {
	limit := 10
	decision := decide(gateInput{
		Policy:    gatePolicy{MaxTokens: &limit},
		Baseline:  testSnapshot(0.5, testCase("critical", status.EvalStatusPassed, 0.5)),
		Candidate: testSnapshot(0.5, testCase("critical", status.EvalStatusPassed, 0.5)),
	})

	assert.False(t, decision.Accepted)
	assert.Contains(t, failedCheckIDs(decision), "tokens")
}

func TestGateCriticalCaseReasonOmitsEmptyMetricSeparator(t *testing.T) {
	decision := decide(gateInput{
		Policy: gatePolicy{Critical: []criticalRule{{
			EvalCaseID: "critical", MustPass: true,
		}}},
		Baseline:  testSnapshot(0.5, testCase("critical", status.EvalStatusPassed, 0.5)),
		Candidate: testSnapshot(0.5, testCase("critical", status.EvalStatusFailed, 0.5)),
	})

	for _, check := range decision.Checks {
		if check.ID == "critical_case" {
			assert.Equal(t, "critical evidence critical did not pass", check.Observed)
			return
		}
	}
	t.Fatal("critical_case check not found")
}

func TestGateCountsFailedMetricWhenCaseStatusIsInconsistent(t *testing.T) {
	candidateCase := testCase("critical", status.EvalStatusPassed, 0.5)
	candidateCase.Metrics[0].Status = status.EvalStatusFailed
	decision := decide(gateInput{
		Policy:    gatePolicy{MaxHardFailures: 0},
		Baseline:  testSnapshot(0.5, testCase("critical", status.EvalStatusPassed, 0.5)),
		Candidate: testSnapshot(0.5, candidateCase),
	})

	assert.Contains(t, failedCheckIDs(decision), "hard_failures")
	assert.Contains(t, failedCheckIDs(decision), "evidence_shape")
}

func testSnapshot(score float64, cases ...caseResult) evaluationSnapshot {
	return evaluationSnapshot{
		EvalSetID: "validation",
		Status:    status.EvalStatusPassed,
		Score:     score,
		Cases:     cases,
	}
}

func testCase(id string, evalStatus status.EvalStatus, score float64) caseResult {
	return caseResult{
		EvalSetID:  "validation",
		EvalCaseID: id,
		Status:     evalStatus,
		Score:      score,
		Metrics: []metricResult{{
			Name:   "quality",
			Score:  score,
			Status: evalStatus,
		}},
	}
}

func failedCheckIDs(decision gateDecision) []string {
	var result []string
	for _, check := range decision.Checks {
		if !check.Passed {
			result = append(result, check.ID)
		}
	}
	return result
}
