package model

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/message"
)

// MockRegistryModel is a simple mock model for testing the registry.
type MockRegistryModel struct {
	name           string
	provider       string
	generateCalled bool
}

func NewMockRegistryModel(name, provider string) *MockRegistryModel {
	return &MockRegistryModel{
		name:     name,
		provider: provider,
	}
}

func (m *MockRegistryModel) Name() string {
	return m.name
}

func (m *MockRegistryModel) Provider() string {
	return m.provider
}

func (m *MockRegistryModel) Generate(ctx context.Context, prompt string, options GenerationOptions) (*Response, error) {
	m.generateCalled = true
	return &Response{
		Text: "This is a test response",
	}, nil
}

func (m *MockRegistryModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options GenerationOptions) (*Response, error) {
	m.generateCalled = true
	return &Response{
		Text: "This is a test response",
	}, nil
}

// SupportsToolCalls implements the Model interface
func (m *MockRegistryModel) SupportsToolCalls() bool {
	return false
}

func TestNewRegistry(t *testing.T) {
	registry := NewRegistry()
	if registry == nil {
		t.Fatal("Expected registry to be non-nil")
	}
	if registry.configs == nil {
		t.Error("Expected configs map to be initialized")
	}
	if registry.models == nil {
		t.Error("Expected models map to be initialized")
	}
}

func TestRegistry_RegisterConfig(t *testing.T) {
	registry := NewRegistry()

	// Test with valid config
	config := &ModelConfig{
		Name:     "test-model",
		Provider: "test-provider",
	}
	err := registry.RegisterConfig(config)
	if err != nil {
		t.Errorf("Unexpected error registering config: %v", err)
	}

	// Test with nil config
	err = registry.RegisterConfig(nil)
	if err == nil {
		t.Error("Expected error when registering nil config")
	}

	// Test with empty name
	config = &ModelConfig{
		Provider: "test-provider",
	}
	err = registry.RegisterConfig(config)
	if err == nil {
		t.Error("Expected error when registering config with empty name")
	}
}

func TestRegistry_RegisterModel(t *testing.T) {
	registry := NewRegistry()

	// Test with valid model
	model := NewMockRegistryModel("test-model", "test-provider")
	err := registry.RegisterModel(model)
	if err != nil {
		t.Errorf("Unexpected error registering model: %v", err)
	}

	// Test with nil model
	err = registry.RegisterModel(nil)
	if err == nil {
		t.Error("Expected error when registering nil model")
	}
}

func TestRegistry_GetConfig(t *testing.T) {
	registry := NewRegistry()
	config := &ModelConfig{
		Name:     "test-model",
		Provider: "test-provider",
	}
	_ = registry.RegisterConfig(config)

	// Test getting a registered config
	gotConfig, exists := registry.GetConfig("test-model")
	if !exists {
		t.Error("Expected config to exist")
	}
	if gotConfig != config {
		t.Error("Expected to get the same config instance")
	}

	// Test getting a non-existent config
	_, exists = registry.GetConfig("non-existent")
	if exists {
		t.Error("Expected non-existent config to not exist")
	}
}

func TestRegistry_GetModel(t *testing.T) {
	registry := NewRegistry()
	model := NewMockRegistryModel("test-model", "test-provider")
	_ = registry.RegisterModel(model)

	// Test getting a registered model
	gotModel, exists := registry.GetModel("test-model")
	if !exists {
		t.Error("Expected model to exist")
	}

	// We need to cast to the concrete type for comparison
	mockModel, ok := gotModel.(*MockRegistryModel)
	if !ok {
		t.Error("Expected model to be of type *MockRegistryModel")
	} else if mockModel != model {
		t.Error("Expected to get the same model instance")
	}

	// Test getting a non-existent model
	_, exists = registry.GetModel("non-existent")
	if exists {
		t.Error("Expected non-existent model to not exist")
	}
}

func TestRegistry_GetDefaultConfig(t *testing.T) {
	registry := NewRegistry()

	// Test when no default is set
	_, exists := registry.GetDefaultConfig()
	if exists {
		t.Error("Expected no default config to exist")
	}

	// Test when a default is set
	config := &ModelConfig{
		Name:     "test-model",
		Provider: "test-provider",
	}
	_ = registry.RegisterConfig(config)
	gotConfig, exists := registry.GetDefaultConfig()
	if !exists {
		t.Error("Expected default config to exist")
	}
	if gotConfig != config {
		t.Error("Expected default config to match the first registered config")
	}
}

func TestRegistry_SetDefaultConfig(t *testing.T) {
	registry := NewRegistry()
	config1 := &ModelConfig{
		Name:     "model1",
		Provider: "provider1",
	}
	config2 := &ModelConfig{
		Name:     "model2",
		Provider: "provider2",
	}
	_ = registry.RegisterConfig(config1)
	_ = registry.RegisterConfig(config2)

	// Test setting a valid default
	err := registry.SetDefaultConfig("model2")
	if err != nil {
		t.Errorf("Unexpected error setting default config: %v", err)
	}
	gotConfig, _ := registry.GetDefaultConfig()
	if gotConfig != config2 {
		t.Error("Expected default config to be model2")
	}

	// Test setting a non-existent default
	err = registry.SetDefaultConfig("non-existent")
	if err == nil {
		t.Error("Expected error when setting non-existent config as default")
	}
}

func TestRegistry_ListConfigs(t *testing.T) {
	registry := NewRegistry()
	config1 := &ModelConfig{
		Name:     "model1",
		Provider: "provider1",
	}
	config2 := &ModelConfig{
		Name:     "model2",
		Provider: "provider2",
	}
	_ = registry.RegisterConfig(config1)
	_ = registry.RegisterConfig(config2)

	configs := registry.ListConfigs()
	if len(configs) != 2 {
		t.Errorf("Expected 2 configs, got %d", len(configs))
	}

	// Check that both configs are in the list
	var foundConfig1, foundConfig2 bool
	for _, config := range configs {
		if config == config1 {
			foundConfig1 = true
		}
		if config == config2 {
			foundConfig2 = true
		}
	}
	if !foundConfig1 {
		t.Error("Expected to find config1 in the list")
	}
	if !foundConfig2 {
		t.Error("Expected to find config2 in the list")
	}
}

func TestRegistry_ListModels(t *testing.T) {
	registry := NewRegistry()
	model1 := NewMockRegistryModel("model1", "provider1")
	model2 := NewMockRegistryModel("model2", "provider2")
	_ = registry.RegisterModel(model1)
	_ = registry.RegisterModel(model2)

	models := registry.ListModels()
	if len(models) != 2 {
		t.Errorf("Expected 2 models, got %d", len(models))
	}

	// Check that both models are in the list
	var foundModel1, foundModel2 bool
	for _, model := range models {
		// Type assertion to compare concrete types
		if mockModel, ok := model.(*MockRegistryModel); ok {
			if mockModel == model1 {
				foundModel1 = true
			}
			if mockModel == model2 {
				foundModel2 = true
			}
		}
	}
	if !foundModel1 {
		t.Error("Expected to find model1 in the list")
	}
	if !foundModel2 {
		t.Error("Expected to find model2 in the list")
	}
}

func TestRegistry_HasModel(t *testing.T) {
	registry := NewRegistry()
	model := NewMockRegistryModel("test-model", "test-provider")
	_ = registry.RegisterModel(model)

	if !registry.HasModel("test-model") {
		t.Error("Expected HasModel to return true for registered model")
	}
	if registry.HasModel("non-existent") {
		t.Error("Expected HasModel to return false for non-existent model")
	}
}

func TestRegistry_HasConfig(t *testing.T) {
	registry := NewRegistry()
	config := &ModelConfig{
		Name:     "test-model",
		Provider: "test-provider",
	}
	_ = registry.RegisterConfig(config)

	if !registry.HasConfig("test-model") {
		t.Error("Expected HasConfig to return true for registered config")
	}
	if registry.HasConfig("non-existent") {
		t.Error("Expected HasConfig to return false for non-existent config")
	}
}
