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
	callbackProviderDataProtocol = "test.native"
	callbackProviderDataModel    = "callback-model"
)

type callbackProviderDataModelStub struct {
	mu       sync.Mutex
	calls    int
	requests [][]model.Message
	native   bool
}

func (m *callbackProviderDataModelStub) Info() model.Info {
	info := model.Info{Name: callbackProviderDataModel}
	if m.native {
		info.ProviderDataProtocol = callbackProviderDataProtocol
	}
	return info
}

func (m *callbackProviderDataModelStub) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.requests = append(m.requests, cloneCallbackProviderDataMessages(req.Messages))
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
			msg.ProviderData = &model.ProviderData{
				Protocol: callbackProviderDataProtocol,
				Model:    callbackProviderDataModel,
				Payload:  json.RawMessage(`[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"calculator","arguments":"{\"a\":1}"}]`),
			}
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}
	close(ch)
	return ch, nil
}

func (m *callbackProviderDataModelStub) requestSnapshot() (int, [][]model.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]model.Message, len(m.requests))
	for i := range m.requests {
		out[i] = cloneCallbackProviderDataMessages(m.requests[i])
	}
	return m.calls, out
}

func cloneCallbackProviderDataMessages(in []model.Message) []model.Message {
	out := make([]model.Message, len(in))
	copy(out, in)
	for i := range out {
		if out[i].ProviderData != nil {
			record := *out[i].ProviderData
			out[i].ProviderData = &record
		}
		out[i].ToolCalls = append([]model.ToolCall(nil), out[i].ToolCalls...)
		for j := range out[i].ToolCalls {
			out[i].ToolCalls[j].Function.Arguments = append([]byte(nil), out[i].ToolCalls[j].Function.Arguments...)
		}
	}
	return out
}

type callbackProviderDataRun struct {
	messages    []model.Message
	modelCalls  int
	requests    [][]model.Message
	toolValue   int32
	callbackIn  string
	callbackErr error
}

type callbackProviderDataScope int

const (
	callbackProviderDataNodeScope callbackProviderDataScope = iota
	callbackProviderDataPluginScope
)

func runCallbackProviderDataGraph(t *testing.T, native bool, scope callbackProviderDataScope) callbackProviderDataRun {
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
	modelStub := &callbackProviderDataModelStub{native: native}
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
	if scope == callbackProviderDataNodeScope {
		callbacks = tool.NewCallbacks()
		callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			callbackInput = string(args.Arguments)
			callbackErr = mutate(args)
			return &tool.BeforeToolResult{Context: ctx}, callbackErr
		})
	} else {
		manager = plugin.MustNewManager(&callbackProviderDataMutationPlugin{
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
		captured <- cloneCallbackProviderDataMessages(messages)
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
	result := callbackProviderDataRun{
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

type callbackProviderDataMutationPlugin struct {
	observe func(string, error)
}

func (p *callbackProviderDataMutationPlugin) Name() string { return "callback-replay-mutation" }

func (p *callbackProviderDataMutationPlugin) Register(r *plugin.Registry) {
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

func findCallbackProviderDataCall(t *testing.T, messages []model.Message) model.Message {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == model.RoleAssistant && len(msg.ToolCalls) > 0 {
			return msg
		}
	}
	t.Fatal("assistant tool call not found in graph messages")
	return model.Message{}
}

func assertCallbackProviderDataBinding(t *testing.T, msg model.Message, wantArguments string) {
	t.Helper()
	require.Len(t, msg.ToolCalls, 1)
	require.Equal(t, "call_1", msg.ToolCalls[0].ID)
	require.Equal(t, []byte(wantArguments), msg.ToolCalls[0].Function.Arguments)
	require.NotNil(t, msg.ProviderData)
	require.Equal(t, callbackProviderDataProtocol, msg.ProviderData.Protocol)
	require.Equal(t, callbackProviderDataModel, msg.ProviderData.Model)
}

func TestGraphBeforeToolCallbacksDoNotRewriteNativeProviderDataHistory(t *testing.T) {
	for _, scope := range []struct {
		name  string
		scope callbackProviderDataScope
	}{
		{name: "node callback", scope: callbackProviderDataNodeScope},
		{name: "plugin callback", scope: callbackProviderDataPluginScope},
	} {
		t.Run(scope.name, func(t *testing.T) {
			result := runCallbackProviderDataGraph(t, true, scope.scope)
			require.NoError(t, result.callbackErr)
			require.Equal(t, `{"a":1}`, result.callbackIn)
			require.EqualValues(t, 9, result.toolValue, "the callback-mutated arguments are used for execution")
			require.Equal(t, 2, result.modelCalls, "the graph must continue to the next LLM turn")

			call := findCallbackProviderDataCall(t, result.messages)
			assertCallbackProviderDataBinding(t, call, `{"a":1}`)
			secondCall := findCallbackProviderDataCall(t, result.requests[1])
			assertCallbackProviderDataBinding(t, secondCall, `{"a":1}`)
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
	result := runCallbackProviderDataGraph(t, false, callbackProviderDataNodeScope)
	require.NoError(t, result.callbackErr)
	require.Equal(t, `{"a":1}`, result.callbackIn)
	require.EqualValues(t, 9, result.toolValue)
	require.Equal(t, 2, result.modelCalls)
	call := findCallbackProviderDataCall(t, result.messages)
	require.Nil(t, call.ProviderData)
	nextCall := findCallbackProviderDataCall(t, result.requests[1])
	require.Nil(t, nextCall.ProviderData)
}

func TestGraphBeforeModelCallbackEditsIsolatedNativeProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	modelStub := &callbackProviderDataModelStub{native: true}
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
	nativeCall.ProviderData = &model.ProviderData{
		Protocol: callbackProviderDataProtocol,
		Model:    callbackProviderDataModel,
		Payload:  json.RawMessage(`[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"calculator","arguments":"{\"a\":1}"}]`),
	}
	initial := gr.State{gr.StateKeyMessages: []model.Message{model.NewUserMessage("continue"), nativeCall}}
	inv := agent.NewInvocation(agent.WithInvocationID("before-model-replay"))
	events, err := executor.Execute(ctx, initial, inv)
	require.NoError(t, err)
	var errorMessage string
	for evt := range events {
		if evt != nil && evt.Error != nil && errorMessage == "" {
			errorMessage = evt.Error.Message
		}
	}
	require.NoError(t, ctx.Err())
	require.EqualValues(t, 1, callbackCalls.Load())
	require.Empty(t, errorMessage)
	calls, requests := modelStub.requestSnapshot()
	require.Equal(t, 1, calls, "the edited projection is accepted by the model")
	require.Len(t, requests, 1)
	var sawEditedProviderData bool
	for _, message := range requests[0] {
		if message.Role != model.RoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		sawEditedProviderData = true
		require.Equal(t, []byte(`{"a":9}`), message.ToolCalls[0].Function.Arguments)
		require.NotNil(t, message.ProviderData)
		payload := message.ProviderData.Payload
		require.JSONEq(t,
			`[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"calculator","arguments":"{\"a\":1}"}]`,
			string(payload),
			"the native payload remains authoritative for a later model request",
		)
	}
	require.True(t, sawEditedProviderData, "the native history message must reach the model")

	originalMessages, ok := initial[gr.StateKeyMessages].([]model.Message)
	require.True(t, ok)
	require.Equal(t, []byte(`{"a":1}`), originalMessages[1].ToolCalls[0].Function.Arguments,
		"a BeforeModel edit must not mutate the caller-owned graph input state")
	require.NotNil(t, originalMessages[1].ProviderData)
}
