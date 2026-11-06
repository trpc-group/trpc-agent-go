//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package openairesponses_test

import (
	"context"
	"encoding/json"
	"sort"
	"sync/atomic"
	"testing"

	sdkresponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func nativeShapeCallback(mode string) model.AfterModelCallbackStructured {
	return func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		if args.Response == nil || args.Response.IsPartial || len(args.Response.Choices) == 0 ||
			len(args.Response.Choices[0].Message.ToolCalls) == 0 {
			return nil, nil
		}
		response := args.Response
		if mode == "replacement" {
			response = response.Clone()
		}
		message := &response.Choices[0].Message
		var items []sdkresponses.ResponseOutputItemUnion
		if err := json.Unmarshal(message.ProviderData.Payload, &items); err != nil {
			return nil, err
		}
		output := make([]json.RawMessage, 0, len(items)+1)
		for _, item := range items {
			if item.Type == "function_call" && (mode == "remove" || (mode == "remove one" && item.CallID == "call_b")) {
				continue
			}
			raw := json.RawMessage(item.RawJSON())
			if item.Type == "message" && (mode == "text" || mode == "replacement") {
				var native sdkresponses.ResponseOutputMessageParam
				if err := json.Unmarshal(raw, &native); err != nil {
					return nil, err
				}
				for i := range native.Content {
					if part := native.Content[i].OfOutputText; part != nil {
						part.Text = "edited native text"
					}
				}
				encoded, err := json.Marshal(native)
				if err != nil {
					return nil, err
				}
				raw = encoded
			}
			if item.Type == "function_call" && mode == "invalid" {
				var native sdkresponses.ResponseFunctionToolCallParam
				if err := json.Unmarshal(raw, &native); err != nil {
					return nil, err
				}
				native.Arguments = "{"
				encoded, err := json.Marshal(native)
				if err != nil {
					return nil, err
				}
				raw = encoded
			}
			output = append(output, raw)
		}
		if mode == "add" {
			encoded, err := json.Marshal(sdkresponses.ResponseFunctionToolCallParam{
				CallID: "call_added", Name: "calculator", Arguments: `{"a":9}`,
			})
			if err != nil {
				return nil, err
			}
			output = append(output, encoded)
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			return nil, err
		}
		message.ProviderData.Payload = encoded
		if mode == "replacement" {
			return &model.AfterModelResult{CustomResponse: response}, nil
		}
		return nil, nil
	}
}

func TestNativeEditsDriveLLMAgentAndGraph(t *testing.T) {
	for _, graphMode := range []bool{false, true} {
		for _, mode := range []string{"remove", "remove one", "add", "text", "replacement", "invalid", "plugin"} {
			name := "llmagent/" + mode
			if graphMode {
				name = "graph/" + mode
			}
			t.Run(name, func(t *testing.T) {
				transport := newCallbackIsolationTransport()
				if mode == "remove one" {
					transport.firstOutput = callbackIsolationMultiToolOutput
				}
				m := callbackIsolationModel(transport)
				var calls atomic.Int32
				seen := make(chan int, 4)
				calculator := callbackIsolationCalculator(&calls, seen, false)
				tools := map[string]tool.Tool{"calculator": calculator}
				callbacks := model.NewCallbacks().RegisterAfterModel(nativeShapeCallback(mode))
				var runnerOptions []runner.Option
				if mode == "plugin" {
					callbacks = model.NewCallbacks()
					runnerOptions = append(runnerOptions, runner.WithPlugins(&callbackIsolationPlugin{
						name: "edit-native-output", afterModel: nativeShapeCallback("text"),
					}))
				}
				var a agent.Agent
				if graphMode {
					g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
						AddLLMNode("llm", m, "Use calculator.", tools,
							graph.WithModelCallbacks(callbacks), graph.WithGenerationConfig(model.GenerationConfig{Stream: true})).
						AddToolsNode("tools", tools).
						AddToolsConditionalEdges("llm", "tools", graph.End).
						AddEdge("tools", "llm").SetEntryPoint("llm").Compile()
					require.NoError(t, err)
					ga, err := graphagent.New("native", g)
					require.NoError(t, err)
					a = ga
				} else {
					a = llmagent.New("native", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{calculator}),
						llmagent.WithModelCallbacks(callbacks), llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}))
				}
				svc := inmemory.NewSessionService()
				t.Cleanup(func() { _ = svc.Close() })
				r := runner.NewRunner("native", a, append(runnerOptions, runner.WithSessionService(svc))...)
				t.Cleanup(func() { _ = r.Close() })
				events := runCallbackIsolation(t, r, "calculate", agent.WithGraphEmitFinalModelResponses(true))
				if mode == "invalid" {
					require.NotEmpty(t, callbackIsolationErrors(events))
					require.Zero(t, calls.Load())
					require.EqualValues(t, 1, transport.calls.Load())
					return
				}
				require.Empty(t, callbackIsolationErrors(events))
				want := 1
				if mode == "remove" {
					want = 0
				}
				if mode == "add" {
					want = 2
				}
				require.EqualValues(t, want, calls.Load())
				if mode == "remove" {
					require.EqualValues(t, 1, transport.calls.Load())
					return
				}
				values := make([]int, 0, want)
				for i := 0; i < want; i++ {
					values = append(values, <-seen)
				}
				sort.Ints(values)
				if mode == "add" {
					require.Equal(t, []int{1, 9}, values)
				} else {
					require.Equal(t, []int{1}, values)
				}
				<-transport.requests
				wire := <-transport.requests
				if mode == "text" || mode == "replacement" || mode == "plugin" {
					require.Contains(t, string(wire), "edited native text")
					require.Contains(t, string(wire), `"phase":"commentary"`)
					var found bool
					for _, evt := range events {
						if evt.Response != nil && len(evt.Choices) > 0 && evt.Choices[0].Message.Content == "edited native text" {
							found = true
						}
					}
					require.True(t, found, "publish the regenerated display projection")
				}
				if mode == "remove one" {
					require.Contains(t, string(wire), "call_a")
					require.NotContains(t, string(wire), "call_b")
				}
			})
		}
	}
}
