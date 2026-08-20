//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package finalevent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestCallbacksAreOneShotAndSharedWithClones(t *testing.T) {
	root := agent.NewInvocation()
	Attach(root)
	child := root.Clone()

	var observed []*event.Event
	require.True(t, Register(child, "event-1", func(_ context.Context, evt *event.Event) {
		observed = append(observed, evt)
	}))
	finalized := &event.Event{ID: "replacement"}
	Run(context.Background(), root, "event-1", finalized)
	Run(context.Background(), root, "event-1", &event.Event{})
	require.Equal(t, []*event.Event{finalized}, observed)
}

func TestDiscardAndClearReleaseCallbacks(t *testing.T) {
	inv := agent.NewInvocation()
	require.False(t, Register(inv, "event-1", func(context.Context, *event.Event) {}))

	Attach(inv)
	called := false
	require.True(t, Register(inv, "event-1", func(context.Context, *event.Event) {
		called = true
	}))
	Discard(inv, "event-1")
	Run(context.Background(), inv, "event-1", &event.Event{})
	require.False(t, called)

	require.True(t, Register(inv, "event-2", func(context.Context, *event.Event) {
		called = true
	}))
	Clear(inv)
	Run(context.Background(), inv, "event-2", &event.Event{})
	require.False(t, called)
	require.False(t, Register(inv, "event-3", func(context.Context, *event.Event) {}))
}
