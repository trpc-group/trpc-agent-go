//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replayconsistency

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	stateSemanticsSessionID = "state-semantics-session"
	explicitNullStateKey    = "explicit-null"
	deletedStateKey         = "deleted"
	expectedStateSessions   = 1
	firstStateSessionIndex  = 0
)

func TestReplayFixtureDistinguishesDeleteFromExplicitNull(t *testing.T) {
	fixture, err := newInMemoryBackend().New(context.Background(), "state-semantics")
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	defer func() {
		if err := fixture.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	}()
	operations := []replaytest.Operation{
		{Kind: replaytest.OperationCreateSession, SessionID: stateSemanticsSessionID},
		{
			Kind: replaytest.OperationUpdateState, SessionID: stateSemanticsSessionID,
			StateUpdates: map[string]any{explicitNullStateKey: nil, deletedStateKey: "value"},
		},
		{
			Kind: replaytest.OperationUpdateState, SessionID: stateSemanticsSessionID,
			StateDeletes: []string{deletedStateKey},
		},
	}
	for _, operation := range operations {
		if err := fixture.Apply(context.Background(), operation); err != nil {
			t.Fatalf("apply %q: %v", operation.Kind, err)
		}
	}
	snapshot, err := fixture.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Sessions) != expectedStateSessions {
		t.Fatalf("sessions = %d, want %d", len(snapshot.Sessions), expectedStateSessions)
	}
	state := snapshot.Sessions[firstStateSessionIndex].State
	if state[explicitNullStateKey].Kind != replaytest.StateValueNull {
		t.Fatalf("explicit null state = %#v", state[explicitNullStateKey])
	}
	if _, exists := state[deletedStateKey]; exists {
		t.Fatalf("deleted state remains: %#v", state[deletedStateKey])
	}
}

func TestNormalizeDeletedStatePreservesStaleValue(t *testing.T) {
	state := map[string]replaytest.StateValueSnapshot{
		deletedStateKey:      replaytest.JSONStateValue("stale"),
		explicitNullStateKey: replaytest.NullStateValue(),
		"tombstone":          replaytest.NullStateValue(),
	}
	deleted := map[string]struct{}{
		deletedStateKey: {}, explicitNullStateKey: {}, "tombstone": {},
	}
	normalizeDeletedState(state, deleted, map[string]struct{}{"tombstone": {}})
	if got := state[deletedStateKey]; got != replaytest.JSONStateValue("stale") {
		t.Fatalf("stale deleted value was hidden: %#v", got)
	}
	if got := state[explicitNullStateKey]; got.Kind != replaytest.StateValueNull {
		t.Fatalf("explicit null was changed: %#v", got)
	}
	if _, exists := state["tombstone"]; exists {
		t.Fatalf("raw tombstone remains: %#v", state["tombstone"])
	}
}
