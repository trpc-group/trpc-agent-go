package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

func TestBaseAgent(t *testing.T) {
	config := BaseAgentConfig{
		Name:        "TestAgent",
		Description: "A test agent",
	}

	agent := NewBaseAgent(config)

	assert.Equal(t, "TestAgent", agent.Name())
	assert.Equal(t, "A test agent", agent.Description())
}

func TestBaseAgentRun(t *testing.T) {
	agent := NewBaseAgent(BaseAgentConfig{
		Name:        "TestAgent",
		Description: "A test agent",
	})

	msg := message.NewUserMessage("Hello, world!")
	response, err := agent.Run(context.Background(), msg)

	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, message.RoleAssistant, response.Role)
	assert.Equal(t, "BaseAgent implementation: Hello, world!", response.Content)
}

func TestBaseAgentRunAsync(t *testing.T) {
	agent := NewBaseAgent(BaseAgentConfig{
		Name:        "TestAgent",
		Description: "A test agent",
	})

	msg := message.NewUserMessage("Hello, world!")
	eventCh, err := agent.RunAsync(context.Background(), msg)

	assert.NoError(t, err)
	assert.NotNil(t, eventCh)

	// Wait for the response
	var responseReceived bool
	timeout := time.After(1 * time.Second)

	select {
	case event := <-eventCh:
		responseReceived = true
		assert.NotNil(t, event)
	case <-timeout:
		t.Fatal("Timeout waiting for response")
	}

	assert.True(t, responseReceived)
}
