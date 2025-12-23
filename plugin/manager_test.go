package plugin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
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
