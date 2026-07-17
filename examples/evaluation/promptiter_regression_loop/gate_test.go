//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestGateRejectsValidationRegression(t *testing.T) {
	delta := DeltaSummary{
		BaselineScore:     0.8,
		CandidateScore:    0.7,
		ScoreDelta:        -0.1,
		NewlyFailed:       1,
		CriticalRegressed: 1,
	}
	decision := DecideGate(GateConfig{
		MinValidationGain:        0.05,
		MaxNewHardFails:          0,
		RejectCriticalRegression: true,
	}, delta, CostSummary{TotalCalls: 4})
	require.False(t, decision.Accepted)
	require.Contains(t, strings.Join(decision.Reasons, "\n"), "critical validation case")
}

func TestGateAcceptsCleanValidationGain(t *testing.T) {
	delta := DeltaSummary{
		BaselineScore:  0.7,
		CandidateScore: 0.9,
		ScoreDelta:     0.2,
	}
	decision := DecideGate(GateConfig{
		MinValidationGain:        0.05,
		MaxNewHardFails:          0,
		RejectCriticalRegression: true,
		MaxCalls:                 10,
	}, delta, CostSummary{TotalCalls: 4})
	require.True(t, decision.Accepted)
	require.Contains(t, decision.Reasons[0], "passed")
}

func TestComputeDeltaClassifiesFixedAndRegressed(t *testing.T) {
	baseline := EvaluationRun{
		OverallScore: 0.5,
		Cases: []CaseResult{
			testCase("fixed", false, 0, status.EvalStatusFailed),
			testCase("regressed", true, 1, status.EvalStatusPassed),
		},
	}
	candidate := EvaluationRun{
		OverallScore: 0.5,
		Cases: []CaseResult{
			testCase("fixed", false, 1, status.EvalStatusPassed),
			testCase("regressed", true, 0, status.EvalStatusFailed),
		},
	}
	delta := ComputeDelta(baseline, candidate)
	require.Equal(t, 1, delta.NewlyPassed)
	require.Equal(t, 1, delta.NewlyFailed)
	require.Equal(t, 1, delta.CriticalRegressed)
	require.Equal(t, TransitionFixed, delta.Cases[0].Transition)
	require.Equal(t, TransitionRegressed, delta.Cases[1].Transition)
}

func TestAttributeFailuresFromMetricReason(t *testing.T) {
	failures := AttributeFailures([]MetricResult{
		{
			MetricName: "tool_trajectory_exact",
			Status:     status.EvalStatusFailed,
			Reason:     "tool argument error: city should be Paris",
		},
		{
			MetricName: "format_json",
			Status:     status.EvalStatusFailed,
			Reason:     "format error: expected JSON object",
		},
	}, Invocation{}, Invocation{})
	require.Len(t, failures, 2)
	require.Equal(t, FailureToolArgumentError, failures[0].Category)
	require.Equal(t, FailureFormatError, failures[1].Category)
}

func TestRenderMarkdownReportIncludesDecisionAndDelta(t *testing.T) {
	report := &OptimizationReport{
		RunID:           "run-1",
		AppName:         "app",
		Mode:            "deterministic",
		DataSource:      "fake",
		TargetSurfaceID: "agent#instruction",
		FakeEngine:      FakeEngineConfig{Name: "fake", Model: "fake-model"},
		BaselineTrain:   EvaluationRun{OverallScore: 0.4},
		Candidate: CandidateSummary{
			ID:              "candidate-1",
			TrainEvaluation: EvaluationRun{OverallScore: 0.8},
		},
		Delta: DeltaSummary{
			BaselineScore:  0.7,
			CandidateScore: 0.6,
			ScoreDelta:     -0.1,
			Cases: []CaseDelta{{
				CaseID:         "case-1",
				BaselineScore:  1,
				CandidateScore: 0,
				Transition:     TransitionRegressed,
			}},
		},
		Gate: GateDecision{
			Accepted: false,
			Reasons:  []string{"validation regressed"},
		},
	}
	markdown := RenderMarkdownReport(report)
	require.Contains(t, markdown, "Decision: **REJECT**")
	require.Contains(t, markdown, "`case-1`")
	require.Contains(t, markdown, "validation regressed")
}

func testCase(id string, critical bool, score float64, st status.EvalStatus) CaseResult {
	return CaseResult{
		CaseID:   id,
		Critical: critical,
		Score:    score,
		Status:   st,
	}
}
