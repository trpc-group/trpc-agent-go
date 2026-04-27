//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package eventcontrol

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestSkipPersistence(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("root"))
	evt := event.New("invocation", "agent")
	require.False(t, SkipPersistence(inv, evt))
	MarkSkipPersistence(inv, evt)
	require.True(t, SkipPersistence(inv, evt))
	require.Nil(t, evt.Extensions)
	replacement := event.New("invocation", "agent")
	require.True(t, SkipPersistence(inv, replacement))
	require.False(t, SkipPersistence(inv, nil))
	require.False(t, SkipPersistence(nil, evt))
}

func TestHandlePersistence(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("root"))
	evt := event.New("invocation", "agent")
	handler := &recordingPersistenceHandler{}
	AttachPersistenceHandler(inv, handler)
	require.True(t, HandlePersistence(context.Background(), inv, evt, evt))
	require.Same(t, inv, handler.root)
	require.Same(t, evt, handler.routeEvent)
	require.Same(t, evt, handler.event)
}

func TestAttachPersistenceHandler_AttachesAncestors(t *testing.T) {
	root := agent.NewInvocation(agent.WithInvocationID("root"))
	child := root.Clone(agent.WithInvocationID("child"))
	evt := event.New("child", "agent")
	handler := &recordingPersistenceHandler{}
	AttachPersistenceHandler(child, handler)
	require.True(t, HandlePersistence(context.Background(), root, evt, evt))
	require.Same(t, root, handler.root)
	require.Same(t, evt, handler.routeEvent)
	require.Same(t, evt, handler.event)
}

type recordingPersistenceHandler struct {
	root       *agent.Invocation
	routeEvent *event.Event
	event      *event.Event
}

func (h *recordingPersistenceHandler) HandleEventPersistence(
	_ context.Context,
	root *agent.Invocation,
	routeEvt *event.Event,
	evt *event.Event,
) bool {
	h.root = root
	h.routeEvent = routeEvt
	h.event = evt
	return true
}
