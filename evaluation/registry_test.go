package evaluation

import (
	"context"
	"testing"
)

// mockEvaluator implements the Evaluator interface for testing.
type mockEvaluator struct {
	name string
}

func (m *mockEvaluator) Evaluate(ctx context.Context, input EvaluationInput) (*Result, error) {
	metrics := Metrics{
		ResponseQuality: 0.9,
		Correctness:     0.8,
		Relevance:       0.85,
		ToolUsage:       0.95,
	}
	return NewResult("test-run", metrics, 1, 0, 1), nil
}

func (m *mockEvaluator) Name() string {
	return m.name
}

func TestRegistryBasicOperations(t *testing.T) {
	reg := NewRegistry()

	// Test registering an evaluator
	eval1 := &mockEvaluator{name: "test-evaluator"}
	if err := reg.RegisterEvaluator(eval1); err != nil {
		t.Fatalf("Failed to register evaluator: %v", err)
	}

	// Test getting the evaluator
	retrieved, err := reg.GetEvaluator("test-evaluator")
	if err != nil {
		t.Fatalf("Failed to get evaluator: %v", err)
	}
	if retrieved.Name() != "test-evaluator" {
		t.Errorf("Expected evaluator name 'test-evaluator', got '%s'", retrieved.Name())
	}

	// Test listing evaluators
	names := reg.ListEvaluators()
	if len(names) != 1 || names[0] != "test-evaluator" {
		t.Errorf("Expected list to contain only 'test-evaluator', got %v", names)
	}

	// Test error on duplicate registration
	if err := reg.RegisterEvaluator(eval1); err == nil {
		t.Error("Expected error when registering duplicate evaluator, got nil")
	}

	// Test error when getting non-existent evaluator
	if _, err := reg.GetEvaluator("non-existent"); err == nil {
		t.Error("Expected error when getting non-existent evaluator, got nil")
	}
}

func TestConfigRegistration(t *testing.T) {
	reg := NewRegistry()

	// Test registering a config
	config := Config{
		MinResponseQuality: 0.8,
		MinToolUsageScore:  0.7,
		MinCorrectness:     0.75,
		MinRelevance:       0.85,
		Runs:               3,
	}
	if err := reg.RegisterConfig("test-config", config); err != nil {
		t.Fatalf("Failed to register config: %v", err)
	}

	// Test getting the config
	retrieved, err := reg.GetConfig("test-config")
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}
	if retrieved.MinResponseQuality != 0.8 {
		t.Errorf("Expected MinResponseQuality 0.8, got %f", retrieved.MinResponseQuality)
	}

	// Test error when getting non-existent config
	if _, err := reg.GetConfig("non-existent"); err == nil {
		t.Error("Expected error when getting non-existent config, got nil")
	}
}

func TestGlobalRegistry(t *testing.T) {
	// Reset the global registry to ensure a clean state
	globalRegistry.Reset()

	// Test global registry functions
	eval1 := &mockEvaluator{name: "global-test"}
	if err := RegisterEvaluator(eval1); err != nil {
		t.Fatalf("Failed to register evaluator in global registry: %v", err)
	}

	retrieved, err := GetEvaluator("global-test")
	if err != nil {
		t.Fatalf("Failed to get evaluator from global registry: %v", err)
	}
	if retrieved.Name() != "global-test" {
		t.Errorf("Expected evaluator name 'global-test', got '%s'", retrieved.Name())
	}

	names := ListEvaluators()
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
}

func TestRegistryReset(t *testing.T) {
	reg := NewRegistry()

	// Register an evaluator and config
	eval1 := &mockEvaluator{name: "reset-test"}
	if err := reg.RegisterEvaluator(eval1); err != nil {
		t.Fatalf("Failed to register evaluator: %v", err)
	}

	if err := reg.RegisterConfig("reset-config", DefaultConfig()); err != nil {
		t.Fatalf("Failed to register config: %v", err)
	}

	// Reset the registry
	reg.Reset()

	// Verify that evaluators and configs are cleared
	if len(reg.ListEvaluators()) != 0 {
		t.Error("Expected empty evaluator list after reset")
	}

	if _, err := reg.GetConfig("reset-config"); err == nil {
		t.Error("Expected error when getting config after reset, got nil")
	}
}
