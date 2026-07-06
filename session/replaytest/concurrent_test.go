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
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestConcurrentCaseE2E(t *testing.T) {
	report := runReplayCaseReport(t, CaseConcurrentInterleaved)
	require.Equal(t, 1, report.PassedCases)

	snapshot := runReplayCaseSnapshot(t, CaseConcurrentInterleaved)
	norm, err := NewNormalizer().Normalize(snapshot)
	require.NoError(t, err)
	require.NotNil(t, norm.Session)
	require.Len(t, norm.Session.Events, 3)

	branches := map[string]string{}
	for _, evt := range norm.Session.Events {
		branches[evt.ID] = evt.Branch
	}
	require.Equal(t, "agent_x", branches["c10.agent_x.step_1"])
	require.Equal(t, "agent_y", branches["c10.agent_y.step_1"])
	require.Equal(t, "agent_x", branches["c10.agent_x.step_2"])
}

func TestConcurrentPartialOrderFaults(t *testing.T) {
	base := testSnapshotWithEvents("a", []event.Event{
		*testEvent("c10.agent_x.step_1", "agent_x", "x1"),
		*testEvent("c10.agent_y.step_1", "agent_y", "y1"),
		*testEvent("c10.agent_x.step_2", "agent_x", "x2"),
	})

	tests := []struct {
		name   string
		events []event.Event
	}{
		{
			name: "branch_internal_reorder",
			events: []event.Event{
				*testEvent("c10.agent_x.step_2", "agent_x", "x2"),
				*testEvent("c10.agent_y.step_1", "agent_y", "y1"),
				*testEvent("c10.agent_x.step_1", "agent_x", "x1"),
			},
		},
		{
			name: "duplicate_event",
			events: []event.Event{
				*testEvent("c10.agent_x.step_1", "agent_x", "x1"),
				*testEvent("c10.agent_x.step_1", "agent_x", "x1"),
				*testEvent("c10.agent_y.step_1", "agent_y", "y1"),
				*testEvent("c10.agent_x.step_2", "agent_x", "x2"),
			},
		},
		{
			name: "missing_event",
			events: []event.Event{
				*testEvent("c10.agent_x.step_1", "agent_x", "x1"),
				*testEvent("c10.agent_y.step_1", "agent_y", "y1"),
			},
		},
		{
			name: "branch_wrong",
			events: []event.Event{
				*testEvent("c10.agent_x.step_1", "agent_z", "x1"),
				*testEvent("c10.agent_y.step_1", "agent_y", "y1"),
				*testEvent("c10.agent_x.step_2", "agent_x", "x2"),
			},
		},
		{
			name: "extra_branch",
			events: []event.Event{
				*testEvent("c10.agent_x.step_1", "agent_x", "x1"),
				*testEvent("c10.agent_y.step_1", "agent_y", "y1"),
				*testEvent("c10.agent_x.step_2", "agent_x", "x2"),
				*testEvent("c10.agent_z.step_1", "agent_z", "z1"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changed := testSnapshotWithEvents("b", tc.events)
			result := NewComparator().Compare(base, changed, nil, InMemoryProfile(), InMemoryProfile())
			require.Equal(t, StatusFailed, result.Status)
			require.NotEmpty(t, result.Diffs)
		})
	}
}

func TestRecoveryDuplicateEventCaseE2E(t *testing.T) {
	report := runReplayCaseReport(t, CaseRecoveryDuplicateEvent)
	require.Equal(t, 1, report.PassedCases)

	snapshot := runReplayCaseSnapshot(t, CaseRecoveryDuplicateEvent)
	norm, err := NewNormalizer().Normalize(snapshot)
	require.NoError(t, err)
	require.NotNil(t, norm.Session)
	require.Len(t, norm.Session.Events, 3)
	require.Equal(t, 2, countEventsByID(norm, "c13.user.1"))
	require.Equal(t, 1, countEventsByID(norm, "c13.assistant.1"))
}

func countEventsByID(snapshot *SessionSnapshot, id string) int {
	count := 0
	for _, evt := range snapshot.Session.Events {
		if evt.ID == id {
			count++
		}
	}
	return count
}
