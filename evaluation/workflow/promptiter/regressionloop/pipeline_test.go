//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineRunsBaselineBeforeCandidatesAndIsReproducible(t *testing.T) {
	cfg := testConfig(t)
	evaluator := &fakeEvaluator{results: map[string]EvaluationSummary{
		phaseKey(PhaseBaselineTrain, 0):       evalSummary(0.5, caseResult("train", 0.5, false)),
		phaseKey(PhaseBaselineValidation, 0):  evalSummary(0.6, caseResult("valid", 0.6, true)),
		phaseKey(PhaseCandidateTrain, 1):      evalSummary(0.8, caseResult("train", 0.8, true)),
		phaseKey(PhaseCandidateValidation, 1): evalSummary(0.8, caseResult("valid", 0.8, true)),
	}}
	pipeline := &Pipeline{
		Evaluator: evaluator,
		Optimizer: fakeOptimizer{candidates: []Candidate{{Round: 1, Prompt: "better"}}},
		Clock: &fixedClock{ticks: []time.Time{
			time.Unix(100, 0).UTC(),
			time.Unix(101, 0).UTC(),
		}},
	}

	first, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, PhaseBaselineTrain, evaluator.calls[0].Phase)
	assert.Equal(t, PhaseBaselineValidation, evaluator.calls[1].Phase)
	assert.Equal(t, PhaseCandidateTrain, evaluator.calls[2].Phase)
	assert.Equal(t, PhaseCandidateValidation, evaluator.calls[3].Phase)
	assert.True(t, first.Report.GateDecision.Accepted)

	evaluator.calls = nil
	pipeline.Clock = &fixedClock{ticks: []time.Time{time.Unix(100, 0).UTC(), time.Unix(101, 0).UTC()}}
	second, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, first.Report.Run.StartedAt, second.Report.Run.StartedAt)
	assert.Equal(t, first.Report.GateDecision, second.Report.GateDecision)
}

func TestPipelineRejectsOverfitCandidate(t *testing.T) {
	cfg := testConfig(t)
	evaluator := &fakeEvaluator{results: map[string]EvaluationSummary{
		phaseKey(PhaseBaselineTrain, 0):      evalSummary(0.4, caseResult("train", 0.4, false)),
		phaseKey(PhaseBaselineValidation, 0): evalSummary(0.8, caseResult("critical", 0.8, true)),
		phaseKey(PhaseCandidateTrain, 1):     evalSummary(0.9, caseResult("train", 0.9, true)),
		phaseKey(PhaseCandidateValidation, 1): evalSummary(0.5, CaseResult{
			EvalID:   "critical",
			Critical: true,
			Score:    0,
			Passed:   false,
			HardFail: true,
			MetricResults: []MetricResult{{
				Name:     "quality",
				Score:    0,
				Passed:   false,
				HardFail: true,
				Reason:   "format error",
			}},
			FailureReasons: []string{"format error"},
		}),
	}}
	pipeline := &Pipeline{
		Evaluator: evaluator,
		Optimizer: fakeOptimizer{candidates: []Candidate{{Round: 1, Prompt: "overfit"}}},
		Clock: &fixedClock{ticks: []time.Time{
			time.Unix(100, 0).UTC(),
			time.Unix(101, 0).UTC(),
		}},
	}

	result, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.False(t, result.Report.GateDecision.Accepted)
	assert.Contains(t, result.Report.GateDecision.Reasons, "no candidate accepted by gate")
	require.Len(t, result.Report.Candidates, 1)
	assert.Contains(t, result.Report.Candidates[0].GateDecision.FailedRules, "no_new_hard_fails")
	assert.Contains(t, result.Report.Candidates[0].GateDecision.FailedRules, "critical_case_non_regression")
	assert.Nil(t, result.Report.SelectedCandidate)
}

func TestPipelineFinalReportUsesSelectedAcceptedCandidate(t *testing.T) {
	cfg := testConfig(t)
	cfg.Gate.MaxCalls = 2
	evaluator := &fakeEvaluator{results: map[string]EvaluationSummary{
		phaseKey(PhaseBaselineTrain, 0):      evalSummary(0.5, caseResult("train", 0.5, false)),
		phaseKey(PhaseBaselineValidation, 0): evalSummary(0.6, caseResult("valid", 0.6, true)),
		phaseKey(PhaseCandidateTrain, 1):     evalSummary(0.7, caseResult("train", 0.7, true)),
		phaseKey(PhaseCandidateValidation, 1): evalSummary(0.75,
			caseResult("valid", 0.75, true),
		),
		phaseKey(PhaseCandidateTrain, 2): evalSummary(0.95,
			caseResult("train-a", 0.95, true),
			caseResult("train-b", 0.95, true),
		),
		phaseKey(PhaseCandidateValidation, 2): evalSummary(0.9,
			caseResult("valid", 0.9, true),
			caseResult("extra", 0.9, true),
		),
	}}
	pipeline := &Pipeline{
		Evaluator: evaluator,
		Optimizer: fakeOptimizer{candidates: []Candidate{
			{Round: 1, Prompt: "accepted"},
			{Round: 2, Prompt: "higher-score-but-too-expensive"},
		}},
		Clock: &fixedClock{ticks: []time.Time{
			time.Unix(100, 0).UTC(),
			time.Unix(101, 0).UTC(),
		}},
	}

	result, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, result.Report.SelectedCandidate)
	assert.Equal(t, 1, result.Report.SelectedCandidate.Round)
	assert.True(t, result.Report.GateDecision.Accepted)
	assert.Equal(t, result.Report.SelectedCandidate.GateDecision, result.Report.GateDecision)
	require.Len(t, result.Report.Delta.Cases, 1)
	assert.Equal(t, 0.75, result.Report.Delta.Cases[0].CandidateScore)
	assert.False(t, result.Report.Candidates[1].GateDecision.Accepted)
	assert.Contains(t, result.Report.Candidates[1].GateDecision.FailedRules, "max_calls")
}
