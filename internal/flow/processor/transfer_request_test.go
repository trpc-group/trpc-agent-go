package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockTRPCAgent implements agent.Agent for transfer request tests.
type mockTRPCAgent struct{ name string }

func (m *mockTRPCAgent) Info() agent.Info                { return agent.Info{Name: m.name} }
func (m *mockTRPCAgent) SubAgents() []agent.Agent        { return nil }
func (m *mockTRPCAgent) FindSubAgent(string) agent.Agent { return nil }
func (m *mockTRPCAgent) Tools() []tool.Tool              { return nil }
func (m *mockTRPCAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.New(inv.InvocationID, m.name)
	}()
	return ch, nil
}

// mockParentTRPCAgent can resolve a single sub-agent by name.
type mockParentTRPCAgent struct{ child agent.Agent }

func (p *mockParentTRPCAgent) Info() agent.Info         { return agent.Info{Name: "parent"} }
func (p *mockParentTRPCAgent) SubAgents() []agent.Agent { return []agent.Agent{p.child} }
func (p *mockParentTRPCAgent) FindSubAgent(name string) agent.Agent {
	if p.child != nil && p.child.Info().Name == name {
		return p.child
	}
	return nil
}
func (p *mockParentTRPCAgent) Tools() []tool.Tool { return nil }
func (p *mockParentTRPCAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func TestTransferRequestProcessor_Success(t *testing.T) {
	// Arrange target and parent
	target := &mockTRPCAgent{name: "child"}
	parent := &mockParentTRPCAgent{child: target}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-pre-llm",
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child", Message: "hi"},
	}

	proc := NewTransferRequestProcessor()
	out := make(chan *event.Event, 10)

	// Act
	proc.ProcessRequest(context.Background(), inv, &model.Request{}, out)
	close(out)

	// Assert events: first a transfer event, then child's event
	evts := []*event.Event{}
	for e := range out {
		evts = append(evts, e)
	}
	require.GreaterOrEqual(t, len(evts), 2)
	require.Equal(t, model.ObjectTypeTransfer, evts[0].Object)
	require.Equal(t, "child", evts[1].Author)

	// Original invocation should be ended and transfer cleared
	require.True(t, inv.EndInvocation)
	require.Nil(t, inv.TransferInfo)
}

func TestTransferRequestProcessor_TargetNotFound(t *testing.T) {
	// Arrange no child
	parent := &mockParentTRPCAgent{child: nil}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-pre-llm-404",
		TransferInfo: &agent.TransferInfo{TargetAgentName: "missing"},
	}

	proc := NewTransferRequestProcessor()
	out := make(chan *event.Event, 1)

	// Act
	proc.ProcessRequest(context.Background(), inv, &model.Request{}, out)
	close(out)

	// Assert an error event emitted and invocation ended
	evt := <-out
	require.NotNil(t, evt.Error)
	require.Equal(t, model.ErrorTypeFlowError, evt.Error.Type)
	require.True(t, inv.EndInvocation)
	require.Nil(t, inv.TransferInfo)
}
