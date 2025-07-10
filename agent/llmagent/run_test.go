package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockFlow implements flow.Flow returning predefined events.
type mockFlow struct{ done bool }

func (m *mockFlow) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		if !m.done {
			ch <- event.New(inv.InvocationID, inv.AgentName)
		}
	}()
	return ch, nil
}

func TestLLMAgent_Run_BeforeCallbackCustom(t *testing.T) {
	cb := agent.NewAgentCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{Object: "before", Done: true}, nil
	})

	a := New("agent", WithAgentCallbacks(cb))
	// Replace flow to avoid heavy deps.
	a.flow = &mockFlow{done: true}

	evts, err := a.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "agent"})
	require.NoError(t, err)
	first := <-evts
	require.Equal(t, "before", first.Object)
}

func TestLLMAgent_Run_BeforeCallbackError(t *testing.T) {
	cb := agent.NewAgentCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, context.Canceled
	})

	a := New("agent", WithAgentCallbacks(cb))
	a.flow = &mockFlow{done: true}

	_, err := a.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "agent"})
	require.Error(t, err)
}

func TestLLMAgent_Run_FlowAndAfterCallback(t *testing.T) {
	after := agent.NewAgentCallbacks()
	after.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return &model.Response{Object: "after", Done: true}, nil
	})

	a := New("agent", WithAgentCallbacks(after))
	a.flow = &mockFlow{}

	evts, err := a.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "agent"})
	require.NoError(t, err)

	objs := []string{}
	for e := range evts {
		objs = append(objs, e.Object)
	}
	require.Equal(t, []string{"", "after"}, objs) // First event has empty Object set by mockFlow
}
