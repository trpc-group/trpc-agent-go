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

func TestResolveCurrentTurnSession_EdgeCases(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	root, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	}, session.StateMap{})
	require.NoError(t, err)
	owner := &recordingAgent{
		name:      "team",
		subAgents: []agent.Agent{&recordingAgent{name: "member"}},
	}
	got, err := ResolveCurrentTurnSession(ctx, service, nil, owner)
	require.NoError(t, err)
	require.Nil(t, got)
	got, err = ResolveCurrentTurnSession(ctx, service, root, nil)
	require.NoError(t, err)
	require.Same(t, root, got)
	root.SetState(currentTurnRouteStateKey("team"), []byte("{"))
	got, err = ResolveCurrentTurnSession(ctx, service, root, owner)
	require.Error(t, err)
	require.Nil(t, got)
	state, err := CurrentTurnRouteState(
		"team",
		"member",
		root,
		session.NewSession("app", "user", "root/team/member"),
	)
	require.NoError(t, err)
	ApplyCurrentTurnRouteState(root, state)
	got, err = ResolveCurrentTurnSession(ctx, nil, root, owner)
	require.Error(t, err)
	require.Nil(t, got)
	got, err = ResolveCurrentTurnSession(ctx, service, root, owner)
	require.NoError(t, err)
	require.Equal(t, "root/team/member", got.ID)
}

func TestCurrentTurnRouteState_ClearsRootRoute(t *testing.T) {
	root := session.NewSession("app", "user", "root")
	state, err := CurrentTurnRouteState("team", "member", root, root)
	require.NoError(t, err)
	key := currentTurnRouteStateKey("team")
	require.Contains(t, state, key)
	require.Nil(t, state[key])
}

func TestCurrentTurnRouteState_ClearsInvalidRoutes(t *testing.T) {
	root := session.NewSession("app", "user", "root")
	target := session.NewSession("app", "user", "root/team/member")
	cases := []struct {
		name        string
		owner       string
		targetAgent string
		root        *session.Session
		target      *session.Session
	}{
		{name: "nil root", owner: "team", targetAgent: "member", target: target},
		{name: "nil target", owner: "team", targetAgent: "member", root: root},
		{name: "empty owner", owner: " ", targetAgent: "member", root: root, target: target},
		{name: "empty target agent", owner: "team", targetAgent: " ", root: root, target: target},
		{name: "empty target session", owner: "team", targetAgent: "member", root: root, target: session.NewSession("app", "user", " ")},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			state, err := CurrentTurnRouteState(tt.owner, tt.targetAgent, tt.root, tt.target)
			require.NoError(t, err)
			key := currentTurnRouteStateKey(tt.owner)
			require.Contains(t, state, key)
			require.Nil(t, state[key])
		})
	}
}

func TestApplyCurrentTurnRouteState_NilAndEmptyState(t *testing.T) {
	require.NotPanics(t, func() {
		ApplyCurrentTurnRouteState(nil, session.StateMap{"route": []byte("member")})
	})
	root := session.NewSession("app", "user", "root")
	ApplyCurrentTurnRouteState(root, nil)
	_, ok := root.GetState("route")
	require.False(t, ok)
	ApplyCurrentTurnRouteState(root, session.StateMap{"route": []byte("member"), "clear": nil})
	route, ok := root.GetState("route")
	require.True(t, ok)
	require.Equal(t, []byte("member"), route)
	clear, ok := root.GetState("clear")
	require.True(t, ok)
	require.Nil(t, clear)
}

func TestHasCurrentTurnRoute(t *testing.T) {
	root := session.NewSession("app", "user", "root")
	require.False(t, HasCurrentTurnRoute("team", root))
	require.False(t, HasCurrentTurnRoute("team", nil))
	root.SetState(currentTurnRouteStateKey("team"), nil)
	require.False(t, HasCurrentTurnRoute("team", root))
	state, err := CurrentTurnRouteState("team", "member", root, session.NewSession("app", "user", "member"))
	require.NoError(t, err)
	ApplyCurrentTurnRouteState(root, state)
	require.True(t, HasCurrentTurnRoute("team", root))
}

func TestRouteEvent_NilAndUnroutedInputs(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("root"))
	evt := event.New("invocation", "agent")
	got, ok := RouteEvent(nil, evt)
	require.False(t, ok)
	require.Nil(t, got)
	got, ok = RouteEvent(inv, evt)
	require.False(t, ok)
	require.Nil(t, got)
	require.NotPanics(t, func() {
		AttachEventRouter(nil, &recordingEventRouter{})
		AttachEventRouter(inv, nil)
	})
	got, ok = RouteEvent(inv, evt)
	require.False(t, ok)
	require.Nil(t, got)
}

func TestSnapshotEventIdentity(t *testing.T) {
	require.Nil(t, SnapshotEventIdentity(nil))
	src := event.New("invocation", "agent")
	src.RequestID = "request"
	src.ParentInvocationID = "parent"
	src.Branch = "team/member"
	src.FilterKey = "filter"
	got := SnapshotEventIdentity(src)
	require.NotSame(t, src, got)
	require.Equal(t, "request", got.RequestID)
	require.Equal(t, "invocation", got.InvocationID)
	require.Equal(t, "parent", got.ParentInvocationID)
	require.Equal(t, "team/member", got.Branch)
	require.Equal(t, "filter", got.FilterKey)
	require.Nil(t, got.Response)
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
