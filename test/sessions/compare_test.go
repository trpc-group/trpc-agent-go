//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareSnapshotsDistinguishesMissingStateKeyFromNull(t *testing.T) {
	nullState := map[string]json.RawMessage{
		"status": json.RawMessage(`null`),
	}
	emptyState := map[string]json.RawMessage{}
	missing := map[string]bool{"$missing": true}

	tests := []struct {
		name            string
		referenceState  map[string]json.RawMessage
		actualState     map[string]json.RawMessage
		wantReference   any
		wantActual      any
		wantExplanation string
	}{
		{
			name:            "actual key is missing",
			referenceState:  nullState,
			actualState:     emptyState,
			wantReference:   nil,
			wantActual:      missing,
			wantExplanation: "map key is missing from actual snapshot",
		},
		{
			name:            "reference key is missing",
			referenceState:  emptyState,
			actualState:     nullState,
			wantReference:   missing,
			wantActual:      nil,
			wantExplanation: "map key is missing from reference snapshot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CompareSnapshots(
				stateComparisonSnapshot(tt.referenceState),
				stateComparisonSnapshot(tt.actualState),
				"inmemory",
				"sqlite",
				nil,
			)

			require.False(t, result.Equal)
			require.Len(t, result.Differences, 1)
			diff := result.Differences[0]
			require.Equal(t, "$.sessions[0].state.status", diff.Path)
			require.Equal(t, "state", diff.Category)
			require.Equal(t, "session-presence", diff.SessionID)
			require.Equal(t, tt.wantReference, diff.Reference)
			require.Equal(t, tt.wantActual, diff.Actual)
			require.Equal(t, tt.wantExplanation, diff.Explanation)
			require.False(t, diff.Allowed)
		})
	}
}

func TestCompareSnapshotsTreatsPresentNullStateKeysAsEqual(t *testing.T) {
	state := map[string]json.RawMessage{
		"status": json.RawMessage(`null`),
	}

	result := CompareSnapshots(
		stateComparisonSnapshot(state),
		stateComparisonSnapshot(state),
		"inmemory",
		"sqlite",
		nil,
	)

	require.True(t, result.Equal)
	require.Empty(t, result.Differences)
}

func TestCompareSnapshotsAllowsMissingStateKeyDifference(t *testing.T) {
	path := "$.sessions[0].state.status"
	reference := stateComparisonSnapshot(map[string]json.RawMessage{
		"status": json.RawMessage(`null`),
	})
	actual := stateComparisonSnapshot(map[string]json.RawMessage{})

	result := CompareSnapshots(
		reference,
		actual,
		"inmemory",
		"sqlite",
		[]AllowedDiffRule{{
			Path: path, Backend: "sqlite",
			Reason: "sqlite intentionally omits null state keys",
		}},
	)

	require.True(t, result.Equal)
	require.Len(t, result.Differences, 1)
	require.True(t, result.Differences[0].Allowed)
	require.Equal(t, path, result.Differences[0].Path)
	require.Equal(t,
		"sqlite intentionally omits null state keys",
		result.Differences[0].Explanation,
	)
}

func TestCompareValueDistinguishesMissingSliceElementFromNull(t *testing.T) {
	missing := map[string]bool{"$missing": true}
	tests := []struct {
		name            string
		left            []any
		right           []any
		wantReference   any
		wantActual      any
		wantExplanation string
	}{
		{
			name:            "actual element is missing",
			left:            []any{nil},
			right:           []any{},
			wantReference:   nil,
			wantActual:      missing,
			wantExplanation: "array element is missing from actual snapshot",
		},
		{
			name:            "reference element is missing",
			left:            []any{},
			right:           []any{nil},
			wantReference:   missing,
			wantActual:      nil,
			wantExplanation: "array element is missing from reference snapshot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var differences []Difference
			compareValue("$.items", tt.left, tt.right, &differences)

			require.Len(t, differences, 1)
			diff := differences[0]
			require.Equal(t, "$.items[0]", diff.Path)
			require.Equal(t, tt.wantReference, diff.Reference)
			require.Equal(t, tt.wantActual, diff.Actual)
			require.Equal(t, tt.wantExplanation, diff.Explanation)
		})
	}
}

func TestCompareValueTreatsPresentNullSliceElementsAsEqual(t *testing.T) {
	var differences []Difference
	compareValue("$.items", []any{nil}, []any{nil}, &differences)

	require.Empty(t, differences)
}

func stateComparisonSnapshot(
	state map[string]json.RawMessage,
) CanonicalSnapshot {
	return CanonicalSnapshot{Snapshot: Snapshot{
		CaseID: "presence-comparison",
		Sessions: []SessionSnapshot{{
			ID:    "session-presence",
			State: state,
		}},
	}}
}
