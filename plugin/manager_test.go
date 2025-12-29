//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package plugin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testPlugin struct {
	name string
	reg  func(r *plugin.Registry)
}

func (p *testPlugin) Name() string { return p.name }

func (p *testPlugin) Register(r *plugin.Registry) {
	if p.reg != nil {
		p.reg(r)
	}
}

type closerPlugin struct {
	name       string
	closedWith context.Context
	closeOrder *[]string
	closeErr   error
}

func (p *closerPlugin) Name() string { return p.name }

func (p *closerPlugin) Register(r *plugin.Registry) {}

func (p *closerPlugin) Close(ctx context.Context) error {
	p.closedWith = ctx
	if p.closeOrder != nil {
		*p.closeOrder = append(*p.closeOrder, p.name)
	}
	return p.closeErr
}

func TestNewManager_DuplicateName(t *testing.T) {
	p1 := &testPlugin{name: "p"}
	p2 := &testPlugin{name: "p"}
	_, err := plugin.NewManager(p1, p2)
	require.Error(t, err)
}

func TestNewManager_NilPlugin(t *testing.T) {
	_, err := plugin.NewManager(nil)
	require.Error(t, err)
}

func TestNewManager_EmptyName(t *testing.T) {
	p := &testPlugin{name: ""}
	_, err := plugin.NewManager(p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "name")
}

func TestMustNewManager_PanicsOnError(t *testing.T) {
	p := &testPlugin{name: ""}
	require.Panics(t, func() {
		_ = plugin.MustNewManager(p)
	})
}

func TestManager_CallbackSetsNilWhenEmpty(t *testing.T) {
	m, err := plugin.NewManager()
	require.NoError(t, err)
	require.Nil(t, m.AgentCallbacks())
	require.Nil(t, m.ModelCallbacks())
	require.Nil(t, m.ToolCallbacks())

	e := &event.Event{}
	out, err := m.OnEvent(context.Background(), &agent.Invocation{}, e)
	require.NoError(t, err)
	require.Same(t, e, out)

	require.NoError(t, m.Close(context.Background()))
	require.NoError(t, m.Close(nil))
}

func TestManager_NilReceiver_IsSafe(t *testing.T) {
	var m *plugin.Manager
	require.Nil(t, m.AgentCallbacks())
	require.Nil(t, m.ModelCallbacks())
	require.Nil(t, m.ToolCallbacks())

	out, err := m.OnEvent(
		context.Background(),
		&agent.Invocation{},
		nil,
	)
	require.NoError(t, err)
	require.Nil(t, out)

	require.NoError(t, m.Close(nil))
}

func TestManager_Close_ReverseOrderAndJoinErrors(t *testing.T) {
	const (
		errCloseP2 = "close err p2"
		errCloseP3 = "close err p3"
	)
	var closeOrder []string
	p1 := &closerPlugin{name: "p1", closeOrder: &closeOrder}
	p2Err := errors.New(errCloseP2)
	p2 := &closerPlugin{
		name:       "p2",
		closeOrder: &closeOrder,
		closeErr:   p2Err,
	}
	p3Err := errors.New(errCloseP3)
	p3 := &closerPlugin{
		name:       "p3",
		closeOrder: &closeOrder,
		closeErr:   p3Err,
	}

	m := plugin.MustNewManager(p1, p2, p3)
	err := m.Close(nil)
	require.Error(t, err)
	require.ErrorIs(t, err, p2Err)
	require.ErrorIs(t, err, p3Err)
	require.Contains(t, err.Error(), "plugin")
	require.Contains(t, err.Error(), "p2")
	require.Contains(t, err.Error(), "p3")

	require.Equal(t, []string{"p3", "p2", "p1"}, closeOrder)
	require.NotNil(t, p3.closedWith)
}

func TestManager_Close_SkipsNonCloser(t *testing.T) {
	var closeOrder []string
	p1 := &closerPlugin{name: "p1", closeOrder: &closeOrder}
	p2 := &testPlugin{name: "p2"}
	p3 := &closerPlugin{name: "p3", closeOrder: &closeOrder}

	m := plugin.MustNewManager(p1, p2, p3)
	err := m.Close(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"p3", "p1"}, closeOrder)
}

func TestManager_ModelCallbacks_Order(t *testing.T) {
	var calls []string
	p1 := &testPlugin{
		name: "p1",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				calls = append(calls, "p1")
				return nil, nil
			})
		},
	}
	p2 := &testPlugin{
		name: "p2",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				calls = append(calls, "p2")
				return nil, nil
			})
		},
	}

	m := plugin.MustNewManager(p1, p2)
	callbacks := m.ModelCallbacks()
	require.NotNil(t, callbacks)

	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"p1", "p2"}, calls)
}

func TestManager_ModelCallbacks_EarlyExit(t *testing.T) {
	var calls []string
	p1 := &testPlugin{
		name: "p1",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				calls = append(calls, "p1")
				return &model.BeforeModelResult{
					CustomResponse: &model.Response{Done: true},
				}, nil
			})
		},
	}
	p2 := &testPlugin{
		name: "p2",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				calls = append(calls, "p2")
				return nil, nil
			})
		},
	}

	m := plugin.MustNewManager(p1, p2)
	callbacks := m.ModelCallbacks()
	require.NotNil(t, callbacks)

	result, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.CustomResponse)
	require.Equal(t, []string{"p1"}, calls)
}

func TestManager_OnEvent_Order(t *testing.T) {
	var calls []string
	p1 := &testPlugin{
		name: "p1",
		reg: func(r *plugin.Registry) {
			r.OnEvent(func(
				ctx context.Context,
				inv *agent.Invocation,
				e *event.Event,
			) (*event.Event, error) {
				calls = append(calls, "p1")
				return nil, nil
			})
		},
	}
	p2 := &testPlugin{
		name: "p2",
		reg: func(r *plugin.Registry) {
			r.OnEvent(func(
				ctx context.Context,
				inv *agent.Invocation,
				e *event.Event,
			) (*event.Event, error) {
				calls = append(calls, "p2")
				return nil, nil
			})
		},
	}

	m := plugin.MustNewManager(p1, p2)
	inv := &agent.Invocation{}
	e := event.New("inv", "author")
	_, err := m.OnEvent(context.Background(), inv, e)
	require.NoError(t, err)
	require.Equal(t, []string{"p1", "p2"}, calls)
}

func TestManager_OnEvent_ErrorWrapsName(t *testing.T) {
	wantErr := errors.New("boom")
	p := &testPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.OnEvent(func(
				ctx context.Context,
				inv *agent.Invocation,
				e *event.Event,
			) (*event.Event, error) {
				return nil, wantErr
			})
		},
	}

	m := plugin.MustNewManager(p)
	_, err := m.OnEvent(
		context.Background(),
		&agent.Invocation{},
		&event.Event{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "plugin")
	require.Contains(t, err.Error(), "p")
}

func TestManager_OnEvent_ReplacementPropagates(t *testing.T) {
	const (
		tagOriginal = "orig"
		tagUpdated  = "updated"
	)
	var seen []string
	p1 := &testPlugin{
		name: "p1",
		reg: func(r *plugin.Registry) {
			r.OnEvent(func(
				ctx context.Context,
				inv *agent.Invocation,
				e *event.Event,
			) (*event.Event, error) {
				updated := &event.Event{Tag: tagUpdated}
				return updated, nil
			})
		},
	}
	p2 := &testPlugin{
		name: "p2",
		reg: func(r *plugin.Registry) {
			r.OnEvent(func(
				ctx context.Context,
				inv *agent.Invocation,
				e *event.Event,
			) (*event.Event, error) {
				if e != nil {
					seen = append(seen, e.Tag)
				}
				return nil, nil
			})
		},
	}

	m := plugin.MustNewManager(p1, p2)
	_, err := m.OnEvent(
		context.Background(),
		&agent.Invocation{},
		&event.Event{Tag: tagOriginal},
	)
	require.NoError(t, err)
	require.Equal(t, []string{tagUpdated}, seen)
}

func TestManager_AgentCallbacks_WrapErrorWithName(t *testing.T) {
	wantErr := errors.New("boom")
	p := &testPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeAgent(func(
				ctx context.Context,
				args *agent.BeforeAgentArgs,
			) (*agent.BeforeAgentResult, error) {
				return nil, wantErr
			})
		},
	}

	m := plugin.MustNewManager(p)
	cb := m.AgentCallbacks()
	require.NotNil(t, cb)

	_, err := cb.RunBeforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: &agent.Invocation{}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "p")
}

func TestManager_ToolCallbacks_WrapErrorWithName(t *testing.T) {
	wantErr := errors.New("boom")
	p := &testPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeTool(func(
				ctx context.Context,
				args *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				return nil, wantErr
			})
		},
	}

	m := plugin.MustNewManager(p)
	cb := m.ToolCallbacks()
	require.NotNil(t, cb)

	_, err := cb.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{ToolName: "t"},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "p")
}

func TestManager_ModelCallbacks_WrapAfterErrorWithName(t *testing.T) {
	wantErr := errors.New("boom")
	p := &testPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterModel(func(
				ctx context.Context,
				args *model.AfterModelArgs,
			) (*model.AfterModelResult, error) {
				return nil, wantErr
			})
		},
	}

	m := plugin.MustNewManager(p)
	cb := m.ModelCallbacks()
	require.NotNil(t, cb)

	_, err := cb.RunAfterModel(
		context.Background(),
		&model.AfterModelArgs{
			Request:  &model.Request{},
			Response: &model.Response{Done: true},
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "p")
}

func TestManager_ToolCallbacks_WrapAfterErrorWithName(t *testing.T) {
	wantErr := errors.New("boom")
	p := &testPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterTool(func(
				ctx context.Context,
				args *tool.AfterToolArgs,
			) (*tool.AfterToolResult, error) {
				return nil, wantErr
			})
		},
	}

	m := plugin.MustNewManager(p)
	cb := m.ToolCallbacks()
	require.NotNil(t, cb)

	_, err := cb.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolName:    "t",
			Declaration: nil,
			Arguments:   []byte("{}"),
			Result:      "x",
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "p")
}

func TestGlobalInstruction_PrependsSystemMessage(t *testing.T) {
	m := plugin.MustNewManager(plugin.NewGlobalInstruction("policy"))
	callbacks := m.ModelCallbacks()
	require.NotNil(t, callbacks)

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	}

	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.NotEmpty(t, req.Messages)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	require.Contains(t, req.Messages[0].Content, "policy")
}

func TestGlobalInstruction_NoMessages_AddsSystem(t *testing.T) {
	const instr = "policy"
	m := plugin.MustNewManager(plugin.NewGlobalInstruction(instr))
	callbacks := m.ModelCallbacks()

	req := &model.Request{}
	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)

	require.Len(t, req.Messages, 1)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	require.Equal(t, instr, req.Messages[0].Content)
}

func TestGlobalInstruction_EmptyInstruction_NoChange(t *testing.T) {
	m := plugin.MustNewManager(plugin.NewGlobalInstruction("  \n\t "))
	callbacks := m.ModelCallbacks()
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	}

	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.Len(t, req.Messages, 1)
	require.Equal(t, model.RoleUser, req.Messages[0].Role)
}

func TestGlobalInstruction_SystemEmptyContent_Sets(t *testing.T) {
	const instr = "policy"
	m := plugin.MustNewManager(plugin.NewGlobalInstruction(instr))
	callbacks := m.ModelCallbacks()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(""),
			model.NewUserMessage("hi"),
		},
	}

	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.Len(t, req.Messages, 2)
	require.Equal(t, instr, req.Messages[0].Content)
}

func TestGlobalInstruction_SystemWithContent_Prepends(t *testing.T) {
	const (
		instr = "policy"
		old   = "old"
	)
	m := plugin.MustNewManager(plugin.NewGlobalInstruction(instr))
	callbacks := m.ModelCallbacks()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(old),
			model.NewUserMessage("hi"),
		},
	}

	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.True(
		t,
		strings.HasPrefix(req.Messages[0].Content, instr),
	)
	require.Contains(t, req.Messages[0].Content, old)
}

func TestGlobalInstruction_FirstNonSystem_Prepends(t *testing.T) {
	const instr = "policy"
	m := plugin.MustNewManager(plugin.NewGlobalInstruction(instr))
	callbacks := m.ModelCallbacks()
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("hi"),
		},
	}

	_, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.Len(t, req.Messages, 2)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	require.Equal(t, instr, req.Messages[0].Content)
	require.Equal(t, model.RoleUser, req.Messages[1].Role)
}

func TestLoggingPlugin_Callbacks_DoNotError(t *testing.T) {
	m := plugin.MustNewManager(plugin.NewLogging())

	inv := &agent.Invocation{
		AgentName: "a",
		Model:     &staticModel{name: "m"},
	}

	agentCB := m.AgentCallbacks()
	require.NotNil(t, agentCB)
	before, err := agentCB.RunBeforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: inv},
	)
	require.NoError(t, err)
	ctx := context.Background()
	if before != nil && before.Context != nil {
		ctx = before.Context
	}
	_, err = agentCB.RunAfterAgent(ctx, &agent.AfterAgentArgs{
		Invocation:        inv,
		FullResponseEvent: &event.Event{},
		Error:             nil,
	})
	require.NoError(t, err)

	modelCB := m.ModelCallbacks()
	require.NotNil(t, modelCB)
	invCtx := agent.NewInvocationContext(context.Background(), inv)
	beforeModel, err := modelCB.RunBeforeModel(
		invCtx,
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	var modelCtx context.Context = invCtx
	if beforeModel != nil && beforeModel.Context != nil {
		modelCtx = beforeModel.Context
	}
	_, err = modelCB.RunAfterModel(modelCtx, &model.AfterModelArgs{
		Request:  &model.Request{},
		Response: &model.Response{Done: true},
		Error:    nil,
	})
	require.NoError(t, err)

	toolCB := m.ToolCallbacks()
	require.NotNil(t, toolCB)
	toolCtx := context.WithValue(
		context.Background(),
		tool.ContextKeyToolCallID{},
		"call",
	)
	beforeTool, err := toolCB.RunBeforeTool(
		toolCtx,
		&tool.BeforeToolArgs{
			ToolName:    "t",
			Declaration: nil,
			Arguments:   []byte("{}"),
		},
	)
	require.NoError(t, err)
	if beforeTool != nil && beforeTool.Context != nil {
		toolCtx = beforeTool.Context
	}
	_, err = toolCB.RunAfterTool(toolCtx, &tool.AfterToolArgs{
		ToolName:    "t",
		Declaration: nil,
		Arguments:   []byte("{}"),
		Result:      "ok",
		Error:       nil,
	})
	require.NoError(t, err)
}

func TestLoggingPlugin_NoInvocationContextAndErrorArgs(t *testing.T) {
	m := plugin.MustNewManager(plugin.NewLogging())
	cb := m.ModelCallbacks()
	require.NotNil(t, cb)

	before, err := cb.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	require.NotNil(t, before)

	afterCtx := context.Background()
	if before.Context != nil {
		afterCtx = before.Context
	}
	_, err = cb.RunAfterModel(afterCtx, &model.AfterModelArgs{
		Request:  &model.Request{},
		Response: &model.Response{Done: true},
		Error:    errors.New("boom"),
	})
	require.NoError(t, err)

	toolCB := m.ToolCallbacks()
	require.NotNil(t, toolCB)
	_, err = toolCB.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{
			ToolName:    "t",
			Declaration: nil,
			Arguments:   []byte("{}"),
		},
	)
	require.NoError(t, err)
}

func TestGlobalInstruction_NilRequest_IsSafe(t *testing.T) {
	m := plugin.MustNewManager(plugin.NewGlobalInstruction("policy"))
	cb := m.ModelCallbacks()
	require.NotNil(t, cb)

	_, err := cb.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: nil},
	)
	require.NoError(t, err)
}

type staticModel struct {
	name string
}

func (m *staticModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true}
	close(ch)
	return ch, nil
}

func (m *staticModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func TestRegistry_NilReceiver_IsSafe(t *testing.T) {
	var r *plugin.Registry
	r.BeforeAgent(nil)
	r.AfterAgent(nil)
	r.BeforeModel(nil)
	r.AfterModel(nil)
	r.BeforeTool(nil)
	r.AfterTool(nil)
	r.OnEvent(nil)
}

func TestNewNamedLogging_EmptyName_UsesDefault(t *testing.T) {
	const defaultName = "logging"
	got := plugin.NewNamedLogging("")
	require.Equal(t, defaultName, got.Name())
}

func TestNewNamedGlobalInstruction_EmptyName_UsesDefault(t *testing.T) {
	const defaultName = "global_instruction"
	got := plugin.NewNamedGlobalInstruction("", "x")
	require.Equal(t, defaultName, got.Name())
}
