//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backends

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestWrapFaultyDuplicateEventPersistsTwice(t *testing.T) {
	base, err := newInMemoryBackend(testSummarizer{})
	require.NoError(t, err)
	defer base.Close()
	faulty, err := WrapFaulty(base, FaultDuplicateEvent)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "a", UserID: "u", SessionID: "s"}
	_, err = faulty.Session.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)
	sess, err := faulty.Session.GetSession(ctx, key)
	require.NoError(t, err)
	ev := event.New("", "user")
	ev.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: "user", Content: "hi"}}}}
	require.NoError(t, faulty.Session.AppendEvent(ctx, sess, ev))
	got, err := faulty.Session.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, got.GetEvents(), 2, "duplicate_event must persist the event twice")
}

func TestWrapFaultyDropMemoryNoOps(t *testing.T) {
	base, err := newInMemoryBackend(testSummarizer{})
	require.NoError(t, err)
	defer base.Close()
	faulty, err := WrapFaulty(base, FaultDropMemory)
	require.NoError(t, err)

	ctx := context.Background()
	uk := memory.UserKey{AppName: "a", UserID: "u"}
	require.NoError(t, faulty.Memory.AddMemory(ctx, uk, "remember me", []string{"t"}))
	mems, err := faulty.Memory.ReadMemories(ctx, uk, 0)
	require.NoError(t, err)
	require.Empty(t, mems, "drop_memory must swallow the AddMemory")
}

func TestWrapFaultyUnknownFaultErrors(t *testing.T) {
	base, err := newInMemoryBackend(testSummarizer{})
	require.NoError(t, err)
	defer base.Close()
	_, err = WrapFaulty(base, "nonexistent")
	require.Error(t, err)
}
