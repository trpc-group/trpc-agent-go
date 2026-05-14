//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sessionroute

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRouteEvent(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("root"))
	evt := event.New("invocation", "agent")
	want := session.NewSession("app", "user", "member")
	router := &recordingEventRouter{session: want}
	AttachEventRouter(inv, router)
	got, ok := RouteEvent(inv, evt)
	require.True(t, ok)
	require.Same(t, want, got)
	require.Same(t, inv, router.root)
	require.Same(t, evt, router.event)
}

func TestAttachEventRouter_AttachesAncestors(t *testing.T) {
	root := agent.NewInvocation(agent.WithInvocationID("root"))
	child := root.Clone(agent.WithInvocationID("child"))
	evt := event.New("child", "agent")
	want := session.NewSession("app", "user", "member")
	router := &recordingEventRouter{session: want}
	AttachEventRouter(child, router)
	got, ok := RouteEvent(root, evt)
	require.True(t, ok)
	require.Same(t, want, got)
	require.Same(t, root, router.root)
	require.Same(t, evt, router.event)
}

func TestResolveCurrentTurnSession(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	root, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	}, session.StateMap{})
	require.NoError(t, err)
	member := session.NewSession("app", "user", "root/team/member")
	state, err := CurrentTurnRouteState("team", "member", root, member)
	require.NoError(t, err)
	ApplyCurrentTurnRouteState(root, state)
	require.NoError(t, service.UpdateSessionState(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	}, state))
	owner := &recordingAgent{
		name:      "team",
		subAgents: []agent.Agent{&recordingAgent{name: "member"}},
	}
	got, err := ResolveCurrentTurnSession(ctx, service, root, owner)
	require.NoError(t, err)
	require.Equal(t, "root/team/member", got.ID)
	got, err = ResolveCurrentTurnSession(ctx, service, root, &recordingAgent{name: "other"})
	require.NoError(t, err)
	require.Same(t, root, got)
	owner.subAgents = nil
	got, err = ResolveCurrentTurnSession(ctx, service, root, owner)
	require.NoError(t, err)
	require.Same(t, root, got)
}

func TestCurrentTurnRouteState_ClearsRootRoute(t *testing.T) {
	root := session.NewSession("app", "user", "root")
	state, err := CurrentTurnRouteState("team", "member", root, root)
	require.NoError(t, err)
	key := currentTurnRouteStateKey("team")
	require.Contains(t, state, key)
	require.Nil(t, state[key])
}

func TestHasCurrentTurnRoute(t *testing.T) {
	root := session.NewSession("app", "user", "root")
	require.False(t, HasCurrentTurnRoute("team", root))
	state, err := CurrentTurnRouteState("team", "member", root, session.NewSession("app", "user", "member"))
	require.NoError(t, err)
	ApplyCurrentTurnRouteState(root, state)
	require.True(t, HasCurrentTurnRoute("team", root))
}

type recordingEventRouter struct {
	root    *agent.Invocation
	event   *event.Event
	session *session.Session
}

func (r *recordingEventRouter) RouteEvent(
	root *agent.Invocation,
	evt *event.Event,
) (*session.Session, bool) {
	r.root = root
	r.event = evt
	return r.session, r.session != nil
}

type recordingAgent struct {
	name      string
	subAgents []agent.Agent
}

func (a *recordingAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *recordingAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

func (a *recordingAgent) FindSubAgent(name string) agent.Agent {
	for _, sub := range a.subAgents {
		if sub != nil && sub.Info().Name == name {
			return sub
		}
	}
	return nil
}

func (a *recordingAgent) Tools() []tool.Tool {
	return nil
}

func (a *recordingAgent) Run(
	context.Context,
	*agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}
