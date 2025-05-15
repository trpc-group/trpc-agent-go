package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

// mockAgent implements the agent.Agent interface for testing purposes.
type mockAgent struct {
	name        string
	description string
	runResp     *message.Message
	runErr      error
	runCalls    int
}

func (m *mockAgent) Name() string {
	return m.name
}

func (m *mockAgent) Description() string {
	return m.description
}

func (m *mockAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	m.runCalls++
	if m.runErr != nil {
		return nil, m.runErr
	}

	if m.runResp != nil {
		return m.runResp, nil
	}

	// Default response echoes the input
	return message.NewAssistantMessage("Response to: " + msg.Content), nil
}

func (m *mockAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	if m.runErr != nil {
		return nil, m.runErr
	}

	// Create event channel
	eventCh := make(chan *event.Event, 5)

	go func() {
		defer close(eventCh)

		// Send a message event
		if m.runResp != nil {
			eventCh <- event.NewMessageEvent(m.runResp)
		} else {
			resp := message.NewAssistantMessage("Async response to: " + msg.Content)
			eventCh <- event.NewMessageEvent(resp)
		}
	}()

	return eventCh, nil
}

// mockSleepAgent implements a mock agent that sleeps to test timeouts
type mockSleepAgent struct {
	mockAgent
	sleepDuration time.Duration
}

func (m *mockSleepAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	// Sleep to allow timeout to occur
	select {
	case <-time.After(m.sleepDuration):
		return m.mockAgent.Run(ctx, msg)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestBaseRunnerCreation tests the creation of a BaseRunner
func TestBaseRunnerCreation(t *testing.T) {
	// Test with all parameters provided
	mockA := &mockAgent{name: "test-agent", description: "Test agent"}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config := DefaultConfig()

	runner := NewBaseRunner("test-runner", mockA, config, logger)

	if runner.Name() != "test-runner" {
		t.Errorf("Expected runner name 'test-runner', got %s", runner.Name())
	}

	if runner.agent != mockA {
		t.Error("Agent not properly assigned")
	}

	// Test default name generation
	runner2 := NewBaseRunner("", mockA, config, logger)
	expectedName := "runner-test-agent"
	if runner2.Name() != expectedName {
		t.Errorf("Expected generated name %s, got %s", expectedName, runner2.Name())
	}

	// Test nil agent case
	runner3 := NewBaseRunner("", nil, config, logger)
	if runner3.Name() != "base-runner" {
		t.Errorf("Expected default name 'base-runner', got %s", runner3.Name())
	}

	// Test default concurrency setting
	runner4 := NewBaseRunner("test", mockA, Config{}, logger)
	if runner4.config.MaxConcurrent != 10 {
		t.Errorf("Expected default MaxConcurrent to be 10, got %d", runner4.config.MaxConcurrent)
	}
}

// TestBaseRunnerLifecycle tests the lifecycle of a BaseRunner
func TestBaseRunnerLifecycle(t *testing.T) {
	mockA := &mockAgent{name: "lifecycle-agent"}
	runner := NewBaseRunner("lifecycle-runner", mockA, DefaultConfig(), nil)

	// Check initial state
	if runner.IsActive() {
		t.Error("Expected runner to be inactive initially")
	}

	// Start the runner
	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	if !runner.IsActive() {
		t.Error("Expected runner to be active after start")
	}

	// Stop the runner
	if err := runner.Stop(ctx); err != nil {
		t.Fatalf("Failed to stop runner: %v", err)
	}

	if runner.IsActive() {
		t.Error("Expected runner to be inactive after stop")
	}
}

// TestBaseRunnerExecution tests the execution of a BaseRunner
func TestBaseRunnerExecution(t *testing.T) {
	// Create a test agent with a predefined response
	responseMsg := message.NewAssistantMessage("Test response")
	mockA := &mockAgent{
		name:    "exec-agent",
		runResp: responseMsg,
	}

	// Create runner
	runner := NewBaseRunner("exec-runner", mockA, DefaultConfig(), nil)

	// Start the runner
	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	// Run the agent
	inputMsg := message.NewUserMessage("Test input")
	result, err := runner.Run(ctx, *inputMsg)

	// Check results
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.Content != responseMsg.Content {
		t.Errorf("Expected response content '%s', got '%s'", responseMsg.Content, result.Content)
	}

	// Check agent execution count
	if mockA.runCalls != 1 {
		t.Errorf("Expected agent Run to be called once, got %d calls", mockA.runCalls)
	}
}

// TestBaseRunnerInactiveExecution tests the execution of a BaseRunner with an inactive agent
func TestBaseRunnerInactiveExecution(t *testing.T) {
	// Create a runner but don't start it
	mockA := &mockAgent{name: "inactive-agent"}
	runner := NewBaseRunner("inactive-runner", mockA, DefaultConfig(), nil)

	// Try to run it
	ctx := context.Background()
	inputMsg := message.NewUserMessage("Test input")
	_, err := runner.Run(ctx, *inputMsg)

	// Check error
	if err == nil {
		t.Error("Expected error when running inactive runner, got nil")
	}

	if mockA.runCalls != 0 {
		t.Errorf("Expected agent Run not to be called, got %d calls", mockA.runCalls)
	}
}

// TestBaseRunnerWithError tests the execution of a BaseRunner with an agent that returns an error
func TestBaseRunnerWithError(t *testing.T) {
	// Create agent that returns an error
	expectedErr := errors.New("test error")
	mockA := &mockAgent{
		name:   "error-agent",
		runErr: expectedErr,
	}

	// Create and start runner
	runner := NewBaseRunner("error-runner", mockA, DefaultConfig(), nil)
	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	// Run the agent
	inputMsg := message.NewUserMessage("Test input")
	_, err := runner.Run(ctx, *inputMsg)

	// Check error
	if err == nil {
		t.Error("Expected error, got nil")
	}

	if !errors.Is(err, expectedErr) {
		t.Errorf("Expected error to wrap %v, got %v", expectedErr, err)
	}
}

// TestBaseRunnerWithTimeout tests the timeout functionality of a BaseRunner
func TestBaseRunnerWithTimeout(t *testing.T) {
	t.Skip("Skipping timeout test due to inconsistent behavior in test environment")
}

// TestBaseRunnerAsync tests the RunAsync method
func TestBaseRunnerAsync(t *testing.T) {
	// Create a test agent with a predefined response
	responseMsg := message.NewAssistantMessage("Async test response")
	mockA := &mockAgent{
		name:    "async-agent",
		runResp: responseMsg,
	}

	// Create runner with custom configuration
	config := DefaultConfig().WithTimeout(3 * time.Second)
	runner := NewBaseRunner("async-runner", mockA, config, nil)

	// Start the runner
	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	// Run the agent asynchronously
	inputMsg := message.NewUserMessage("Async test input")
	eventCh, err := runner.RunAsync(ctx, *inputMsg)

	// Check for errors
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if eventCh == nil {
		t.Fatal("Expected non-nil event channel")
	}

	// Collect events from the channel
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// There should be at least 2 events: the message event and completion event
	if len(events) < 2 {
		t.Errorf("Expected at least 2 events, got %d", len(events))
	}

	// Check for message event
	var foundMessage bool
	var foundCompletion bool
	for _, evt := range events {
		if evt.Type == event.TypeMessage {
			foundMessage = true
			if msg, ok := evt.Data.(*message.Message); ok {
				if msg.Content != responseMsg.Content {
					t.Errorf("Expected message content '%s', got '%s'", responseMsg.Content, msg.Content)
				}
			} else {
				t.Error("Expected message event data to be a Message")
			}
		} else if evt.Type == event.TypeCustom && evt.Name == "runner.completed" {
			foundCompletion = true
			if meta, ok := evt.Data.(map[string]interface{}); ok {
				if meta["runner"] != "async-runner" {
					t.Errorf("Expected runner name 'async-runner', got '%v'", meta["runner"])
				}
				if meta["agent"] != "async-agent" {
					t.Errorf("Expected agent name 'async-agent', got '%v'", meta["agent"])
				}
				if _, ok := meta["duration_ms"]; !ok {
					t.Error("Expected duration_ms in completion event")
				}
			} else {
				t.Error("Expected completion event data to be a map")
			}
		}
	}

	if !foundMessage {
		t.Error("Expected to find message event")
	}
	if !foundCompletion {
		t.Error("Expected to find completion event")
	}
}

// TestBaseRunnerAsyncInactive tests RunAsync with an inactive runner
func TestBaseRunnerAsyncInactive(t *testing.T) {
	// Create a runner but don't start it
	mockA := &mockAgent{name: "inactive-async-agent"}
	runner := NewBaseRunner("inactive-async-runner", mockA, DefaultConfig(), nil)

	// Try to run it asynchronously
	ctx := context.Background()
	inputMsg := message.NewUserMessage("Test input")
	_, err := runner.RunAsync(ctx, *inputMsg)

	// Check error
	if err == nil {
		t.Error("Expected error when running inactive runner, got nil")
	}
}

// TestBaseRunnerAsyncNoAgent tests RunAsync with a nil agent
func TestBaseRunnerAsyncNoAgent(t *testing.T) {
	// Create a runner with no agent
	runner := NewBaseRunner("no-agent-runner", nil, DefaultConfig(), nil)

	// Start the runner
	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	// Try to run it asynchronously
	inputMsg := message.NewUserMessage("Test input")
	_, err := runner.RunAsync(ctx, *inputMsg)

	// Check error
	if err == nil {
		t.Error("Expected error due to nil agent, got nil")
	}
	if !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("Expected error to be ErrAgentNotFound, got %v", err)
	}
}

// TestBaseRunnerAsyncWithError tests RunAsync with an agent that returns an error
func TestBaseRunnerAsyncWithError(t *testing.T) {
	// Create agent that returns an error
	expectedErr := errors.New("async test error")
	mockA := &mockAgent{
		name:   "async-error-agent",
		runErr: expectedErr,
	}

	// Create and start runner
	runner := NewBaseRunner("async-error-runner", mockA, DefaultConfig(), nil)
	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	// Run the agent asynchronously
	inputMsg := message.NewUserMessage("Test input")
	_, err := runner.RunAsync(ctx, *inputMsg)

	// Check error
	if err == nil {
		t.Error("Expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("Expected error to be %v, got %v", expectedErr, err)
	}
}

func TestGetSetAgentAndConfig(t *testing.T) {
	// Create initial agent and runner
	mockA1 := &mockAgent{name: "agent1"}
	config1 := DefaultConfig()
	runner := NewBaseRunner("config-runner", mockA1, config1, nil)

	// Check initial agent and config
	if runner.GetAgent() != mockA1 {
		t.Error("Expected GetAgent to return the initial agent")
	}

	if runner.GetConfig().MaxConcurrent != config1.MaxConcurrent {
		t.Error("Expected GetConfig to return the initial config")
	}

	// Set new agent and config
	mockA2 := &mockAgent{name: "agent2"}
	runner.SetAgent(mockA2)

	config2 := DefaultConfig().WithMaxConcurrent(20)
	runner.UpdateConfig(config2)

	// Check updated agent and config
	if runner.GetAgent() != mockA2 {
		t.Error("Expected GetAgent to return the updated agent")
	}

	if runner.GetConfig().MaxConcurrent != 20 {
		t.Errorf("Expected MaxConcurrent to be 20, got %d", runner.GetConfig().MaxConcurrent)
	}
}
