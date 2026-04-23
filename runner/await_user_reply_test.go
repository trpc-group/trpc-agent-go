//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type awaitReplyTrackingAgent struct {
	name      string
	subAgents []agent.Agent
	calls     int
}

func (a *awaitReplyTrackingAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *awaitReplyTrackingAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

func (a *awaitReplyTrackingAgent) FindSubAgent(name string) agent.Agent {
	for _, sub := range a.subAgents {
		if sub != nil && sub.Info().Name == name {
			return sub
		}
	}
	return nil
}

func (a *awaitReplyTrackingAgent) Tools() []tool.Tool {
	return nil
}

func (a *awaitReplyTrackingAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	a.calls++
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(
				inv.InvocationID,
				a.name,
				&model.Response{
					Done: true,
					Choices: []model.Choice{{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: a.name,
						},
					}},
				},
			),
		)
	}()
	return ch, nil
}

func TestRunner_Run_AwaitUserReplyRoutingConsumesRoute(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName: "child",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	parent := &awaitReplyTrackingAgent{name: "parent"}
	child := &awaitReplyTrackingAgent{name: "child"}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAgent("child", child),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-consume"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 0, parent.calls)
	require.Equal(t, 1, child.calls)

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	_, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRunner_Run_AwaitUserReplyRoutingExplicitAgentWins(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-explicit",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName: "child",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	parent := &awaitReplyTrackingAgent{name: "parent"}
	child := &awaitReplyTrackingAgent{name: "child"}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAgent("child", child),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-explicit"),
		agent.WithAgentByName("parent"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 1, parent.calls)
	require.Equal(t, 0, child.calls)

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	route, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "child", route.AgentName)
}

func TestRunner_Run_AwaitUserReplyRoutingFallsBackWhenMissing(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-missing",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName: "missing",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	parent := &awaitReplyTrackingAgent{name: "parent"}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-missing"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 1, parent.calls)

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	_, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRunner_Run_AwaitUserReplyRoutingResolvesNestedSubAgent(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-nested",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName: "child",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	child := &awaitReplyTrackingAgent{name: "child"}
	parent := &awaitReplyTrackingAgent{
		name:      "parent",
		subAgents: []agent.Agent{child},
	}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-nested"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 0, parent.calls)
	require.Equal(t, 1, child.calls)
}
