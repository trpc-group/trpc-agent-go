//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/harness"
)

func TestRunSingleTurn(t *testing.T) {
	bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
	require.NoError(t, err)
	defer func() {
		for _, b := range bs {
			_ = b.Close()
		}
	}()

	c := &harness.ReplayCase{
		Name: "t",
		Key:  harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		Operations: []harness.Operation{
			{Type: "append_event", Event: &harness.EventSpec{Author: "user", Role: "user", Content: "hi"}},
			{Type: "append_event", Event: &harness.EventSpec{Author: "assistant", Role: "assistant", Content: "hello"}},
		},
	}
	for _, b := range bs {
		snap, err := harness.Run(context.Background(), b, c)
		require.NoError(t, err, b.Name)
		require.Len(t, snap.Events, 2, b.Name)
		require.Equal(t, "hi", snap.Events[0].Content, b.Name)
		require.Equal(t, "hello", snap.Events[1].Content, b.Name)
	}
}

func TestRunFaultyDuplicateEventSurfacesExtraRow(t *testing.T) {
	clean := backendsForTest(t)
	faultySet := backendsForTest(t)

	c := &harness.ReplayCase{
		Name: "t", Key: harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		FaultInjection: "duplicate_event",
		Operations: []harness.Operation{
			{Type: "append_event", Event: &harness.EventSpec{Author: "user", Role: "user", Content: "hi"}},
		},
	}
	cleanSnap, err := harness.Run(context.Background(), clean[1], c)
	require.NoError(t, err)
	bad, err := harness.RunFaulty(context.Background(), faultySet[1], c)
	require.NoError(t, err)
	require.Greater(t, len(bad.Events), len(cleanSnap.Events), "duplicate_event must add a persisted row")
}

func backendsForTest(t *testing.T) []*backends.Backend {
	t.Helper()
	bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, b := range bs {
			_ = b.Close()
		}
	})
	return bs
}

func TestPartialEventIsDroppedOnEveryBackend(t *testing.T) {
	bs := backendsForTest(t)
	c := &harness.ReplayCase{
		Name: "t", Key: harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		Operations: []harness.Operation{
			{Type: "append_event", Event: &harness.EventSpec{Author: "user", Role: "user", Content: "ask"}},
			{Type: "append_event", Event: &harness.EventSpec{Author: "assistant", Role: "assistant", Content: "partial", Partial: true}},
			{Type: "append_event", Event: &harness.EventSpec{Author: "assistant", Role: "assistant", Content: "final"}},
		},
	}
	for _, b := range bs {
		snap, err := harness.Run(context.Background(), b, c)
		require.NoError(t, err, b.Name)
		require.Len(t, snap.Events, 2, b.Name)
		require.Equal(t, "final", snap.Events[len(snap.Events)-1].Content, b.Name)
	}
}

func TestReadBackReturnsMoreThan100Memories(t *testing.T) {
	bs := backendsForTest(t)
	ops := make([]harness.Operation, 0, 105)
	for i := 0; i < 105; i++ {
		ops = append(ops, harness.Operation{Type: "add_memory", Value: fmt.Sprintf("m%03d", i), Topics: []string{"t"}})
	}
	c := &harness.ReplayCase{Name: "t", Key: harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"}, Operations: ops}
	snap, err := harness.Run(context.Background(), bs[0], c)
	require.NoError(t, err)
	require.Len(t, snap.Memories, 105)
}

func TestProjectMemoriesIncludesEpisodicMetadata(t *testing.T) {
	bs := backendsForTest(t)
	c := &harness.ReplayCase{
		Name: "t", Key: harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		Operations: []harness.Operation{
			{Type: "add_memory", Value: "met Bob in Paris", Topics: []string{"trip"}, Kind: "episode"},
		},
	}
	snap, err := harness.Run(context.Background(), bs[0], c)
	require.NoError(t, err)
	require.Len(t, snap.Memories, 1)
	require.Equal(t, "episode", snap.Memories[0].Metadata["kind"])
}

func TestClearStateRemovesSessionKeys(t *testing.T) {
	bs := backendsForTest(t)
	c := &harness.ReplayCase{
		Name: "t", Key: harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		Operations: []harness.Operation{
			{Type: "set_state", Key: "lang", Value: "en"},
			{Type: "clear_state", Key: "lang"},
		},
	}
	for _, b := range bs {
		snap, err := harness.Run(context.Background(), b, c)
		require.NoError(t, err, b.Name)
		_, present := snap.State["lang"]
		require.False(t, present, b.Name)
	}
}

func TestConcurrentEventsAllPersist(t *testing.T) {
	bs := backendsForTest(t)
	c := &harness.ReplayCase{
		Name: "t", Key: harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		Operations: []harness.Operation{
			{Type: "append_event", Event: &harness.EventSpec{Author: "user", Role: "user", Content: "ask"}},
			{Type: "append_event", Concurrent: true, Event: &harness.EventSpec{Author: "a", Role: "assistant", Content: "x"}},
			{Type: "append_event", Concurrent: true, Event: &harness.EventSpec{Author: "b", Role: "assistant", Content: "y"}},
		},
	}
	snap, err := harness.Run(context.Background(), bs[0], c)
	require.NoError(t, err)
	require.Len(t, snap.Events, 3)
}

func TestUpdateAndDeleteMemoryByContentSelector(t *testing.T) {
	bs := backendsForTest(t)
	c := &harness.ReplayCase{
		Name: "t", Key: harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		Operations: []harness.Operation{
			{Type: "add_memory", Value: "old fact", Topics: []string{"t"}, Kind: "fact"},
			{Type: "update_memory", MemoryID: "old fact", Value: "new fact", Topics: []string{"t"}},
		},
	}
	snap, err := harness.Run(context.Background(), bs[0], c)
	require.NoError(t, err)
	require.Len(t, snap.Memories, 1)
	require.Equal(t, "new fact", snap.Memories[0].Content)
}
