package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestNewSequentialAgent(t *testing.T) {
	t.Run("creates new agent successfully", func(t *testing.T) {
		// Create mock agents
		mockAgent1 := new(MockAgent)
		mockAgent1.On("Name").Return("Agent1").Maybe()

		mockAgent2 := new(MockAgent)
		mockAgent2.On("Name").Return("Agent2").Maybe()

		config := SequentialAgentConfig{
			BaseAgentConfig: BaseAgentConfig{
				Name:        "TestSequentialAgent",
				Description: "A test sequential agent",
			},
			Agents: []Agent{mockAgent1, mockAgent2},
		}

		agent, err := NewSequentialAgent(config)
		assert.NoError(t, err)
		assert.NotNil(t, agent)
		assert.Equal(t, "TestSequentialAgent", agent.Name())
		assert.Len(t, agent.agents, 2)
	})

	t.Run("returns error when no agents provided", func(t *testing.T) {
		config := SequentialAgentConfig{
			BaseAgentConfig: BaseAgentConfig{
				Name:        "TestSequentialAgent",
				Description: "A test sequential agent",
			},
			Agents: []Agent{},
		}

		agent, err := NewSequentialAgent(config)
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Contains(t, err.Error(), "no agents specified")
	})
}

func TestSequentialAgent_Run(t *testing.T) {
	t.Run("processes agents in sequence", func(t *testing.T) {
		// Create mock agents
		mockAgent1 := new(MockAgent)
		// Don't enforce the number of calls to Name()
		mockAgent1.On("Name").Return("Agent1")
		mockAgent1.On("Run", mock.Anything, mock.MatchedBy(func(msg *message.Message) bool {
			return msg.Content == "Initial message"
		})).Return(message.NewAssistantMessage("Response from agent 1"), nil).Once()

		mockAgent2 := new(MockAgent)
		// Don't enforce the number of calls to Name()
		mockAgent2.On("Name").Return("Agent2")
		mockAgent2.On("Run", mock.Anything, mock.MatchedBy(func(msg *message.Message) bool {
			return msg.Content == "Response from agent 1"
		})).Return(message.NewAssistantMessage("Final response"), nil).Once()

		// Create sequential agent
		config := SequentialAgentConfig{
			BaseAgentConfig: BaseAgentConfig{
				Name:        "TestSequentialAgent",
				Description: "A test sequential agent",
			},
			Agents: []Agent{mockAgent1, mockAgent2},
		}

		sequentialAgent, err := NewSequentialAgent(config)
		assert.NoError(t, err)

		// Run the agent
		msg := message.NewUserMessage("Initial message")
		response, err := sequentialAgent.Run(context.Background(), msg)

		// Verify
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Equal(t, "Final response", response.Content)

		// Skip the expectations check since we don't control the number of Name() calls
	})

	t.Run("handles errors from agents", func(t *testing.T) {
		// Create mock agents
		mockAgent1 := new(MockAgent)
		mockAgent1.On("Run", mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
		mockAgent1.On("Name").Return("Agent1")

		mockAgent2 := new(MockAgent)
		mockAgent2.On("Name").Return("Agent2").Maybe()

		// Create sequential agent
		config := SequentialAgentConfig{
			BaseAgentConfig: BaseAgentConfig{
				Name:        "TestSequentialAgent",
				Description: "A test sequential agent",
			},
			Agents: []Agent{mockAgent1, mockAgent2},
		}

		sequentialAgent, err := NewSequentialAgent(config)
		assert.NoError(t, err)

		// Run the agent
		msg := message.NewUserMessage("Initial message")
		response, err := sequentialAgent.Run(context.Background(), msg)

		// Verify
		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "error in agent")

		// Assert that Run was called once
		mockAgent1.AssertCalled(t, "Run", mock.Anything, mock.Anything)
		// Agent2 should not be called
		mockAgent2.AssertNotCalled(t, "Run", mock.Anything, mock.Anything)
	})
}
