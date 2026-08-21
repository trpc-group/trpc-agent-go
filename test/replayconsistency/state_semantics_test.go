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
	"sync"
	"testing"
	"time"

	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
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

func TestSessionSnapshotPreservesUserSummaryStateKeys(t *testing.T) {
	sess := session.NewSession(replayAppName, replayUserID, stateSemanticsSessionID)
	rawState := session.StateMap{
		"summary:profile": []byte(`{"name":"ada"}`),
		session.SummaryLastIncludedTimestampStateKey: []byte(`"hidden-ts"`),
		session.SummaryLastIncludedEventIDStateKey:   []byte(`"hidden-id"`),
		"tracks": []byte(`["tools"]`),
	}
	snapshot, err := toSessionSnapshot(sess, rawState, "", false)
	if err != nil {
		t.Fatalf("session snapshot: %v", err)
	}
	got, exists := snapshot.State["summary:profile"]
	if !exists {
		t.Fatalf("summary:profile state was filtered: %#v", snapshot.State)
	}
	if got.Kind != replaytest.StateValueJSON {
		t.Fatalf("summary:profile kind = %q, want %q", got.Kind, replaytest.StateValueJSON)
	}
	profile, ok := got.Value.(map[string]any)
	if !ok || profile["name"] != "ada" {
		t.Fatalf("summary:profile value = %#v", got.Value)
	}
	for _, key := range []string{
		session.SummaryLastIncludedTimestampStateKey,
		session.SummaryLastIncludedEventIDStateKey,
		"tracks",
	} {
		if _, exists := snapshot.State[key]; exists {
			t.Fatalf("internal state key %q remains: %#v", key, snapshot.State[key])
		}
	}
}

func TestReplayFixtureOrdersStateDeleteBookkeepingWithWrites(t *testing.T) {
	sessionService := &delayedStateSessionService{
		SessionService:  sessioninmemory.NewSessionService(),
		firstCommitted:  make(chan struct{}),
		releaseFirst:    make(chan struct{}),
		secondCommitted: make(chan struct{}),
	}
	fixture := newReplayFixture(replayFixtureConfig{
		name:           "state-order",
		sessionService: sessionService,
		memoryService:  memoryinmemory.NewMemoryService(),
		summarizer:     &replaySummarizer{},
	})
	defer func() {
		sessionService.release()
		if err := fixture.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	}()
	if err := fixture.Apply(context.Background(), replaytest.Operation{
		Kind: replaytest.OperationCreateSession, SessionID: stateSemanticsSessionID,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- fixture.Apply(context.Background(), replaytest.Operation{
			Kind: replaytest.OperationUpdateState, SessionID: stateSemanticsSessionID,
			StateUpdates: map[string]any{deletedStateKey: "value"},
		})
	}()
	select {
	case <-sessionService.firstCommitted:
	case <-time.After(time.Second):
		t.Fatal("first state update did not commit")
	}
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- fixture.Apply(context.Background(), replaytest.Operation{
			Kind: replaytest.OperationUpdateState, SessionID: stateSemanticsSessionID,
			StateDeletes: []string{deletedStateKey},
		})
	}()
	select {
	case <-sessionService.secondCommitted:
		t.Fatal("delete committed before earlier write completed bookkeeping")
	case <-time.After(25 * time.Millisecond):
	}
	sessionService.release()
	if err := <-firstDone; err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second update: %v", err)
	}
	snapshot, err := fixture.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	state := snapshot.Sessions[firstStateSessionIndex].State
	if _, exists := state[deletedStateKey]; exists {
		t.Fatalf("deleted state remains: %#v", state[deletedStateKey])
	}
}

type delayedStateSessionService struct {
	*sessioninmemory.SessionService
	firstCommitted  chan struct{}
	releaseFirst    chan struct{}
	secondCommitted chan struct{}
	firstOnce       sync.Once
	secondOnce      sync.Once
	releaseOnce     sync.Once
}

func (service *delayedStateSessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	value, hasTarget := state[deletedStateKey]
	if hasTarget && value != nil {
		err := service.SessionService.UpdateSessionState(ctx, key, state)
		service.firstOnce.Do(func() { close(service.firstCommitted) })
		select {
		case <-service.releaseFirst:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if hasTarget && value == nil {
		select {
		case <-service.firstCommitted:
		case <-ctx.Done():
			return ctx.Err()
		}
		err := service.SessionService.UpdateSessionState(ctx, key, state)
		service.secondOnce.Do(func() { close(service.secondCommitted) })
		return err
	}
	return service.SessionService.UpdateSessionState(ctx, key, state)
}

func (service *delayedStateSessionService) release() {
	service.releaseOnce.Do(func() { close(service.releaseFirst) })
}
