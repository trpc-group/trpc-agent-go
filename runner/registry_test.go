package runner

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

// mockRunner implements the Runner interface for testing.
type mockRunner struct {
	name   string
	active bool
}

func (m *mockRunner) Name() string {
	return m.name
}

func (m *mockRunner) Start(ctx context.Context) error {
	m.active = true
	return nil
}

func (m *mockRunner) Stop(ctx context.Context) error {
	m.active = false
	return nil
}

func (m *mockRunner) Run(ctx context.Context, input message.Message) (*message.Message, error) {
	return message.NewAssistantMessage("Mock response from " + m.name), nil
}

func (m *mockRunner) RunAsync(ctx context.Context, input message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 1)

	go func() {
		defer close(eventCh)
		resp := message.NewAssistantMessage("Async mock response from " + m.name)
		eventCh <- event.NewMessageEvent(resp)
	}()

	return eventCh, nil
}

func TestRegistryBasicOperations(t *testing.T) {
	registry := NewRegistry()

	// Test registering a runner
	runner1 := &mockRunner{name: "test-runner"}
	if err := registry.RegisterRunner(runner1); err != nil {
		t.Fatalf("Failed to register runner: %v", err)
	}

	// Test getting the runner
	retrieved, err := registry.GetRunner("test-runner")
	if err != nil {
		t.Fatalf("Failed to get runner: %v", err)
	}
	if retrieved.Name() != "test-runner" {
		t.Errorf("Expected runner name 'test-runner', got '%s'", retrieved.Name())
	}

	// Test listing runners
	names := registry.ListRunners()
	if len(names) != 1 || names[0] != "test-runner" {
		t.Errorf("Expected list to contain only 'test-runner', got %v", names)
	}

	// Test error on duplicate registration
	if err := registry.RegisterRunner(runner1); err == nil {
		t.Error("Expected error when registering duplicate runner, got nil")
	}

	// Test error when getting non-existent runner
	if _, err := registry.GetRunner("non-existent"); err == nil {
		t.Error("Expected error when getting non-existent runner, got nil")
	}

	// Test unregistering a runner
	if err := registry.UnregisterRunner("test-runner"); err != nil {
		t.Fatalf("Failed to unregister runner: %v", err)
	}

	// Verify runner is unregistered
	if _, err := registry.GetRunner("test-runner"); err == nil {
		t.Error("Expected error when getting unregistered runner, got nil")
	}

	// Test error when unregistering non-existent runner
	if err := registry.UnregisterRunner("non-existent"); err == nil {
		t.Error("Expected error when unregistering non-existent runner, got nil")
	}
}

func TestConfigRegistration(t *testing.T) {
	registry := NewRegistry()

	// Test registering a config
	config := Config{
		MaxConcurrent: 20,
		RetryCount:    5,
	}
	if err := registry.RegisterConfig("test-config", config); err != nil {
		t.Fatalf("Failed to register config: %v", err)
	}

	// Test getting the config
	retrieved, err := registry.GetConfig("test-config")
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}
	if retrieved.MaxConcurrent != 20 || retrieved.RetryCount != 5 {
		t.Errorf("Retrieved config doesn't match original: got %+v", retrieved)
	}

	// Test error when getting non-existent config
	if _, err := registry.GetConfig("non-existent"); err == nil {
		t.Error("Expected error when getting non-existent config, got nil")
	}
}

func TestGlobalRegistry(t *testing.T) {
	// Reset the global registry to ensure a clean state
	Reset()

	// Test global registry functions
	runner1 := &mockRunner{name: "global-test"}
	if err := RegisterRunner(runner1); err != nil {
		t.Fatalf("Failed to register runner in global registry: %v", err)
	}

	retrieved, err := GetRunner("global-test")
	if err != nil {
		t.Fatalf("Failed to get runner from global registry: %v", err)
	}
	if retrieved.Name() != "global-test" {
		t.Errorf("Expected runner name 'global-test', got '%s'", retrieved.Name())
	}

	names := ListRunners()
	if len(names) != 1 || names[0] != "global-test" {
		t.Errorf("Expected global list to contain only 'global-test', got %v", names)
	}

	config := DefaultConfig()
	if err := RegisterConfig("global-config", config); err != nil {
		t.Fatalf("Failed to register config in global registry: %v", err)
	}

	_, err = GetConfig("global-config")
	if err != nil {
		t.Fatalf("Failed to get config from global registry: %v", err)
	}

	// Test global unregister
	if err := UnregisterRunner("global-test"); err != nil {
		t.Fatalf("Failed to unregister runner from global registry: %v", err)
	}

	if _, err := GetRunner("global-test"); err == nil {
		t.Error("Expected error when getting unregistered runner from global registry, got nil")
	}
}

func TestRegistryReset(t *testing.T) {
	registry := NewRegistry()

	// Register a runner and config
	runner1 := &mockRunner{name: "reset-test"}
	if err := registry.RegisterRunner(runner1); err != nil {
		t.Fatalf("Failed to register runner: %v", err)
	}

	if err := registry.RegisterConfig("reset-config", DefaultConfig()); err != nil {
		t.Fatalf("Failed to register config: %v", err)
	}

	// Reset the registry
	registry.Reset()

	// Verify that runners and configs are cleared
	if len(registry.ListRunners()) != 0 {
		t.Error("Expected empty runner list after reset")
	}

	if _, err := registry.GetConfig("reset-config"); err == nil {
		t.Error("Expected error when getting config after reset, got nil")
	}
}
