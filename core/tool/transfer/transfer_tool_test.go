package transfer

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// mockAgentProvider implements agent.SubAgentProvider for testing.
type mockAgentProvider struct {
	subAgents []agent.Agent
}

func (m *mockAgentProvider) SubAgents() []agent.Agent {
	return m.subAgents
}

func (m *mockAgentProvider) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range m.subAgents {
		if subAgent.Name() == name {
			return subAgent
		}
	}
	return nil
}

// mockSubAgent implements agent.Agent for testing.
type mockSubAgent struct {
	name string
}

func (m *mockSubAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (m *mockSubAgent) Tools() []tool.Tool {
	return nil
}

func (m *mockSubAgent) Name() string {
	return m.name
}

func TestTransferTool_Declaration(t *testing.T) {
	calc := &mockSubAgent{name: "calculator"}
	provider := &mockAgentProvider{subAgents: []agent.Agent{calc}}

	tool := New(provider)
	declaration := tool.Declaration()

	if declaration.Name != "transfer_to_agent" {
		t.Errorf("Expected name 'transfer_to_agent', got '%s'", declaration.Name)
	}
}

func TestTransferTool_Call_Success(t *testing.T) {
	calc := &mockSubAgent{name: "calculator"}
	provider := &mockAgentProvider{subAgents: []agent.Agent{calc}}

	tool := New(provider)

	request := Request{AgentName: "calculator"}
	requestBytes, _ := json.Marshal(request)

	ctx := agent.NewContextWithInvocation(context.Background(), &agent.Invocation{
		Agent:        calc,
		AgentName:    "calculator",
		InvocationID: "test-invocation-id",
	})
	result, err := tool.Call(ctx, requestBytes)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	response, ok := result.(Response)
	if !ok {
		t.Error("Expected Response type")
	}

	if !response.Success {
		t.Error("Expected successful transfer")
	}
}
