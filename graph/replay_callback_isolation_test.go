//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	gr "trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	callbackReplayProtocol = "test.native"
	callbackReplayModel    = "callback-model"
)

type callbackReplayModelStub struct {
	mu       sync.Mutex
	calls    int
	requests [][]model.Message
	native   bool
}

func (m *callbackReplayModelStub) Info() model.Info {
	info := model.Info{Name: callbackReplayModel}
	if m.native {
		info.ReplayProtocol = callbackReplayProtocol
	}
	return info
}

func (m *callbackReplayModelStub) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.requests = append(m.requests, cloneCallbackReplayMessages(req.Messages))
	m.mu.Unlock()

	msg := model.NewAssistantMessage("calculation complete")
	if call == 1 {
		msg = model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call_1",
			Function: model.FunctionDefinitionParam{
				Name:      "calculator",
				Arguments: []byte(`{"a":1}`),
			},
		}}}
		if m.native {
			if err := msg.SetReplayData(callbackReplayProtocol, callbackReplayModel,
				json.RawMessage(`[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"calculator","arguments":"{\"a\":1}"}]`)); err != nil {
				return nil, err
			}
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}
	close(ch)
	return ch, nil
}

func (m *callbackReplayModelStub) requestSnapshot() (int, [][]model.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]model.Message, len(m.requests))
	for i := range m.requests {
		out[i] = cloneCallbackReplayMessages(m.requests[i])
	}
	return m.calls, out
}

func cloneCallbackReplayMessages(in []model.Message) []model.Message {
	out := make([]model.Message, len(in))
	copy(out, in)
	for i := range out {
		if out[i].Replay != nil {
			record := *out[i].Replay
			out[i].Replay = &record
		}
		out[i].ToolCalls = append([]model.ToolCall(nil), out[i].ToolCalls...)
		for j := range out[i].ToolCalls {
			out[i].ToolCalls[j].Function.Arguments = append([]byte(nil), out[i].ToolCalls[j].Function.Arguments...)
		}
	}
	return out
}

type callbackReplayRun struct {
	messages    []model.Message
	modelCalls  int
	requests    [][]model.Message
	toolValue   int32
	callbackIn  string
	callbackErr error
}

type callbackReplayScope int

const (
	callbackReplayNodeScope callbackReplayScope = iota
	callbackReplayPluginScope
)

func runCallbackReplayGraph(t *testing.T, native bool, scope callbackReplayScope) callbackReplayRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var toolInput atomic.Int32
	calculator := function.NewFunctionTool(func(_ context.Context, input struct {
		A int `json:"a"`
	}) (int, error) {
		toolInput.Store(int32(input.A))
		return input.A + 1, nil
	}, function.WithName("calculator"))
	tools := map[string]tool.Tool{"calculator": calculator}
	modelStub := &callbackReplayModelStub{native: native}
	captured := make(chan []model.Message, 1)

	mutate := func(args *tool.BeforeToolArgs) error {
		if string(args.Arguments) != `{"a":1}` {
			return fmt.Errorf("callback received arguments %q, want original arguments", args.Arguments)
		}
		args.Arguments[5] = '9'
		return nil
	}
	var callbackInput string
	var callbackErr error
	var callbacks *tool.Callbacks
	manager := (*plugin.Manager)(nil)
	if scope == callbackReplayNodeScope {
		callbacks = tool.NewCallbacks()
		callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			callbackInput = string(args.Arguments)
			callbackErr = mutate(args)
			return &tool.BeforeToolResult{Context: ctx}, callbackErr
		})
	} else {
		manager = plugin.MustNewManager(&callbackReplayMutationPlugin{
			observe: func(input string, err error) {
				callbackInput = input
				callbackErr = err
			},
		})
	}

	inspect := func(_ context.Context, state gr.State) (any, error) {
		messages, ok := state[gr.StateKeyMessages].([]model.Message)
		if !ok {
			return nil, fmt.Errorf("messages state has type %T", state[gr.StateKeyMessages])
		}
		captured <- cloneCallbackReplayMessages(messages)
		return nil, nil
	}
	toolOptions := make([]gr.Option, 0, 1)
	if callbacks != nil {
		toolOptions = append(toolOptions, gr.WithToolCallbacks(callbacks))
	}
	builder := gr.NewStateGraph(gr.MessagesStateSchema()).
		AddLLMNode("llm", modelStub, "Use the calculator.", tools).
		AddToolsNode("tools", tools, toolOptions...).
		AddNode("inspect", inspect).
		AddToolsConditionalEdges("llm", "tools", "inspect").
		AddEdge("tools", "llm").
		SetEntryPoint("llm").SetFinishPoint("inspect")
	g, err := builder.Compile()
	require.NoError(t, err)
	executor, err := gr.NewExecutor(g)
	require.NoError(t, err)
	invocationOpts := []agent.InvocationOptions{agent.WithInvocationID("callback-replay")}
	if manager != nil {
		invocationOpts = append(invocationOpts, agent.WithInvocationPlugins(manager))
	}
	inv := agent.NewInvocation(invocationOpts...)
	events, err := executor.Execute(ctx, gr.State{gr.StateKeyUserInput: "calculate"}, inv)
	require.NoError(t, err)
	var runErr error
	for evt := range events {
		if evt != nil && evt.Error != nil && runErr == nil {
			runErr = fmt.Errorf("%s", evt.Error.Message)
		}
	}
	require.NoError(t, ctx.Err(), "graph execution should finish before the test deadline")
	calls, requests := modelStub.requestSnapshot()
	result := callbackReplayRun{
		modelCalls:  calls,
		requests:    requests,
		toolValue:   toolInput.Load(),
		callbackIn:  callbackInput,
		callbackErr: callbackErr,
	}
	if runErr == nil {
		select {
		case result.messages = <-captured:
		default:
			t.Fatal("inspect node did not capture final graph messages")
		}
	}
	return result
}

type callbackReplayMutationPlugin struct {
	observe func(string, error)
}

func (p *callbackReplayMutationPlugin) Name() string { return "callback-replay-mutation" }

func (p *callbackReplayMutationPlugin) Register(r *plugin.Registry) {
	r.BeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		input := string(args.Arguments)
		err := error(nil)
		if input != `{"a":1}` {
			err = fmt.Errorf("plugin callback received arguments %q, want original arguments", input)
		} else {
			args.Arguments[5] = '9'
		}
		p.observe(input, err)
		return &tool.BeforeToolResult{Context: ctx}, err
	})
}

func findCallbackReplayCall(t *testing.T, messages []model.Message) model.Message {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == model.RoleAssistant && len(msg.ToolCalls) > 0 {
			return msg
		}
	}
	t.Fatal("assistant tool call not found in graph messages")
	return model.Message{}
}

func assertCallbackReplayBinding(t *testing.T, msg model.Message, wantArguments string) {
	t.Helper()
	require.Len(t, msg.ToolCalls, 1)
	require.Equal(t, "call_1", msg.ToolCalls[0].ID)
	require.Equal(t, []byte(wantArguments), msg.ToolCalls[0].Function.Arguments)
	_, err := msg.ReplayDataFor(callbackReplayProtocol, callbackReplayModel)
	require.NoError(t, err, "the native replay record must still match its framework projection")
}

func TestGraphBeforeToolCallbacksDoNotRewriteNativeReplayHistory(t *testing.T) {
	for _, scope := range []struct {
		name  string
		scope callbackReplayScope
	}{
		{name: "node callback", scope: callbackReplayNodeScope},
		{name: "plugin callback", scope: callbackReplayPluginScope},
	} {
		t.Run(scope.name, func(t *testing.T) {
			result := runCallbackReplayGraph(t, true, scope.scope)
			require.NoError(t, result.callbackErr)
			require.Equal(t, `{"a":1}`, result.callbackIn)
			require.EqualValues(t, 9, result.toolValue, "the callback-mutated arguments are used for execution")
			require.Equal(t, 2, result.modelCalls, "the graph must continue to the next LLM turn")

			call := findCallbackReplayCall(t, result.messages)
			assertCallbackReplayBinding(t, call, `{"a":1}`)
			secondCall := findCallbackReplayCall(t, result.requests[1])
			assertCallbackReplayBinding(t, secondCall, `{"a":1}`)
			var hasToolResult bool
			for _, msg := range result.requests[1] {
				if msg.Role == model.RoleTool && msg.ToolID == "call_1" {
					hasToolResult = true
					require.Equal(t, "10", msg.Content)
				}
			}
			require.True(t, hasToolResult, "the next LLM request must include the callback-derived tool result")
		})
	}
}

func TestGraphBeforeToolCallbacksKeepLegacyArgumentMutation(t *testing.T) {
	result := runCallbackReplayGraph(t, false, callbackReplayNodeScope)
	require.NoError(t, result.callbackErr)
	require.Equal(t, `{"a":1}`, result.callbackIn)
	require.EqualValues(t, 9, result.toolValue)
	require.Equal(t, 2, result.modelCalls)
	call := findCallbackReplayCall(t, result.messages)
	require.Nil(t, call.Replay)
	nextCall := findCallbackReplayCall(t, result.requests[1])
	require.Nil(t, nextCall.Replay)
}

func TestGraphBeforeModelCallbackCannotMutateCallerNativeHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	modelStub := &callbackReplayModelStub{native: true}
	callbackCalls := atomic.Int32{}
	modelCallbacks := model.NewCallbacks().RegisterBeforeModel(func(
		_ context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		callbackCalls.Add(1)
		for i := range args.Request.Messages {
			msg := &args.Request.Messages[i]
			if msg.Role == model.RoleAssistant && len(msg.ToolCalls) > 0 {
				msg.ToolCalls[0].Function.Arguments[5] = '9'
			}
		}
		return nil, nil
	})
	g, err := gr.NewStateGraph(gr.MessagesStateSchema()).
		AddLLMNode("llm", modelStub, "", nil, gr.WithModelCallbacks(modelCallbacks)).
		SetEntryPoint("llm").SetFinishPoint("llm").Compile()
	require.NoError(t, err)
	executor, err := gr.NewExecutor(g)
	require.NoError(t, err)

	nativeCall := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
		Type: "function", ID: "call_1",
		Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(`{"a":1}`)},
	}}}
	require.NoError(t, nativeCall.SetReplayData(callbackReplayProtocol, callbackReplayModel,
		json.RawMessage(`[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"calculator","arguments":"{\"a\":1}"}]`)))
	initial := gr.State{gr.StateKeyMessages: []model.Message{model.NewUserMessage("continue"), nativeCall}}
	inv := agent.NewInvocation(agent.WithInvocationID("before-model-replay"))
	events, err := executor.Execute(ctx, initial, inv)
	require.NoError(t, err)
	var guardError string
	for evt := range events {
		if evt != nil && evt.Error != nil && guardError == "" {
			guardError = evt.Error.Message
		}
	}
	require.NoError(t, ctx.Err())
	require.EqualValues(t, 1, callbackCalls.Load())
	require.Contains(t, guardError, "projection", "the edited native request must be rejected before model execution")
	calls, _ := modelStub.requestSnapshot()
	require.Zero(t, calls, "a request rejected by the replay guard must not reach the model")

	originalMessages, ok := initial[gr.StateKeyMessages].([]model.Message)
	require.True(t, ok)
	require.Equal(t, []byte(`{"a":1}`), originalMessages[1].ToolCalls[0].Function.Arguments,
		"a BeforeModel edit must not mutate the caller-owned graph input state")
	_, replayErr := originalMessages[1].ReplayDataFor(callbackReplayProtocol, callbackReplayModel)
	require.NoError(t, replayErr)
}
