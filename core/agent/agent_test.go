package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
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

func TestBaseAgentProcess(t *testing.T) {
	agent := NewBaseAgent(BaseAgentConfig{
		Name:        "TestAgent",
		Description: "A test agent",
	})

	content := event.NewTextContent("Hello, world!")
	response, err := agent.Process(context.Background(), content)

	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.True(t, response.HasText())
	assert.Equal(t, "BaseAgent processed: Hello, world!", response.GetText())
}

func TestBaseAgentProcessAsync(t *testing.T) {
	agent := NewBaseAgent(BaseAgentConfig{
		Name:        "TestAgent",
		Description: "A test agent",
	})

	content := event.NewTextContent("Hello, world!")
	eventCh, err := agent.ProcessAsync(context.Background(), content)

	assert.NoError(t, err)
	assert.NotNil(t, eventCh)

	// Wait for the response
	var responseReceived bool
	timeout := time.After(1 * time.Second)

	select {
	case evt := <-eventCh:
		responseReceived = true
		assert.NotNil(t, evt)
		assert.Equal(t, "TestAgent", evt.Author)
		assert.True(t, evt.HasText())
	case <-timeout:
		t.Fatal("Timeout waiting for response")
	}

	assert.True(t, responseReceived)
}
