//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestComputeDeltaClassifiesMovements(t *testing.T) {
	baseline := evalResult("validation", []caseSpec{
		{id: "fix", metric: "exact", score: 0, status: status.EvalStatusFailed},
		{id: "regress", metric: "exact", score: 1, status: status.EvalStatusPassed},
		{id: "soft_up", metric: "rubric", score: 0.4, status: status.EvalStatusFailed},
		{id: "soft_down", metric: "rubric", score: 0.8, status: status.EvalStatusFailed},
	})
	candidate := evalResult("validation", []caseSpec{
		{id: "fix", metric: "exact", score: 1, status: status.EvalStatusPassed},
		{id: "regress", metric: "exact", score: 0, status: status.EvalStatusFailed},
		{id: "soft_up", metric: "rubric", score: 0.7, status: status.EvalStatusFailed},
		{id: "soft_down", metric: "rubric", score: 0.6, status: status.EvalStatusFailed},
	})
	delta := ComputeDelta(baseline, candidate, []string{"regress"})
	assert.Equal(t, 1, delta.Summary.NewlyPassed)
	assert.Equal(t, 1, delta.Summary.NewlyFailed)
	assert.Equal(t, 1, delta.Summary.ScoreUp)
	assert.Equal(t, 1, delta.Summary.ScoreDown)
	kinds := map[string]DeltaKind{}
	critical := false
	for _, item := range delta.Cases {
		kinds[item.EvalCaseID] = item.Kind
		if item.EvalCaseID == "regress" {
			critical = item.Critical
		}
	}
	assert.Equal(t, DeltaNewlyPassed, kinds["fix"])
	assert.Equal(t, DeltaNewlyFailed, kinds["regress"])
	assert.Equal(t, DeltaScoreUp, kinds["soft_up"])
	assert.Equal(t, DeltaScoreDown, kinds["soft_down"])
	assert.True(t, critical)
}

func TestComputeDeltaTreatsDroppedPassingMetricAsNewFailure(t *testing.T) {
	baseline := evalResult("validation", []caseSpec{
		{id: "case", metric: "exact", score: 1, status: status.EvalStatusPassed},
	})
	candidate := evalResult("validation", nil)
	delta := ComputeDelta(baseline, candidate, nil)
	assert.Equal(t, 1, delta.Summary.NewlyFailed)
	assert.Equal(t, statusAbsent, delta.Cases[0].CandidateStatus)
}

func TestComputeDeltaTreatsAddedFailedMetricAsNewFailure(t *testing.T) {
	baseline := evalResult("validation", []caseSpec{
		{id: "case", metric: "exact", score: 1, status: status.EvalStatusPassed},
	})
	candidate := evalResult("validation", []caseSpec{
		{id: "case", metric: "exact", score: 1, status: status.EvalStatusPassed},
		{id: "case", metric: "format", score: 0, status: status.EvalStatusFailed},
	})
	delta := ComputeDelta(baseline, candidate, []string{"case"})
	assert.Equal(t, 1, delta.Summary.NewlyFailed)
	var added CaseDelta
	for _, item := range delta.Cases {
		if item.MetricName == "format" {
			added = item
		}
	}
	assert.Equal(t, statusAbsent, added.BaselineStatus)
	assert.Equal(t, string(status.EvalStatusFailed), added.CandidateStatus)
	assert.Equal(t, DeltaNewlyFailed, added.Kind)
	assert.True(t, added.Critical)
}

func TestComputeDeltaCoversAddedPassedZeroAndDroppedFailedZero(t *testing.T) {
	baseline := evalResult("validation", []caseSpec{
		{id: "dropped_failed_zero", metric: "rubric", score: 0, status: status.EvalStatusFailed},
	})
	candidate := evalResult("validation", []caseSpec{
		{id: "added_passed_zero", metric: "optional", score: 0, status: status.EvalStatusPassed},
	})
	delta := ComputeDelta(baseline, candidate, nil)
	assert.Equal(t, 2, delta.Summary.Unchanged)
	for _, item := range delta.Cases {
		assert.Equal(t, DeltaUnchanged, item.Kind)
	}

	delta = ComputeDelta(
		evalResult("validation", []caseSpec{
			{id: "dropped_failed_positive", metric: "rubric", score: 0.4, status: status.EvalStatusFailed},
		}),
		evalResult("validation", []caseSpec{
			{id: "added_passed_positive", metric: "optional", score: 1, status: status.EvalStatusPassed},
		}),
		nil,
	)
	assert.Equal(t, 1, delta.Summary.ScoreUp)
	assert.Equal(t, 1, delta.Summary.ScoreDown)
}

func TestUnionMetricKeysSortsByEvalSetBeforeCase(t *testing.T) {
	keys := unionMetricKeys(
		map[metricKey]promptiterengine.MetricResult{{evalSetID: "b", evalCaseID: "a", metricName: "m"}: {}},
		map[metricKey]promptiterengine.MetricResult{{evalSetID: "a", evalCaseID: "z", metricName: "m"}: {}},
	)
	require.Len(t, keys, 2)
	assert.Equal(t, "a", keys[0].evalSetID)
	assert.Equal(t, "b", keys[1].evalSetID)
}

func TestAcceptedValidationReturnsLastAcceptedRoundOrBaseline(t *testing.T) {
	baseline := evalResult("validation", []caseSpec{{id: "base", metric: "m", score: 0, status: status.EvalStatusFailed}})
	first := evalResult("validation", []caseSpec{{id: "first", metric: "m", score: 1, status: status.EvalStatusPassed}})
	second := evalResult("validation", []caseSpec{{id: "second", metric: "m", score: 1, status: status.EvalStatusPassed}})
	got, round, ok := AcceptedValidation(&promptiterengine.RunResult{
		BaselineValidation: baseline,
		Rounds: []promptiterengine.RoundResult{
			{Round: 1, Validation: first, Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true}},
			{Round: 2, Validation: second, Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true}},
		},
	})
	assert.True(t, ok)
	assert.Equal(t, 2, round)
	assert.Same(t, second, got)

	got, round, ok = AcceptedValidation(&promptiterengine.RunResult{BaselineValidation: baseline})
	assert.False(t, ok)
	assert.Equal(t, 0, round)
	assert.Same(t, baseline, got)
}

func TestFinalCandidateValidationHandlesNilAndMissingValidation(t *testing.T) {
	got, round, ok := FinalCandidateValidation(nil)
	assert.False(t, ok)
	assert.Equal(t, 0, round)
	assert.Nil(t, got)

	got, round, ok = FinalCandidateValidation(&promptiterengine.RunResult{
		Rounds: []promptiterengine.RoundResult{{Round: 1}},
	})
	assert.False(t, ok)
	assert.Equal(t, 0, round)
	assert.Nil(t, got)
}

type caseSpec struct {
	id     string
	metric string
	score  float64
	status status.EvalStatus
	reason string
}

func evalResult(evalSetID string, specs []caseSpec) *promptiterengine.EvaluationResult {
	cases := make([]promptiterengine.CaseResult, 0, len(specs))
	total := 0.0
	for _, spec := range specs {
		total += spec.score
		cases = append(cases, promptiterengine.CaseResult{
			EvalSetID:  evalSetID,
			EvalCaseID: spec.id,
			Metrics: []promptiterengine.MetricResult{
				{
					MetricName: spec.metric,
					Score:      spec.score,
					Status:     spec.status,
					Reason:     spec.reason,
				},
			},
		})
	}
	score := 0.0
	if len(specs) > 0 {
		score = total / float64(len(specs))
	}
	return &promptiterengine.EvaluationResult{
		OverallScore: score,
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID:    evalSetID,
				OverallScore: score,
				Cases:        cases,
			},
		},
	}
}
