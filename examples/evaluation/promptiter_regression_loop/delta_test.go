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

	"github.com/stretchr/testify/require"
)

func TestComputeDeltas_Outcomes(t *testing.T) {
	baseline := []CaseScore{
		{EvalCaseID: "case_newly_passed", Passed: false, Score: 0.0},
		{EvalCaseID: "case_newly_failed", Passed: true, Score: 1.0},
		{EvalCaseID: "case_improved", Passed: true, Score: 0.5},
		{EvalCaseID: "case_regressed", Passed: true, Score: 1.0},
		{EvalCaseID: "case_unchanged", Passed: true, Score: 1.0},
		{EvalCaseID: "case_missing", Passed: false, Score: 0.0},
	}
	candidate := []CaseScore{
		{EvalCaseID: "case_newly_passed", Passed: true, Score: 1.0},
		{EvalCaseID: "case_newly_failed", Passed: false, Score: 0.0},
		{EvalCaseID: "case_improved", Passed: true, Score: 0.9},
		{EvalCaseID: "case_regressed", Passed: true, Score: 0.4},
		{EvalCaseID: "case_unchanged", Passed: true, Score: 1.0},
		{EvalCaseID: "case_brand_new", Passed: true, Score: 1.0},
	}

	deltas := ComputeDeltas(baseline, candidate)
	require.Len(t, deltas, len(candidate))

	byID := make(map[string]CaseDelta, len(deltas))
	for _, delta := range deltas {
		byID[delta.EvalCaseID] = delta
	}

	require.Equal(t, DeltaNewlyPassed, byID["case_newly_passed"].Outcome)
	require.Equal(t, DeltaNewlyFailed, byID["case_newly_failed"].Outcome)
	require.Equal(t, DeltaScoreImproved, byID["case_improved"].Outcome)
	require.Equal(t, DeltaScoreRegressed, byID["case_regressed"].Outcome)
	require.Equal(t, DeltaUnchanged, byID["case_unchanged"].Outcome)

	// A case absent from baseline is treated as newly passed when the candidate passes.
	require.Equal(t, DeltaNewlyPassed, byID["case_brand_new"].Outcome)
}

func TestComputeDeltas_Scores(t *testing.T) {
	baseline := []CaseScore{{EvalCaseID: "c1", Passed: false, Score: 0.25}}
	candidate := []CaseScore{{EvalCaseID: "c1", Passed: true, Score: 0.75}}
	deltas := ComputeDeltas(baseline, candidate)
	require.Len(t, deltas, 1)
	require.InDelta(t, 0.5, deltas[0].Delta, 1e-9)
	require.Equal(t, 0.25, deltas[0].BaselineScore)
	require.Equal(t, 0.75, deltas[0].CandidateScore)
}

func TestSummarizeDeltas(t *testing.T) {
	deltas := []CaseDelta{
		{Outcome: DeltaNewlyPassed},
		{Outcome: DeltaNewlyPassed, CandidatePassed: true},
		{Outcome: DeltaNewlyFailed},
		{Outcome: DeltaScoreImproved, CandidatePassed: true},
		{Outcome: DeltaScoreRegressed, CandidatePassed: true},
		{Outcome: DeltaUnchanged, CandidatePassed: true},
	}
	summary := SummarizeDeltas(deltas)
	require.Equal(t, 6, summary.Total)
	require.Equal(t, 2, summary.NewlyPassed)
	require.Equal(t, 1, summary.NewlyFailed)
	require.Equal(t, 1, summary.ScoreImproved)
	require.Equal(t, 1, summary.ScoreRegressed)
	require.Equal(t, 1, summary.Unchanged)
	require.Equal(t, 4, summary.PassedAtCandidate)
}
