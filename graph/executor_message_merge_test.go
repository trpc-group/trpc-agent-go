//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Test that when a node returns State{messages: MessageOp}, the schema reducer
// applies the operation and messages remain a []model.Message, not []MessageOp.
func TestMessagesReducerAppliesMessageOps(t *testing.T) {
	schema := MessagesStateSchema()
	sg := NewStateGraph(schema)

	sg.
		AddNode("seed", func(ctx context.Context, s State) (any, error) {
			return State{StateKeyMessages: []model.Message{model.NewUserMessage("u")}}, nil
		}).
		AddNode("op", func(ctx context.Context, s State) (any, error) {
			return State{StateKeyMessages: AppendMessages{Items: []model.Message{model.NewAssistantMessage("a")}}, StateKeyLastResponse: "a"}, nil
		}).
		SetEntryPoint("seed").
		AddEdge("seed", "op").
		SetFinishPoint("op")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "merge-msg-op"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	var finalMessages []model.Message
	for e := range ch {
		if e.Done && e.StateDelta != nil {
			if data, ok := e.StateDelta[StateKeyMessages]; ok {
				var msgs []model.Message
				require.NoError(t, json.Unmarshal(data, &msgs))
				finalMessages = msgs
			}
		}
	}
	require.Len(t, finalMessages, 2)
	require.Equal(t, model.RoleUser, finalMessages[0].Role)
	require.Equal(t, model.RoleAssistant, finalMessages[1].Role)
}

// Test that AddToolsConditionalEdges routes to the tools node when tool-calls
// are present in the assistant message.
func TestAddToolsConditionalEdgesRoutesToTools(t *testing.T) {
	schema := MessagesStateSchema()
	sg := NewStateGraph(schema)

	sg.
		AddNode("llm", func(ctx context.Context, s State) (any, error) {
			// Simulate an assistant message containing a tool call
			msgs := []model.Message{
				model.NewUserMessage("hi"),
				{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Type:     "function",
							Function: model.FunctionDefinitionParam{Name: "dummy"},
							ID:       "call-1",
						},
					},
				},
			}
			return State{StateKeyMessages: msgs}, nil
		}).
		AddNode("tools", func(ctx context.Context, s State) (any, error) {
			return State{"routed": "tools"}, nil
		}).
		AddNode("fallback", func(ctx context.Context, s State) (any, error) {
			return State{"routed": "fallback"}, nil
		}).
		SetEntryPoint("llm").
		SetFinishPoint("tools").
		SetFinishPoint("fallback").
		AddToolsConditionalEdges("llm", "tools", "fallback")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "tools-route"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	var routed string
	for e := range ch {
		if e.Done && e.StateDelta != nil {
			if b, ok := e.StateDelta["routed"]; ok {
				require.NoError(t, json.Unmarshal(b, &routed))
			}
		}
	}
	require.Equal(t, "tools", routed)
}
