//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	gr "trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// nativeToolModel calls the calculator natively once and then answers.
type nativeToolModel struct {
	mu    sync.Mutex
	calls int
}

func (m *nativeToolModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.calls++
	first := m.calls == 1
	m.mu.Unlock()
	msg := model.NewAssistantMessage("done")
	if first {
		msg = model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call_1",
			Function: model.FunctionDefinitionParam{
				Name:      "calculator",
				Arguments: []byte(`{"a":1}`),
			},
		}}}
	}
	msg.ProviderData = &model.ProviderData{
		Protocol: "test.native",
		Model:    "native-model",
		Payload: json.RawMessage(
			`[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"}]`,
		),
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}
	close(ch)
	return ch, nil
}

func (m *nativeToolModel) Info() model.Info {
	return model.Info{Name: "native-model", ProviderDataProtocol: "test.native"}
}

func TestGraphNativeToolCallsAfterModel(t *testing.T) {
	for _, tc := range []struct {
		name             string
		edit             bool
		dropProviderData bool
		replaceResponse  bool
		wantExecutions   int32
		wantInput        int32
	}{
		{name: "unchanged", wantExecutions: 1, wantInput: 1},
		{name: "edited with the record retained", edit: true, wantExecutions: 1, wantInput: 1000},
		{name: "edited with the record dropped", edit: true, dropProviderData: true, wantExecutions: 1, wantInput: 1000},
		{name: "replaced with plain text", replaceResponse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var executed, input atomic.Int32
			calculator := function.NewFunctionTool(func(_ context.Context, in struct {
				A int `json:"a"`
			}) (int, error) {
				executed.Add(1)
				input.Store(int32(in.A))
				return in.A + 1, nil
			}, function.WithName("calculator"))
			tools := map[string]tool.Tool{"calculator": calculator}
			callbacks := model.NewCallbacks().RegisterAfterModel(func(
				_ context.Context,
				_ *model.Request,
				rsp *model.Response,
				_ error,
			) (*model.Response, error) {
				if len(rsp.Choices) == 0 || len(rsp.Choices[0].Message.ToolCalls) == 0 {
					return nil, nil
				}
				if tc.replaceResponse {
					return &model.Response{Done: true, Choices: []model.Choice{{
						Message: model.NewAssistantMessage("tool call declined"),
					}}}, nil
				}
				if tc.edit {
					rsp.Choices[0].Message.ToolCalls[0].Function.Arguments = []byte(`{"a":1000}`)
					if tc.dropProviderData {
						rsp.Choices[0].Message.ProviderData = nil
					}
				}
				return nil, nil
			})
			llm := &nativeToolModel{}
			g, err := gr.NewStateGraph(gr.MessagesStateSchema()).
				AddLLMNode("llm", llm, "Use the calculator.", tools, gr.WithModelCallbacks(callbacks)).
				AddToolsNode("tools", tools).
				AddToolsConditionalEdges("llm", "tools", gr.End).
				AddEdge("tools", "llm").
				SetEntryPoint("llm").
				Compile()
			require.NoError(t, err)
			exec, err := gr.NewExecutor(g)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			events, err := exec.Execute(ctx, gr.State{gr.StateKeyUserInput: "add one"},
				&agent.Invocation{InvocationID: "inv"})
			require.NoError(t, err)
			var errMessage string
			for evt := range events {
				if evt.Error != nil && errMessage == "" {
					errMessage = evt.Error.Message
				}
			}

			require.Empty(t, errMessage)
			require.Equal(t, tc.wantExecutions, executed.Load())
			require.Equal(t, tc.wantInput, input.Load())
			require.Equal(t, int(tc.wantExecutions)+1, llm.calls)
		})
	}
}
