//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func nativeHistoryModel(t *testing.T, arguments string) (*Model, <-chan map[string]json.RawMessage, *atomic.Int32) {
	t.Helper()
	requests := make(chan map[string]json.RawMessage, 2)
	var calls atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if call > 2 {
			return nil, fmt.Errorf("unexpected model request %d", call)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		requests <- body
		output := []map[string]any{{
			"type": "function_call", "id": "fc_native", "call_id": "call_native",
			"name": "calculator", "arguments": arguments, "status": "completed",
		}}
		if call == 2 {
			output = []map[string]any{{
				"type": "message", "id": "msg_answer", "role": "assistant", "status": "completed",
				"content": []map[string]any{{"type": "output_text", "text": "2"}},
			}}
		}
		encoded, err := json.Marshal(map[string]any{
			"id": fmt.Sprintf("resp_%d", call), "status": "completed", "output": output,
		})
		if err != nil {
			return nil, err
		}
		contentType := "application/json"
		if bytes.Equal(bytes.TrimSpace(body["stream"]), []byte("true")) {
			encoded, err = json.Marshal(map[string]any{"type": "response.completed", "response": json.RawMessage(encoded)})
			if err != nil {
				return nil, err
			}
			encoded = []byte("data: " + string(encoded) + "\n\n")
			contentType = "text/event-stream"
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}},
			Body: io.NopCloser(bytes.NewReader(encoded)), Request: req,
		}, nil
	})
	m := New("test-model", WithAPIKey("test"), WithBaseURL("http://fixture.invalid/v1"),
		WithOpenAIOptions(option.WithHTTPClient(&http.Client{Transport: transport}), option.WithMaxRetries(0)))
	return m, requests, &calls
}

func TestNativeToolHistoryRetainsArguments(t *testing.T) {
	for _, path := range []string{"runner", "graph"} {
		for _, arguments := range []string{`{"a":1}`, " \n{\"a\":1}\t "} {
			t.Run(fmt.Sprintf("%s/%q", path, arguments), func(t *testing.T) {
				m, requests, modelCalls := nativeHistoryModel(t, arguments)
				var toolCalls atomic.Int32
				calculator := integrationCalculator(&toolCalls)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var events []*event.Event
				if path == "runner" {
					a := llmagent.New("native", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{calculator}))
					r := runner.NewRunner("native", a, runner.WithSessionService(inmemory.NewSessionService()))
					defer r.Close()
					events = runIntegration(t, r, ctx, "add one")
				} else {
					tools := map[string]tool.Tool{"calculator": calculator}
					g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
						AddLLMNode("llm", m, "Use the calculator.", tools).
						AddToolsNode("tools", tools).
						AddToolsConditionalEdges("llm", "tools", graph.End).
						AddEdge("tools", "llm").SetEntryPoint("llm").Compile()
					require.NoError(t, err)
					executor, err := graph.NewExecutor(g)
					require.NoError(t, err)
					ch, err := executor.Execute(ctx, graph.State{graph.StateKeyUserInput: "add one"},
						&agent.Invocation{InvocationID: "native-history"})
					require.NoError(t, err)
					for evt := range ch {
						events = append(events, evt)
					}
				}
				require.NoError(t, ctx.Err())
				for _, evt := range events {
					require.Nil(t, evt.Error)
				}
				require.EqualValues(t, 2, modelCalls.Load())
				require.EqualValues(t, 1, toolCalls.Load())
				<-requests
				var input []map[string]any
				require.NoError(t, json.Unmarshal((<-requests)["input"], &input))
				var hasCall, hasResult bool
				for _, item := range input {
					switch item["type"] {
					case "function_call":
						hasCall = true
						require.Equal(t, arguments, item["arguments"])
						require.Equal(t, "fc_native", item["id"])
					case "function_call_output":
						hasResult = true
						require.Equal(t, "call_native", item["call_id"])
					}
				}
				require.True(t, hasCall)
				require.True(t, hasResult)
			})
		}
	}
}

func TestGraphNativeHistoryRejectsMissingResultsAndEdits(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit bool
		want string
	}{
		{name: "missing result", want: "has no tool result"},
		{name: "edited arguments", edit: true, want: "projection changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, modelCalls := nativeHistoryModel(t, `{"a":1}`)
			msg := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
				Type: "function", ID: "call_native",
				Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(`{"a":1}`)},
			}}}
			require.NoError(t, msg.SetReplayData(m.Info().ReplayProtocol, m.Info().Name,
				json.RawMessage(`[{"type":"function_call","id":"fc_native","call_id":"call_native","name":"calculator","arguments":"{\"a\":1}"}]`)))
			history := []model.Message{model.NewUserMessage("add one"), msg}
			if tc.edit {
				history[1].ToolCalls[0].Function.Arguments = []byte(`{"a":2}`)
				history = append(history, model.Message{Role: model.RoleTool, ToolID: "call_native", Content: "2"})
			}
			g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
				AddLLMNode("llm", m, "Use the calculator.", nil).SetEntryPoint("llm").Compile()
			require.NoError(t, err)
			executor, err := graph.NewExecutor(g)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ch, err := executor.Execute(ctx, graph.State{graph.StateKeyMessages: history},
				&agent.Invocation{InvocationID: "invalid-native-history"})
			require.NoError(t, err)
			var errors []string
			for evt := range ch {
				if evt.Error != nil {
					errors = append(errors, evt.Error.Message)
				}
			}
			require.NoError(t, ctx.Err())
			require.NotEmpty(t, errors)
			require.Contains(t, errors[0], tc.want)
			require.Zero(t, modelCalls.Load(), "invalid history must fail before HTTP")
		})
	}
}
