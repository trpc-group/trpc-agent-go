//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllCases(t *testing.T) {
	cases := AllCases()
	require.Len(t, cases, 13)
	seen := map[string]bool{}
	for _, tc := range cases {
		require.NotEmpty(t, tc.Name)
		require.NotEmpty(t, tc.Steps)
		require.False(t, seen[tc.Name])
		seen[tc.Name] = true
	}
}

func TestCasesInjectFault(t *testing.T) {
	for _, tc := range AllCases() {
		a := testSnapshot("a", "hello", "answer")
		b := testSnapshot("b", "hello", "fault-"+tc.Name)
		result := NewComparator().Compare(a, b, tc.AllowedDiffs, InMemoryProfile(), InMemoryProfile())
		require.Equal(t, StatusFailed, result.Status, tc.Name)
	}
}

func TestStepTypeMethods(t *testing.T) {
	tests := []struct {
		step   ReplayStep
		want   string
		logKey string
	}{
		{AppendEventStep{Key: "k1"}, "append_event", "k1"},
		{UpdateStateStep{Key: "k2"}, "update_state", "k2"},
		{AddMemoryStep{Key: "k3"}, "add_memory", "k3"},
		{SearchMemoryStep{Key: "k4"}, "search_memory", "k4"},
		{CreateSummaryStep{Key: "k5"}, "create_summary", "k5"},
		{WaitSummaryStep{Key: "k6"}, "wait_summary", "k6"},
		{AppendTrackStep{Key: "k7"}, "append_track", "k7"},
		{GetSessionStep{Key: "k8"}, "get_session", "k8"},
		{ListAppStatesStep{Key: "k9"}, "list_app_states", "k9"},
		{ListUserStatesStep{Key: "k10"}, "list_user_states", "k10"},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, tc.step.Type())
		require.Equal(t, tc.logKey, tc.step.LogicalKey())
	}
}
