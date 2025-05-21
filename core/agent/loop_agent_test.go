package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestNewLoopAgent(t *testing.T) {
	t.Run("creates new agent successfully", func(t *testing.T) {
		// Create mock inner agent
		mockAgent := new(MockAgent)
		mockAgent.On("Name").Return("MockAgent")

		config := LoopAgentConfig{
			BaseAgentConfig: BaseAgentConfig{
				Name:        "TestLoopAgent",
				Description: "A test loop agent",
			},
			InnerAgent:    mockAgent,
			MaxIterations: 5,
		}

		agent, err := NewLoopAgent(config)
		assert.NoError(t, err)
		assert.NotNil(t, agent)
		assert.Equal(t, "TestLoopAgent", agent.Name())
		assert.Equal(t, 5, agent.maxIterations)
		assert.NotNil(t, agent.terminationCondition)
	})

	t.Run("returns error when no inner agent provided", func(t *testing.T) {
		config := LoopAgentConfig{
			BaseAgentConfig: BaseAgentConfig{
				Name:        "TestLoopAgent",
				Description: "A test loop agent",
			},
			// No inner agent
		}

		agent, err := NewLoopAgent(config)
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Equal(t, ErrInnerAgentRequired, err)
	})
}

func TestLoopAgent_Run(t *testing.T) {
	t.Run("stops at max iterations", func(t *testing.T) {
		// Create mock inner agent that always returns the same message
		mockAgent := new(MockAgent)
		mockAgent.On("Run", mock.Anything, mock.Anything).Return(
			message.NewAssistantMessage("Always the same response"), nil)
		mockAgent.On("Name").Return("MockAgent")

		// Create a loop agent with a termination condition that never triggers
		config := LoopAgentConfig{
			BaseAgentConfig: BaseAgentConfig{
				Name:        "TestLoopAgent",
				Description: "A test loop agent",
			},
			InnerAgent:    mockAgent,
			MaxIterations: 3, // Should stop after 3 iterations
		}

		loopAgent, err := NewLoopAgent(config)
		assert.NoError(t, err)

		// Run the agent
		msg := message.NewUserMessage("Initial message")
		response, err := loopAgent.Run(context.Background(), msg)

		// Verify - should have max iterations error
		assert.Error(t, err)
		assert.Equal(t, ErrMaxIterationsExceeded, err)
		assert.NotNil(t, response)

		// Should have called Run 3 times (max iterations)
		mockAgent.AssertNumberOfCalls(t, "Run", 3)
	})
}
