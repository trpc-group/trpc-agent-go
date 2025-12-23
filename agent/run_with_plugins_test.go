package agent_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testAgent struct {
	mu     sync.Mutex
	called bool
}

func (a *testAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.called = true
	a.mu.Unlock()

	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		rsp := &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("orig"),
			}},
		}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp),
		)
	}()
	return ch, nil
}

func (a *testAgent) Tools() []tool.Tool { return nil }

func (a *testAgent) Info() agent.Info {
	return agent.Info{Name: "a", Description: "test"}
}

func (a *testAgent) SubAgents() []agent.Agent        { return nil }
func (a *testAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *testAgent) wasCalled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.called
}

type cbPlugin struct {
	name string
	reg  func(r *plugin.Registry)
}

func (p *cbPlugin) Name() string { return p.name }

func (p *cbPlugin) Register(r *plugin.Registry) {
	if p.reg != nil {
		p.reg(r)
	}
}

func TestRunWithPlugins_BeforeAgentCanShortCircuit(t *testing.T) {
	ag := &testAgent{}
	p := &cbPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeAgent(func(
				ctx context.Context,
				args *agent.BeforeAgentArgs,
			) (*agent.BeforeAgentResult, error) {
				return &agent.BeforeAgentResult{
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{{
							Index: 0,
							Message: model.NewAssistantMessage(
								"early",
							),
						}},
					},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}

	require.False(t, ag.wasCalled())
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Response)
	require.Equal(
		t,
		"early",
		events[0].Response.Choices[0].Message.Content,
	)
}

func TestRunWithPlugins_AfterAgentCanAppendEvent(t *testing.T) {
	ag := &testAgent{}
	p := &cbPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				return &agent.AfterAgentResult{
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{{
							Index: 0,
							Message: model.NewAssistantMessage(
								"after",
							),
						}},
					},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}

	require.True(t, ag.wasCalled())
	require.Len(t, events, 2)
	require.Equal(
		t,
		"orig",
		events[0].Response.Choices[0].Message.Content,
	)
	require.Equal(
		t,
		"after",
		events[1].Response.Choices[0].Message.Content,
	)
}
