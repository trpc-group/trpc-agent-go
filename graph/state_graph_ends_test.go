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
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// TestEndsValidation ensures per-node ends' targets are validated at compile time.
func TestEndsValidation(t *testing.T) {
	schema := NewStateSchema().
		AddField("ok", StateField{Type: reflect.TypeOf(false), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) { return nil, nil }, WithEndsMap(map[string]string{
		"goB":  "B",
		"stop": End,
	}))
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return State{"ok": true}, nil })
	sg.SetEntryPoint("A")

	// Should compile: ends refer to existing node B and End.
	_, err := sg.Compile()
	require.NoError(t, err)
}

// TestEndsValidation_InvalidTarget ensures compile fails if ends map refers to a non-existent node.
func TestEndsValidation_InvalidTarget(t *testing.T) {
	schema := NewStateSchema().
		AddField("ok", StateField{Type: reflect.TypeOf(false), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) { return nil, nil }, WithEndsMap(map[string]string{
		"bad": "NOPE", // NOPE is not declared in graph
	}))
	sg.SetEntryPoint("A")

	_, err := sg.Compile()
	require.Error(t, err)
}

// TestCommandGoToWithEnds ensures Command.GoTo resolves via per-node ends.
func TestCommandGoToWithEnds(t *testing.T) {
	schema := NewStateSchema().
		AddField("visited", StateField{Type: reflect.TypeOf(""), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("start", func(ctx context.Context, s State) (any, error) {
		return &Command{GoTo: "toB"}, nil // symbolic branch key
	}, WithEndsMap(map[string]string{"toB": "B"}))
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return State{"visited": "B"}, nil })
	sg.SetEntryPoint("start").SetFinishPoint("B")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-ends-goto"})
	require.NoError(t, err)

	final := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode || k == MetadataKeyPregel || k == MetadataKeyChannel || k == MetadataKeyState || k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}

	require.Equal(t, "B", final["visited"])
}

// TestConditionalEdgesWithEnds ensures conditional results are resolved via per-node ends when no PathMap is provided.
func TestConditionalEdgesWithEnds(t *testing.T) {
	schema := NewStateSchema().
		AddField("res", StateField{Type: reflect.TypeOf(""), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) {
		// Do nothing; routing decided by conditional
		return nil, nil
	}, WithEndsMap(map[string]string{"go": "B"}))
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return State{"res": "ok"}, nil })
	sg.SetEntryPoint("A")
	// Conditional returns symbolic key "go"; since no PathMap given, ends mapping should resolve it to B.
	sg.AddConditionalEdges("A", func(ctx context.Context, s State) (string, error) { return "go", nil }, nil)
	sg.SetFinishPoint("B")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-ends-cond"})
	require.NoError(t, err)

	final := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode || k == MetadataKeyPregel || k == MetadataKeyChannel || k == MetadataKeyState || k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}
	require.Equal(t, "ok", final["res"])
}

// TestMultiConditionalEdgesWithEnds ensures multi-conditional results are
// resolved via per-node ends when no PathMap is provided.
func TestMultiConditionalEdgesWithEnds(t *testing.T) {
	schema := NewStateSchema().
		AddField("b", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer}).
		AddField("c", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) {
		return nil, nil
	}, WithEndsMap(map[string]string{"goB": "B", "goC": "C"}))
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) {
		return State{"b": 1}, nil
	})
	sg.AddNode("C", func(ctx context.Context, s State) (any, error) {
		return State{"c": 1}, nil
	})
	sg.SetEntryPoint("A")
	// Multi-conditional returns two symbolic keys; ends mapping should
	// resolve them to B and C respectively.
	sg.AddMultiConditionalEdges("A", func(ctx context.Context, s State) ([]string, error) {
		return []string{"goB", "goC"}, nil
	}, nil)
	sg.SetFinishPoint("B").SetFinishPoint("C")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	ch, err := exec.Execute(
		context.Background(), State{},
		&agent.Invocation{InvocationID: "inv-multi-ends"},
	)
	require.NoError(t, err)

	got := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				switch k {
				case MetadataKeyNode, MetadataKeyPregel, MetadataKeyChannel,
					MetadataKeyState, MetadataKeyCompletion:
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					got[k] = v
				}
			}
		}
	}
	// Values may appear as float64 due to JSON decode.
	if got["b"] != float64(1) && got["b"] != 1 {
		t.Fatalf("missing or wrong b: %v", got["b"])
	}
	if got["c"] != float64(1) && got["c"] != 1 {
		t.Fatalf("missing or wrong c: %v", got["c"])
	}
}

type hookPlugin struct {
	name string
	reg  func(r *plugin.Registry)
}

func (p *hookPlugin) Name() string { return p.name }

func (p *hookPlugin) Register(r *plugin.Registry) {
	if p.reg != nil {
		p.reg(r)
	}
}

type toolCallbacksPluginManager struct {
	callbacks *tool.Callbacks
}

func (m *toolCallbacksPluginManager) AgentCallbacks() *agent.Callbacks { return nil }

func (m *toolCallbacksPluginManager) ModelCallbacks() *model.Callbacks { return nil }

func (m *toolCallbacksPluginManager) ToolCallbacks() *tool.Callbacks { return m.callbacks }

func (m *toolCallbacksPluginManager) OnEvent(
	_ context.Context,
	_ *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	return e, nil
}

func (m *toolCallbacksPluginManager) Close(_ context.Context) error { return nil }

type pluginCaptureModel struct {
	name   string
	called bool
}

func (m *pluginCaptureModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.called = true
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true}
	close(ch)
	return ch, nil
}

func (m *pluginCaptureModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type captureTool struct {
	name    string
	called  bool
	gotArgs []byte
	result  any
	err     error
}

func (t *captureTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func (t *captureTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	t.called = true
	t.gotArgs = append([]byte(nil), jsonArgs...)
	if t.err != nil {
		return nil, t.err
	}
	return t.result, nil
}

type declOnlyTool struct {
	name string
}

func (t *declOnlyTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

type testCtxKey struct{}

func TestRunModel_PluginBeforeModelShortCircuits(t *testing.T) {
	const (
		pluginName = "p"
		modelName  = "m"
		ctxVal     = "ctx"
	)
	localCalled := false
	local := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, req *model.Request) (*model.Response, error) {
			localCalled = true
			return nil, nil
		},
	)

	plugCalled := false
	p := &hookPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				plugCalled = true
				return &model.BeforeModelResult{
					Context: context.WithValue(ctx, testCtxKey{}, ctxVal),
					CustomResponse: &model.Response{
						Done: true,
					},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := &agent.Invocation{Plugins: pm}
	runCtx := agent.NewInvocationContext(context.Background(), inv)

	llm := &pluginCaptureModel{name: modelName}
	ctx, ch, err := runModel(runCtx, local, llm, &model.Request{})
	require.NoError(t, err)

	got, ok := ctx.Value(testCtxKey{}).(string)
	require.True(t, ok)
	require.Equal(t, ctxVal, got)

	var resp *model.Response
	for r := range ch {
		resp = r
	}
	require.NotNil(t, resp)
	require.True(t, resp.Done)

	require.True(t, plugCalled)
	require.False(t, localCalled)
	require.False(t, llm.called)
}

func TestRunModel_PluginBeforeModelError(t *testing.T) {
	const pluginName = "p"
	p := &hookPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				return nil, errors.New("boom")
			})
		},
	}
	pm := plugin.MustNewManager(p)
	inv := &agent.Invocation{Plugins: pm}
	runCtx := agent.NewInvocationContext(context.Background(), inv)

	llm := &pluginCaptureModel{name: "m"}
	_, ch, err := runModel(runCtx, nil, llm, &model.Request{})
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestProcessModelResponse_PluginAfterModelOverrides(t *testing.T) {
	const pluginName = "p"
	localCalled := false
	local := model.NewCallbacks().RegisterAfterModel(
		func(
			ctx context.Context,
			req *model.Request,
			rsp *model.Response,
			modelErr error,
		) (*model.Response, error) {
			localCalled = true
			return nil, nil
		},
	)

	custom := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.NewAssistantMessage(
				"plugin",
			),
		}},
	}
	p := &hookPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.AfterModel(func(
				ctx context.Context,
				args *model.AfterModelArgs,
			) (*model.AfterModelResult, error) {
				return &model.AfterModelResult{
					CustomResponse: custom,
				}, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)
	inv := &agent.Invocation{Plugins: pm}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	cfg := modelResponseConfig{
		Response:       &model.Response{Done: true},
		ModelCallbacks: local,
		EventChan:      nil,
		InvocationID:   "inv",
		SessionID:      "sess",
		LLMModel:       &pluginCaptureModel{name: "m"},
		Request:        &model.Request{},
		Span:           oteltrace.SpanFromContext(context.Background()),
	}
	_, evt, err := processModelResponse(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, evt)
	require.Same(t, custom, evt.Response)
	require.False(t, localCalled)
}

func TestProcessModelResponse_PluginAfterModelError(t *testing.T) {
	const pluginName = "p"
	p := &hookPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.AfterModel(func(
				ctx context.Context,
				args *model.AfterModelArgs,
			) (*model.AfterModelResult, error) {
				return nil, errors.New("boom")
			})
		},
	}
	pm := plugin.MustNewManager(p)
	inv := &agent.Invocation{Plugins: pm}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	cfg := modelResponseConfig{
		Response:     &model.Response{Done: true},
		InvocationID: "inv",
		SessionID:    "sess",
		LLMModel:     &pluginCaptureModel{name: "m"},
		Request:      &model.Request{},
		Span:         oteltrace.SpanFromContext(context.Background()),
	}
	_, evt, err := processModelResponse(ctx, cfg)
	require.Error(t, err)
	require.Nil(t, evt)
}

func TestRunTool_PluginBeforeToolShortCircuits(t *testing.T) {
	const (
		pluginName = "p"
		callID     = "call-1"
		toolName   = "t"
	)
	localCalled := false
	local := tool.NewCallbacks().RegisterBeforeTool(func(
		ctx context.Context,
		name string,
		decl *tool.Declaration,
		jsonArgs *[]byte,
	) (any, error) {
		localCalled = true
		return nil, nil
	})

	var gotCallID string
	p := &hookPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.BeforeTool(func(
				ctx context.Context,
				args *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				gotCallID, _ = tool.ToolCallIDFromContext(ctx)
				return &tool.BeforeToolResult{
					CustomResult: map[string]any{"ok": true},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := &agent.Invocation{Plugins: pm}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{name: toolName, result: "x"}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{}`),
		},
	}
	state := State{}

	_, got, _, err := runTool(ctx, tc, local, tl, state)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"ok": true}, got)
	require.False(t, localCalled)
	require.False(t, tl.called)
	require.Equal(t, callID, gotCallID)
}

func TestRunTool_PluginAfterToolOverridesError(t *testing.T) {
	const (
		pluginName = "p"
		callID     = "call-1"
		toolName   = "t"
	)
	localAfterCalled := false
	local := tool.NewCallbacks().RegisterAfterTool(func(
		ctx context.Context,
		name string,
		decl *tool.Declaration,
		jsonArgs []byte,
		result any,
		runErr error,
	) (any, error) {
		localAfterCalled = true
		return nil, nil
	})

	const fixed = "fixed"
	p := &hookPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.AfterTool(func(
				ctx context.Context,
				args *tool.AfterToolArgs,
			) (*tool.AfterToolResult, error) {
				return &tool.AfterToolResult{
					CustomResult: fixed,
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := &agent.Invocation{Plugins: pm}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{
		name: toolName,
		err:  errors.New("tool boom"),
	}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{}`),
		},
	}
	state := State{}

	_, got, _, err := runTool(ctx, tc, local, tl, state)
	require.NoError(t, err)
	require.Equal(t, fixed, got)
	require.True(t, tl.called)
	require.False(t, localAfterCalled)
}

func TestRunTool_PluginBeforeTool_CustomResultWithError(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)
	customResult := map[string]any{"need": "confirm"}
	modifiedArgs := []byte(`{"x":2}`)

	cbs := tool.NewCallbacks().RegisterBeforeTool(func(
		ctx context.Context,
		_ *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		next := context.WithValue(ctx, testCtxKey{}, "ctx")
		return &tool.BeforeToolResult{
			Context:           next,
			ModifiedArguments: modifiedArgs,
			CustomResult:      customResult,
		}, NewInterruptError("pause")
	})

	inv := &agent.Invocation{Plugins: &toolCallbacksPluginManager{callbacks: cbs}}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{name: toolName, result: "x"}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	gotCtx, got, gotArgs, err := runTool(ctx, tc, nil, tl, state)
	require.Error(t, err)
	var interruptErr *InterruptError
	require.ErrorAs(t, err, &interruptErr)
	require.Equal(t, customResult, got)
	require.Equal(t, modifiedArgs, gotArgs)
	require.Equal(t, "ctx", gotCtx.Value(testCtxKey{}))
	require.False(t, tl.called)
}

func TestRunTool_PluginBeforeTool_ErrorWithModifiedArguments(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)
	modifiedArgs := []byte(`{"x":2}`)

	cbs := tool.NewCallbacks().RegisterBeforeTool(func(
		ctx context.Context,
		_ *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		next := context.WithValue(ctx, testCtxKey{}, "ctx")
		return &tool.BeforeToolResult{
			Context:           next,
			ModifiedArguments: modifiedArgs,
		}, errors.New("boom")
	})

	inv := &agent.Invocation{Plugins: &toolCallbacksPluginManager{callbacks: cbs}}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{name: toolName, result: "x"}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	gotCtx, got, gotArgs, err := runTool(ctx, tc, nil, tl, state)
	require.Error(t, err)
	require.Nil(t, got)
	require.Equal(t, modifiedArgs, gotArgs)
	require.Equal(t, "ctx", gotCtx.Value(testCtxKey{}))
	require.False(t, tl.called)
}

func TestRunTool_BeforeTool_CustomResultWithError(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)
	customResult := map[string]any{"need": "confirm"}
	modifiedArgs := []byte(`{"x":2}`)

	local := tool.NewCallbacks().RegisterBeforeTool(func(
		ctx context.Context,
		_ *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		next := context.WithValue(ctx, testCtxKey{}, "ctx")
		return &tool.BeforeToolResult{
			Context:           next,
			ModifiedArguments: modifiedArgs,
			CustomResult:      customResult,
		}, NewInterruptError("pause")
	})

	tl := &captureTool{name: toolName, result: "x"}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	gotCtx, got, gotArgs, err := runTool(context.Background(), tc, local, tl, state)
	require.Error(t, err)
	var interruptErr *InterruptError
	require.ErrorAs(t, err, &interruptErr)
	require.Equal(t, customResult, got)
	require.Equal(t, modifiedArgs, gotArgs)
	require.Equal(t, "ctx", gotCtx.Value(testCtxKey{}))
	require.False(t, tl.called)
}

func TestRunTool_AfterTool_InterruptWithoutCustomResultPreservesResult(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)
	toolResult := map[string]any{"x": 1}

	local := tool.NewCallbacks().RegisterAfterTool(func(
		ctx context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		next := context.WithValue(ctx, testCtxKey{}, "ctx")
		return &tool.AfterToolResult{Context: next}, NewInterruptError("pause")
	})

	tl := &captureTool{name: toolName, result: toolResult}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	gotCtx, got, gotArgs, err := runTool(context.Background(), tc, local, tl, state)
	require.Error(t, err)
	var interruptErr *InterruptError
	require.ErrorAs(t, err, &interruptErr)
	require.Equal(t, toolResult, got)
	require.Equal(t, []byte(`{"x":1}`), gotArgs)
	require.Equal(t, "ctx", gotCtx.Value(testCtxKey{}))
	require.True(t, tl.called)
}

func TestRunTool_AfterTool_ErrorWithoutCustomResultReturnsNilResult(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)

	local := tool.NewCallbacks().RegisterAfterTool(func(
		ctx context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		next := context.WithValue(ctx, testCtxKey{}, "ctx")
		return &tool.AfterToolResult{Context: next}, errors.New("boom")
	})

	tl := &captureTool{name: toolName, result: map[string]any{"x": 1}}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	gotCtx, got, gotArgs, err := runTool(context.Background(), tc, local, tl, state)
	require.Error(t, err)
	require.Nil(t, got)
	require.Equal(t, []byte(`{"x":1}`), gotArgs)
	require.Equal(t, "ctx", gotCtx.Value(testCtxKey{}))
	require.True(t, tl.called)
}

func TestRunTool_PluginAfterTool_CustomResultWithError(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)
	customResult := map[string]any{"override": true}

	cbs := tool.NewCallbacks().RegisterAfterTool(func(
		ctx context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		next := context.WithValue(ctx, testCtxKey{}, "ctx")
		return &tool.AfterToolResult{
			Context:      next,
			CustomResult: customResult,
		}, errors.New("boom")
	})

	inv := &agent.Invocation{Plugins: &toolCallbacksPluginManager{callbacks: cbs}}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{name: toolName, result: map[string]any{"x": 1}}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	gotCtx, got, gotArgs, err := runTool(ctx, tc, nil, tl, state)
	require.Error(t, err)
	require.Equal(t, customResult, got)
	require.Equal(t, []byte(`{"x":1}`), gotArgs)
	require.Equal(t, "ctx", gotCtx.Value(testCtxKey{}))
	require.True(t, tl.called)
}

func TestRunTool_PluginAfterTool_InterruptWithoutCustomResultPreservesResult(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)
	toolResult := map[string]any{"x": 1}

	cbs := tool.NewCallbacks().RegisterAfterTool(func(
		ctx context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		next := context.WithValue(ctx, testCtxKey{}, "ctx")
		return &tool.AfterToolResult{Context: next}, NewInterruptError("pause")
	})

	inv := &agent.Invocation{Plugins: &toolCallbacksPluginManager{callbacks: cbs}}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{name: toolName, result: toolResult}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	gotCtx, got, gotArgs, err := runTool(ctx, tc, nil, tl, state)
	require.Error(t, err)
	var interruptErr *InterruptError
	require.ErrorAs(t, err, &interruptErr)
	require.Equal(t, toolResult, got)
	require.Equal(t, []byte(`{"x":1}`), gotArgs)
	require.Equal(t, "ctx", gotCtx.Value(testCtxKey{}))
	require.True(t, tl.called)
}

func TestRunTool_PluginToolCallbacksNilFallsBack(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)
	toolResult := map[string]any{"x": 1}

	inv := &agent.Invocation{Plugins: &toolCallbacksPluginManager{}}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{name: toolName, result: toolResult}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	gotCtx, got, gotArgs, err := runTool(ctx, tc, nil, tl, state)
	require.NoError(t, err)
	require.Equal(t, toolResult, got)
	require.Equal(t, []byte(`{"x":1}`), gotArgs)
	require.True(t, tl.called)
	gotCallID, ok := tool.ToolCallIDFromContext(gotCtx)
	require.True(t, ok)
	require.Equal(t, callID, gotCallID)
}

func TestRunTool_PluginAfterTool_ErrorWithoutResultReturnsNilResult(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)

	cbs := tool.NewCallbacks().RegisterAfterTool(func(
		_ context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return nil, errors.New("boom")
	})

	inv := &agent.Invocation{Plugins: &toolCallbacksPluginManager{callbacks: cbs}}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{name: toolName, result: map[string]any{"x": 1}}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	_, got, gotArgs, err := runTool(ctx, tc, nil, tl, state)
	require.Error(t, err)
	require.Nil(t, got)
	require.Equal(t, []byte(`{"x":1}`), gotArgs)
	require.True(t, tl.called)
}

func TestRunTool_PluginAfterTool_NoResultNoCustomResultReturnsNil(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)

	inv := &agent.Invocation{Plugins: &toolCallbacksPluginManager{callbacks: tool.NewCallbacks()}}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := &captureTool{name: toolName, result: nil}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	_, got, gotArgs, err := runTool(ctx, tc, nil, tl, state)
	require.NoError(t, err)
	require.Nil(t, got)
	require.Equal(t, []byte(`{"x":1}`), gotArgs)
	require.True(t, tl.called)
}

func TestRunTool_AfterTool_NoResultNoCustomResultReturnsNil(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)

	tl := &captureTool{name: toolName, result: nil}
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{"x":1}`),
		},
	}
	state := State{}

	_, got, gotArgs, err := runTool(context.Background(), tc, tool.NewCallbacks(), tl, state)
	require.NoError(t, err)
	require.Nil(t, got)
	require.Equal(t, []byte(`{"x":1}`), gotArgs)
	require.True(t, tl.called)
}

func TestRunTool_NotCallableReturnsError(t *testing.T) {
	const (
		callID   = "call-1"
		toolName = "t"
	)
	tc := model.ToolCall{
		ID: callID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(`{}`),
		},
	}
	state := State{}
	_, _, _, err := runTool(
		context.Background(),
		tc,
		nil,
		&declOnlyTool{name: toolName},
		state,
	)
	require.Error(t, err)
}
