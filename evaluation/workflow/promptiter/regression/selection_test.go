//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectedProfileReturnsOnlyRegressionAuthorizedProfile(t *testing.T) {
	profile := testProfile("target", "selected")
	result := &RunResult{
		Decision: DecisionAccepted, SelectedCandidateID: "candidate",
		Candidates: []CandidateResult{{Candidate: Candidate{ID: "candidate", Profile: profile}}},
	}
	selected, err := result.SelectedProfile()
	require.NoError(t, err)
	require.NotSame(t, profile, selected)
	assert.Equal(t, "selected", *selected.Overrides[0].Value.Text)
	*selected.Overrides[0].Value.Text = "mutated"
	assert.Equal(t, "selected", *profile.Overrides[0].Value.Text)
}

func TestSelectedProfileRejectsMissingOrAmbiguousSelection(t *testing.T) {
	profile := testProfile("target", "selected")
	tests := []struct {
		name   string
		result *RunResult
		isNone bool
	}{
		{name: "nil result", isNone: true},
		{name: "release rejected", result: &RunResult{Decision: DecisionRejected}, isNone: true},
		{name: "selected id absent", result: &RunResult{Decision: DecisionAccepted, SelectedCandidateID: "missing"}},
		{name: "selected profile absent", result: &RunResult{
			Decision: DecisionAccepted, SelectedCandidateID: "candidate",
			Candidates: []CandidateResult{{Candidate: Candidate{ID: "candidate"}}},
		}},
		{name: "duplicate selected id", result: &RunResult{
			Decision: DecisionAccepted, SelectedCandidateID: "candidate",
			Candidates: []CandidateResult{
				{Candidate: Candidate{ID: "candidate", Profile: profile}},
				{Candidate: Candidate{ID: "candidate", Profile: profile}},
			},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile, err := test.result.SelectedProfile()
			assert.Nil(t, profile)
			require.Error(t, err)
			if test.isNone {
				assert.ErrorIs(t, err, ErrNoSelectedCandidate)
			}
		})
	}
}

func TestSelectedProfileDoesNotTreatRejectedCandidateAsPublishable(t *testing.T) {
	_, err := (&RunResult{Decision: DecisionInconclusive}).SelectedProfile()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoSelectedCandidate))
}

func TestSelectedProfileAllowsRepeatedProfileWithUniqueCandidateIDs(t *testing.T) {
	repeated := testProfile("target", "same-output")
	result := &RunResult{
		Decision: DecisionAccepted, SelectedCandidateID: "round-2",
		Candidates: []CandidateResult{
			{Candidate: Candidate{ID: "round-1", Profile: repeated}},
			{Candidate: Candidate{ID: "round-2", Profile: repeated}},
		},
	}
	selected, err := result.SelectedProfile()
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "same-output", *selected.Overrides[0].Value.Text)
}
