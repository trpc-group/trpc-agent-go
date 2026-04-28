//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package noop

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRunnerWithNoopSessionService_GraphSeesCurrentUserMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const userMessage = "hello graph"
	ag := newGraphHistoryCheckAgent(t, "graph", userMessage)
	svc := NewService()
	r := runner.NewRunner("app", ag, runner.WithSessionService(svc))
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	events, err := r.Run(ctx, "user", "session", model.NewUserMessage(userMessage))
	require.NoError(t, err)
	requireNoRunError(t, events)

	stored, err := svc.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})
	require.NoError(t, err)
	require.Nil(t, stored)
}

func TestRunnerWithNoopSessionService_ChainGraphSeesUpstreamHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const upstreamMessage = "from upstream agent"
	graphChild := newGraphHistoryCheckAgent(t, "graph-child", upstreamMessage)
	chain := chainagent.New(
		"chain-parent",
		chainagent.WithSubAgents([]agent.Agent{
			&staticResponseAgent{name: "upstream", content: upstreamMessage},
			graphChild,
		}),
	)

	r := runner.NewRunner("app", chain, runner.WithSessionService(NewService()))
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	events, err := r.Run(ctx, "user", "session", model.NewUserMessage("start"))
	require.NoError(t, err)
	requireNoRunError(t, events)
}

func newGraphHistoryCheckAgent(t *testing.T, name string, want string) agent.Agent {
	t.Helper()
	compiled, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode("check", func(_ context.Context, state graph.State) (any, error) {
			raw, ok := state[graph.StateKeyMessages]
			if !ok {
				return nil, fmt.Errorf("messages not found")
			}
			messages, ok := raw.([]model.Message)
			if !ok {
				return nil, fmt.Errorf("messages has type %T", raw)
			}
			for _, msg := range messages {
				if strings.Contains(msg.Content, want) {
					return graph.State{
						graph.StateKeyLastResponse: "ok",
					}, nil
				}
			}
			return nil, fmt.Errorf("message containing %q not found", want)
		}).
		SetEntryPoint("check").
		SetFinishPoint("check").
		Compile()
	require.NoError(t, err)

	ag, err := graphagent.New(name, compiled)
	require.NoError(t, err)
	return ag
}

type staticResponseAgent struct {
	name    string
	content string
}

func (a *staticResponseAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *staticResponseAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *staticResponseAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *staticResponseAgent) Tools() []tool.Tool {
	return nil
}

func (a *staticResponseAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	events := make(chan *event.Event, 1)
	go func() {
		defer close(events)
		resp := &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(a.content),
			}},
		}
		_ = agent.EmitEvent(ctx, inv, events, event.NewResponseEvent(inv.InvocationID, a.name, resp))
	}()
	return events, nil
}

func requireNoRunError(t *testing.T, events <-chan *event.Event) {
	t.Helper()
	var completion *event.Event
	for evt := range events {
		require.NotNil(t, evt)
		if evt.Error != nil {
			t.Fatalf("unexpected run error: %s", evt.Error.Message)
		}
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
}
