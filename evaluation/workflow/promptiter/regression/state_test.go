//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyStateTransitionAllDecisionCombinations(t *testing.T) {
	tests := []struct {
		name           string
		search         DecisionStatus
		release        DecisionStatus
		wantSearch     string
		wantReleased   string
		searchUpdated  bool
		releaseUpdated bool
	}{
		{
			name: "search accepted release accepted", search: DecisionAccepted, release: DecisionAccepted,
			wantSearch: "candidate", wantReleased: "candidate", searchUpdated: true, releaseUpdated: true,
		},
		{
			name: "search accepted release rejected", search: DecisionAccepted, release: DecisionRejected,
			wantSearch: "candidate", wantReleased: "released", searchUpdated: true,
		},
		{
			name: "search rejected release accepted", search: DecisionRejected, release: DecisionAccepted,
			wantSearch: "search", wantReleased: "candidate", releaseUpdated: true,
		},
		{
			name: "search rejected release rejected", search: DecisionRejected, release: DecisionRejected,
			wantSearch: "search", wantReleased: "released",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := testProfileState()
			candidate := ProfileRecord{Role: ProfileCandidate, Hash: "candidate"}
			candidateTrain := testSnapshot("candidate", []string{"train"}, []string{"quality"})
			candidateValidation := testSnapshot("candidate", []string{"validation"}, []string{"quality"})
			transition, err := ApplyStateTransition(
				&state,
				candidate,
				candidateTrain,
				candidateValidation,
				Decision{Status: test.search},
				Decision{Status: test.release},
			)
			require.NoError(t, err)
			require.Equal(t, test.wantSearch, state.Search.Hash)
			require.Equal(t, test.wantReleased, state.Released.Hash)
			require.Equal(t, test.searchUpdated, transition.SearchUpdated)
			require.Equal(t, test.releaseUpdated, transition.ReleaseUpdated)
			if test.searchUpdated {
				require.Same(t, candidateTrain, state.SearchTrain)
				require.Same(t, candidateValidation, state.SearchValidation)
			}
			if test.releaseUpdated {
				require.Same(t, candidateValidation, state.ReleasedValidation)
			}
		})
	}
}

func TestApplyStateTransitionNotEvaluableUpdatesNothing(t *testing.T) {
	tests := []struct {
		name    string
		search  DecisionStatus
		release DecisionStatus
	}{
		{name: "search not evaluable", search: DecisionNotEvaluable, release: DecisionAccepted},
		{name: "release not evaluable", search: DecisionAccepted, release: DecisionNotEvaluable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := testProfileState()
			beforeSearchTrain := state.SearchTrain
			beforeSearchValidation := state.SearchValidation
			beforeReleasedValidation := state.ReleasedValidation
			transition, err := ApplyStateTransition(
				&state,
				ProfileRecord{Role: ProfileCandidate, Hash: "candidate"},
				testSnapshot("candidate", []string{"train"}, []string{"quality"}),
				testSnapshot("candidate", []string{"validation"}, []string{"quality"}),
				Decision{Status: test.search},
				Decision{Status: test.release},
			)
			require.NoError(t, err)
			require.Equal(t, "search", state.Search.Hash)
			require.Equal(t, "released", state.Released.Hash)
			require.False(t, transition.SearchUpdated)
			require.False(t, transition.ReleaseUpdated)
			require.Same(t, beforeSearchTrain, state.SearchTrain)
			require.Same(t, beforeSearchValidation, state.SearchValidation)
			require.Same(t, beforeReleasedValidation, state.ReleasedValidation)
		})
	}
}

func testProfileState() ProfileState {
	initialValidation := testSnapshot("initial", []string{"validation"}, []string{"quality"})
	searchTrain := testSnapshot("search", []string{"train"}, []string{"quality"})
	searchValidation := testSnapshot("search", []string{"validation"}, []string{"quality"})
	releasedValidation := testSnapshot("released", []string{"validation"}, []string{"quality"})
	return ProfileState{
		Initial:            ProfileRecord{Role: ProfileInitial, Hash: "initial"},
		Search:             ProfileRecord{Role: ProfileSearch, Hash: "search"},
		Released:           ProfileRecord{Role: ProfileReleased, Hash: "released"},
		InitialValidation:  initialValidation,
		SearchTrain:        searchTrain,
		SearchValidation:   searchValidation,
		ReleasedValidation: releasedValidation,
	}
}
