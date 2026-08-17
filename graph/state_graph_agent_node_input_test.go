//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/userinputkey"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExecutor_InvocationMessageLifecycle(t *testing.T) {
	hello := "hello"
	typed := model.Message{
		Role:             model.RoleUser,
		ReasoningContent: "keep-meta",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
			{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/current.png"},
			},
		},
	}
	historical := model.Message{
		Role:    model.RoleUser,
		Content: "old",
		ContentParts: []model.ContentPart{{
			Type:  model.ContentTypeImage,
			Image: &model.Image{URL: "https://example.com/old.png"},
		}},
	}
	var sawBefore bool
	var sawAfter bool
	var prepareMsg model.Message
	var prepareMsgOK bool
	sub := &recordingAgent{name: "child"}
	g, err := NewStateGraph(MessagesStateSchema()).
		AddNode("prepare", func(_ context.Context, state State) (any, error) {
			prepareMsg, prepareMsgOK = state[userinputkey.Message].(model.Message)
			sawBefore = true
			return State{"prepared": true}, nil
		}).
		AddAgentNode("child").
		AddNode("after", func(_ context.Context, state State) (any, error) {
			_, sawAfter = state[userinputkey.Message]
			return nil, nil
		}).
		SetEntryPoint("prepare").
		AddEdge("prepare", "child").
		AddEdge("child", "after").
		SetFinishPoint("after").
		Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	events, err := exec.Execute(
		context.Background(),
		State{
			StateKeyParentAgent:  &parentWithSubAgent{a: sub},
			StateKeyMessages:     []model.Message{historical},
			StateKeyUserInput:    hello,
			userinputkey.Message: typed,
		},
		agent.NewInvocation(agent.WithInvocationID("inv-message-lifecycle")),
	)
	require.NoError(t, err)
	for evt := range events {
		if evt != nil && evt.Done && evt.Object == ObjectTypeGraphExecution {
			require.NotContains(t, evt.StateDelta, userinputkey.Message)
		}
	}
	require.True(t, sawBefore)
	require.True(t, prepareMsgOK)
	require.True(t, model.MessagesEqual(typed, prepareMsg))
	require.False(t, sawAfter)
	got := sub.capturedInvocation().Message
	require.True(t, model.MessagesEqual(typed, got))
	require.Equal(t, "keep-meta", got.ReasoningContent)
	require.False(t, model.MessagesEqual(historical, got))
}

func TestExecutor_InvocationMessageKeptOnNodeError(t *testing.T) {
	hello := "hello"
	typed := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
		},
	}
	tests := []struct {
		name  string
		build func(*testing.T) (*Graph, State)
	}{
		{
			name: "agent error",
			build: func(t *testing.T) (*Graph, State) {
				t.Helper()
				g, err := NewStateGraph(MessagesStateSchema()).
					AddAgentNode("child").
					SetEntryPoint("child").
					SetFinishPoint("child").
					Compile()
				require.NoError(t, err)
				return g, State{
					StateKeyParentAgent:  &parentWithSubAgent{a: &errAgent{name: "child"}},
					StateKeyUserInput:    hello,
					userinputkey.Message: typed,
					CfgKeyLineageID:      "ln-message-error",
				}
			},
		},
		{
			name: "llm error",
			build: func(t *testing.T) (*Graph, State) {
				t.Helper()
				g, err := NewStateGraph(MessagesStateSchema()).
					AddLLMNode("ask", &errModel{}, "", nil).
					SetEntryPoint("ask").
					SetFinishPoint("ask").
					Compile()
				require.NoError(t, err)
				return g, State{
					StateKeyUserInput:    hello,
					userinputkey.Message: typed,
					CfgKeyLineageID:      "ln-message-llm-error",
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, initial := tt.build(t)
			saver := newMockSaver()
			exec, err := NewExecutor(g, WithCheckpointSaver(saver))
			require.NoError(t, err)
			events, err := exec.Execute(
				context.Background(),
				initial,
				agent.NewInvocation(agent.WithInvocationID(initial[CfgKeyLineageID].(string))),
			)
			require.NoError(t, err)
			for range events {
			}
			require.NotEmpty(t, saver.byID)
			var sawTyped bool
			for _, tuple := range saver.byID {
				if tuple == nil || tuple.Checkpoint == nil {
					continue
				}
				got, ok := decodeInvocationUserMessage(tuple.Checkpoint.ChannelValues[userinputkey.Message])
				if !ok {
					continue
				}
				require.True(t, model.MessagesEqual(typed, got))
				sawTyped = true
			}
			require.True(t, sawTyped)
		})
	}
}

func TestExecutor_ParallelAgentNodesDoNotAliasTypedMessage(t *testing.T) {
	hello := "hello"
	typed := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
			{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/a.png", Data: []byte("img")},
			},
		},
	}
	left := &mutatingRecordingAgent{name: "left"}
	right := &mutatingRecordingAgent{name: "right"}
	g, err := NewStateGraph(MessagesStateSchema()).
		AddNode("fork", func(context.Context, State) (any, error) {
			return nil, nil
		}).
		AddAgentNode("left").
		AddAgentNode("right").
		SetEntryPoint("fork").
		AddEdge("fork", "left").
		AddEdge("fork", "right").
		SetFinishPoint("left").
		SetFinishPoint("right").
		Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	events, err := exec.Execute(
		context.Background(),
		State{
			StateKeyParentAgent: &parentWithNamedSubAgents{agents: map[string]agent.Agent{
				"left":  left,
				"right": right,
			}},
			StateKeyUserInput:    hello,
			userinputkey.Message: typed,
		},
		agent.NewInvocation(agent.WithInvocationID("inv-parallel-message")),
	)
	require.NoError(t, err)
	for range events {
	}

	require.Equal(t, "left", *left.got.ContentParts[0].Text)
	require.Equal(t, "right", *right.got.ContentParts[0].Text)
	require.Equal(t, "hello", *typed.ContentParts[0].Text)
	require.Equal(t, "https://example.com/a.png", typed.ContentParts[1].Image.URL)
}

// A non-default AgentNode input path still ends the default user_input
// one-shot, so a following default AgentNode must not re-send the current
// invocation's media. Upstream delivered NewUserMessage("") there. A custom
// user input key owns a different one-shot, so the typed message stays
// pending for the later default AgentNode.
func TestExecutor_InvocationMessageNotResentToSecondDefaultAgentNode(t *testing.T) {
	hello := "hello"
	typed := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
			{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/a.png"},
			},
		},
	}
	toolMsg := model.NewToolMessage("call-1", "handoff_task", "done")
	tests := []struct {
		name       string
		opts       []Option
		setup      func(State)
		wantFirst  model.Message
		wantSecond model.Message
	}{
		{
			name: "custom user input key",
			opts: []Option{WithUserInputKey("custom_input")},
			setup: func(state State) {
				state["custom_input"] = "from-custom"
			},
			wantFirst:  model.NewUserMessage("from-custom"),
			wantSecond: typed,
		},
		{
			name: "input mapper",
			opts: []Option{WithAgentNodeInputMapper(func(State) State {
				return State{StateKeyAgentInputMessage: &toolMsg}
			})},
			wantFirst:  toolMsg,
			wantSecond: model.NewUserMessage(""),
		},
		{
			name: "agent input message state",
			setup: func(state State) {
				state[StateKeyAgentInputMessage] = &toolMsg
			},
			wantFirst:  toolMsg,
			wantSecond: model.NewUserMessage(""),
		},
		{
			name: "input from last response",
			opts: []Option{WithSubgraphInputFromLastResponse()},
			setup: func(state State) {
				state[StateKeyLastResponse] = "upstream-output"
			},
			wantFirst:  model.NewUserMessage("upstream-output"),
			wantSecond: model.NewUserMessage(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := &recordingAgent{name: "first"}
			second := &recordingAgent{name: "second"}
			g, err := NewStateGraph(MessagesStateSchema()).
				AddAgentNode("first", tt.opts...).
				AddAgentNode("second").
				SetEntryPoint("first").
				AddEdge("first", "second").
				SetFinishPoint("second").
				Compile()
			require.NoError(t, err)
			exec, err := NewExecutor(g)
			require.NoError(t, err)

			initial := State{
				StateKeyParentAgent: &parentWithNamedSubAgents{agents: map[string]agent.Agent{
					"first":  first,
					"second": second,
				}},
				StateKeyUserInput:    hello,
				userinputkey.Message: typed,
			}
			if tt.setup != nil {
				tt.setup(initial)
			}
			events, err := exec.Execute(
				context.Background(),
				initial,
				agent.NewInvocation(agent.WithInvocationID("inv-second-default")),
			)
			require.NoError(t, err)
			for range events {
			}
			require.Equal(t, tt.wantFirst, first.capturedInvocation().Message)
			require.Equal(t, tt.wantSecond, second.capturedInvocation().Message)
		})
	}
}

// A plain function node that ends the default user_input one-shot retires the
// typed message, matching the upstream NewUserMessage("") handoff.
func TestExecutor_InvocationMessageConsumedByPlainUserInputClear(t *testing.T) {
	hello := "hello"
	typed := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
			{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/a.png"},
			},
		},
	}
	sub := &recordingAgent{name: "child"}
	g, err := NewStateGraph(MessagesStateSchema()).
		AddNode("clear", func(context.Context, State) (any, error) {
			return State{StateKeyUserInput: ""}, nil
		}).
		AddAgentNode("child").
		SetEntryPoint("clear").
		AddEdge("clear", "child").
		SetFinishPoint("child").
		Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	events, err := exec.Execute(
		context.Background(),
		State{
			StateKeyParentAgent:  &parentWithSubAgent{a: sub},
			StateKeyUserInput:    hello,
			userinputkey.Message: typed,
		},
		agent.NewInvocation(agent.WithInvocationID("inv-plain-clear")),
	)
	require.NoError(t, err)
	for range events {
	}
	require.Equal(t, model.NewUserMessage(""), sub.capturedInvocation().Message)
}

// Payload-only input has no user_input at all, so an ordinary prepare node
// must not retire the typed message before the default AgentNode runs.
func TestExecutor_PureMediaInvocationMessageSurvivesPrepareNode(t *testing.T) {
	typed := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{{
			Type:  model.ContentTypeImage,
			Image: &model.Image{URL: "https://example.com/only.png"},
		}},
	}
	sub := &recordingAgent{name: "child"}
	g, err := NewStateGraph(MessagesStateSchema()).
		AddNode("prepare", func(context.Context, State) (any, error) {
			return State{"prepared": true}, nil
		}).
		AddAgentNode("child").
		SetEntryPoint("prepare").
		AddEdge("prepare", "child").
		SetFinishPoint("child").
		Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	events, err := exec.Execute(
		context.Background(),
		State{
			StateKeyParentAgent:  &parentWithSubAgent{a: sub},
			StateKeyMessages:     []model.Message{typed},
			userinputkey.Message: typed,
		},
		agent.NewInvocation(agent.WithInvocationID("inv-pure-media-prepare")),
	)
	require.NoError(t, err)
	for range events {
	}
	require.Equal(t, typed, sub.capturedInvocation().Message)
}

// An AfterNode override replaces the node result, which already suppresses
// the pre-existing user_input clear. Consumption follows the override's own
// user_input decision rather than the discarded tombstone.
func TestExecutor_InvocationMessageFollowsAfterNodeOverrideUserInput(t *testing.T) {
	hello := "hello"
	typed := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
			{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/a.png"},
			},
		},
	}
	tests := []struct {
		name       string
		override   State
		wantSecond model.Message
	}{
		{
			// Clears the default one-shot but drops the tombstone.
			name:       "override clears user input",
			override:   State{StateKeyUserInput: "", StateKeyLastResponse: "audited"},
			wantSecond: model.NewUserMessage(""),
		},
		{
			// Suppresses the clear, so the turn's input stays pending.
			name:       "override omits user input",
			override:   State{StateKeyLastResponse: "audited"},
			wantSecond: typed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := &recordingAgent{name: "first"}
			second := &recordingAgent{name: "second"}
			override := tt.override
			callbacks := NewNodeCallbacks().RegisterAfterNode(func(
				_ context.Context,
				cbCtx *NodeCallbackContext,
				_ State,
				_ any,
				_ error,
			) (any, error) {
				if cbCtx == nil || cbCtx.NodeID != "first" {
					return nil, nil
				}
				return override.Clone(), nil
			})
			g, err := NewStateGraph(MessagesStateSchema()).
				AddAgentNode("first").
				AddAgentNode("second").
				SetEntryPoint("first").
				AddEdge("first", "second").
				SetFinishPoint("second").
				Compile()
			require.NoError(t, err)
			exec, err := NewExecutor(g)
			require.NoError(t, err)

			events, err := exec.Execute(
				context.Background(),
				State{
					StateKeyParentAgent: &parentWithNamedSubAgents{agents: map[string]agent.Agent{
						"first":  first,
						"second": second,
					}},
					StateKeyNodeCallbacks: callbacks,
					StateKeyUserInput:     hello,
					userinputkey.Message:  typed,
				},
				agent.NewInvocation(agent.WithInvocationID("inv-after-node-override")),
			)
			require.NoError(t, err)
			for range events {
			}
			require.Equal(t, typed, first.capturedInvocation().Message)
			require.Equal(t, tt.wantSecond, second.capturedInvocation().Message)
		})
	}
}

// A cache hit replays the stored node result instead of running the node, so
// the consume decision must still be derivable from that result.
func TestExecutor_InvocationMessageConsumedOnCacheHit(t *testing.T) {
	hello := "hello"
	typed := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &hello},
			{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/a.png"},
			},
		},
	}
	var clearRuns atomic.Int32
	sub := &recordingAgent{name: "child"}
	// Cache only the clearing node so the agent node always runs and records
	// the message it actually received.
	sg := NewStateGraph(MessagesStateSchema()).WithCache(NewInMemoryCache())
	sg.AddNode("clear", func(context.Context, State) (any, error) {
		clearRuns.Add(1)
		return State{StateKeyUserInput: ""}, nil
	}, WithNodeCachePolicy(DefaultCachePolicy()), WithCacheKeyFields(StateKeyUserInput)).
		AddAgentNode("child").
		SetEntryPoint("clear").
		AddEdge("clear", "child").
		SetFinishPoint("child")
	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	run := func(id string) model.Message {
		events, err := exec.Execute(
			context.Background(),
			State{
				StateKeyParentAgent:  &parentWithSubAgent{a: sub},
				StateKeyUserInput:    hello,
				userinputkey.Message: typed,
			},
			agent.NewInvocation(agent.WithInvocationID(id)),
		)
		require.NoError(t, err)
		for range events {
		}
		return sub.capturedInvocation().Message
	}

	require.Equal(t, model.NewUserMessage(""), run("inv-cache-miss"))
	require.Equal(t, model.NewUserMessage(""), run("inv-cache-hit"))
	require.Equal(t, int32(1), clearRuns.Load(), "second run should hit the cache")
}

type parentWithNamedSubAgents struct {
	agents map[string]agent.Agent
}

func (p *parentWithNamedSubAgents) FindSubAgent(name string) agent.Agent {
	if p == nil {
		return nil
	}
	return p.agents[name]
}

type mutatingRecordingAgent struct {
	name string
	got  model.Message
}

func (a *mutatingRecordingAgent) Info() agent.Info { return agent.Info{Name: a.name} }
func (a *mutatingRecordingAgent) Tools() []tool.Tool {
	return nil
}
func (a *mutatingRecordingAgent) SubAgents() []agent.Agent { return nil }
func (a *mutatingRecordingAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *mutatingRecordingAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	if inv == nil {
		return nil, errors.New("nil invocation")
	}
	a.got = inv.Message
	if len(inv.Message.ContentParts) > 0 && inv.Message.ContentParts[0].Text != nil {
		*inv.Message.ContentParts[0].Text = a.name
	}
	return (&recordingAgent{name: a.name}).Run(ctx, inv)
}
