package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

// MockAgent is a mock implementation of the agent.Agent interface for testing.
type MockAgent struct {
	mock.Mock
}

func (m *MockAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	args := m.Called(ctx, msg)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*message.Message), args.Error(1)
}

func (m *MockAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	args := m.Called(ctx, msg)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan *event.Event), args.Error(1)
}

func (m *MockAgent) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockAgent) Description() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockAgent) GetEvents() []*event.Event {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).([]*event.Event)
}

func TestNewParallelAgent(t *testing.T) {
	t.Run("creates new agent successfully", func(t *testing.T) {
		mockAgent1 := new(MockAgent)
		mockAgent1.On("Name").Return("Agent1")

		mockAgent2 := new(MockAgent)
		mockAgent2.On("Name").Return("Agent2")

		config := ParallelAgentConfig{
			Name:        "TestParallelAgent",
			Description: "A test parallel agent",
			Agents:      []agent.Agent{mockAgent1, mockAgent2},
		}

		agent, err := NewParallelAgent(config)
		assert.NoError(t, err)
		assert.NotNil(t, agent)
		assert.Equal(t, "TestParallelAgent", agent.Name())
		assert.Len(t, agent.agents, 2)
	})

	t.Run("returns error when no agents provided", func(t *testing.T) {
		config := ParallelAgentConfig{
			Name:        "TestParallelAgent",
			Description: "A test parallel agent",
			Agents:      []agent.Agent{},
		}

		agent, err := NewParallelAgent(config)
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Contains(t, err.Error(), "at least one agent is required")
	})
}

func TestParallelAgent_Run(t *testing.T) {
	t.Run("runs agents in parallel and combines responses", func(t *testing.T) {
		// Create mock agents
		mockAgent1 := new(MockAgent)
		mockAgent1.On("Run", mock.Anything, mock.Anything).Return(
			message.NewAssistantMessage("Response from agent 1"), nil)
		mockAgent1.On("Name").Return("Agent1")

		mockAgent2 := new(MockAgent)
		mockAgent2.On("Run", mock.Anything, mock.Anything).Return(
			message.NewAssistantMessage("Response from agent 2"), nil)
		mockAgent2.On("Name").Return("Agent2")

		// Create parallel agent
		config := ParallelAgentConfig{
			Name:        "TestParallelAgent",
			Description: "A test parallel agent",
			Agents:      []agent.Agent{mockAgent1, mockAgent2},
		}

		parallelAgent, err := NewParallelAgent(config)
		assert.NoError(t, err)

		// Run the parallel agent
		msg := message.NewUserMessage("Test message")
		response, err := parallelAgent.Run(context.Background(), msg)

		// Verify
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Contains(t, response.Content, "Agent [Agent1]: Response from agent 1")
		assert.Contains(t, response.Content, "Agent [Agent2]: Response from agent 2")

		mockAgent1.AssertExpectations(t)
		mockAgent2.AssertExpectations(t)
	})

	t.Run("handles errors from some agents", func(t *testing.T) {
		// Create mock agents
		mockAgent1 := new(MockAgent)
		mockAgent1.On("Run", mock.Anything, mock.Anything).Return(
			message.NewAssistantMessage("Response from agent 1"), nil)
		mockAgent1.On("Name").Return("Agent1")

		mockAgent2 := new(MockAgent)
		mockAgent2.On("Run", mock.Anything, mock.Anything).Return(
			nil, errors.New("agent 2 failed"))
		mockAgent2.On("Name").Return("Agent2")

		// Create parallel agent
		config := ParallelAgentConfig{
			Name:        "TestParallelAgent",
			Description: "A test parallel agent",
			Agents:      []agent.Agent{mockAgent1, mockAgent2},
		}

		parallelAgent, err := NewParallelAgent(config)
		assert.NoError(t, err)

		// Run the parallel agent
		msg := message.NewUserMessage("Test message")
		response, err := parallelAgent.Run(context.Background(), msg)

		// Verify
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Contains(t, response.Content, "Agent [Agent1]: Response from agent 1")

		mockAgent1.AssertExpectations(t)
		mockAgent2.AssertExpectations(t)
	})
}
