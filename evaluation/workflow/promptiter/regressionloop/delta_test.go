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
)

func TestComputeDeltasClassifiesTransitions(t *testing.T) {
	baseline := evalSummary(0.5,
		caseResult("new-pass", 0.2, false),
		caseResult("new-fail", 0.9, true),
		caseResult("improved", 0.4, false),
		caseResult("regressed", 0.8, true),
		caseResult("unchanged", 0.7, true),
	)
	candidate := evalSummary(0.6,
		caseResult("new-pass", 0.9, true),
		caseResult("new-fail", 0, false),
		caseResult("improved", 0.6, false),
		caseResult("regressed", 0.6, true),
		caseResult("unchanged", 0.7, true),
	)
	deltas, summary := ComputeDeltas(baseline, candidate, []string{"new-fail"})

	byID := map[string]CaseDelta{}
	for _, delta := range deltas {
		byID[delta.EvalID] = delta
	}
	assert.Equal(t, TransitionNewlyPassed, byID["new-pass"].Transition)
	assert.Equal(t, TransitionNewlyFailed, byID["new-fail"].Transition)
	assert.Equal(t, TransitionImproved, byID["improved"].Transition)
	assert.Equal(t, TransitionRegressed, byID["regressed"].Transition)
	assert.Equal(t, TransitionUnchanged, byID["unchanged"].Transition)
	assert.True(t, byID["new-fail"].NewHardFail)
	assert.True(t, byID["new-fail"].CriticalRegression)
	assert.Equal(t, 1, summary.NewlyPassed)
	assert.Equal(t, 1, summary.NewlyFailed)
	assert.Equal(t, 1, summary.Improved)
	assert.Equal(t, 1, summary.Regressed)
	assert.Equal(t, 1, summary.Unchanged)
	assert.Equal(t, 1, summary.NewHardFails)
	assert.Equal(t, 1, summary.CriticalRegressions)
}

func TestComputeDeltasMarksCasesMissingFromEitherSide(t *testing.T) {
	baseline := evalSummary(0.5,
		caseResult("shared", 0.5, true),
		caseResult("baseline-only", 0.4, false),
	)
	candidate := evalSummary(0.6,
		caseResult("shared", 0.6, true),
		caseResult("candidate-only", 0.7, true),
	)

	deltas, _ := ComputeDeltas(baseline, candidate, nil)
	byID := map[string]CaseDelta{}
	for _, delta := range deltas {
		byID[delta.EvalID] = delta
	}

	assert.Equal(t, TransitionMissing, byID["baseline-only"].Transition)
	assert.Equal(t, TransitionMissing, byID["candidate-only"].Transition)
	assert.Equal(t, TransitionImproved, byID["shared"].Transition)
}
