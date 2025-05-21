package evaluation

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// mockAgent implements the Agent interface for testing.
type mockAgent struct {
	name          string
	description   string
	returnMessage *message.Message
	returnError   error
}

func (m *mockAgent) Name() string {
	return m.name
}

func (m *mockAgent) Description() string {
	return m.description
}

func (m *mockAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	if m.returnError != nil {
		return nil, m.returnError
	}
	if m.returnMessage != nil {
		return m.returnMessage, nil
	}
	// Default response echoes the input
	return message.NewAssistantMessage("Mock response to: " + msg.Content), nil
}

func (m *mockAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	// Not used in these tests
	return nil, errors.New("not implemented")
}

func TestAgentRunner(t *testing.T) {
	// Create a mock agent
	mockResp := message.NewAssistantMessage("This is a test response")
	mockA := &mockAgent{
		name:          "test-agent",
		description:   "Test agent for runner",
		returnMessage: mockResp,
	}

	// Create a runner
	runner := NewAgentRunner("test-runner", mockA, nil)

	// Check runner name
	if runner.Name() != "test-runner" {
		t.Errorf("Expected runner name 'test-runner', got %s", runner.Name())
	}

	// Run the agent
	ctx := context.Background()
	response, err := runner.Run(ctx, "Hello, agent!")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Check the response
	if response.Content != mockResp.Content {
		t.Errorf("Expected response content '%s', got '%s'", mockResp.Content, response.Content)
	}
}

func TestAgentRunnerError(t *testing.T) {
	// Create a mock agent that returns an error
	expectedErr := errors.New("agent error")
	mockA := &mockAgent{
		name:        "error-agent",
		description: "Agent that returns errors",
		returnError: expectedErr,
	}

	// Create a runner
	runner := NewAgentRunner("error-runner", mockA, nil)

	// Run the agent
	ctx := context.Background()
	_, err := runner.Run(ctx, "Trigger error")
	if err == nil {
		t.Fatal("Expected an error, got nil")
	}

	// Check that the error is wrapped properly
	if !errors.Is(err, expectedErr) {
		t.Errorf("Expected error to wrap '%v', got '%v'", expectedErr, err)
	}
}

func TestAgentRunnerDefaultName(t *testing.T) {
	// Create a mock agent
	mockA := &mockAgent{
		name:        "name-test-agent",
		description: "Test agent for name defaulting",
	}

	// Create a runner with empty name
	runner := NewAgentRunner("", mockA, nil)

	// Check that the runner name includes the agent name
	expectedName := "runner-name-test-agent"
	if runner.Name() != expectedName {
		t.Errorf("Expected default runner name '%s', got '%s'", expectedName, runner.Name())
	}
}
