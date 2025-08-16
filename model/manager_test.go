//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockModelForManager implements the Model interface for testing.
type mockModelForManager struct {
	name string
}

func (m *mockModelForManager) GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error) {
	// Mock implementation for testing.
	return nil, nil
}

func (m *mockModelForManager) Info() Info {
	return Info{Name: m.name}
}

func TestNewManager(t *testing.T) {
	// Test creating a new manager with a default model.
	defaultModel := &mockModelForManager{name: "default"}
	manager := NewManager(defaultModel)

	assert.NotNil(t, manager)
	assert.Equal(t, defaultModel, manager.ActiveModel())
	assert.Equal(t, 1, len(manager.Models()))
}

func TestManager_SwitchModel(t *testing.T) {
	// Test switching between models.
	defaultModel := &mockModelForManager{name: "default"}
	otherModel := &mockModelForManager{name: "other"}

	manager := NewManager(defaultModel)
	err := manager.RegisterModels(otherModel)
	assert.NoError(t, err)

	// Switch to other model.
	err = manager.SwitchModel("other")
	assert.NoError(t, err)
	assert.Equal(t, otherModel, manager.ActiveModel())

	// Switch back to default model.
	err = manager.SwitchModel("default")
	assert.NoError(t, err)
	assert.Equal(t, defaultModel, manager.ActiveModel())
}

func TestManager_SwitchModelNotFound(t *testing.T) {
	// Test switching to a non-existent model.
	defaultModel := &mockModelForManager{name: "default"}
	manager := NewManager(defaultModel)

	err := manager.SwitchModel("non-existent")
	assert.Error(t, err)
	assert.Equal(t, "model non-existent not found", err.Error())
}

func TestManager_Models(t *testing.T) {
	// Test getting all models.
	defaultModel := &mockModelForManager{name: "default"}
	otherModel := &mockModelForManager{name: "other"}

	manager := NewManager(defaultModel)
	manager.RegisterModels(otherModel)

	models := manager.Models()
	assert.Equal(t, 2, len(models))

	// Check that both models are present.
	modelNames := make(map[string]bool)
	for _, m := range models {
		modelNames[m.Info().Name] = true
	}
	assert.True(t, modelNames["default"])
	assert.True(t, modelNames["other"])
}

func TestManager_GetModel(t *testing.T) {
	// Test getting a model by name.
	defaultModel := &mockModelForManager{name: "default"}
	otherModel := &mockModelForManager{name: "other"}

	manager := NewManager(defaultModel)
	manager.RegisterModels(otherModel)

	// Get existing models.
	model, err := manager.GetModel("default")
	assert.NoError(t, err)
	assert.Equal(t, defaultModel, model)

	model, err = manager.GetModel("other")
	assert.NoError(t, err)
	assert.Equal(t, otherModel, model)

	// Get non-existent model.
	model, err = manager.GetModel("non-existent")
	assert.Error(t, err)
	assert.Nil(t, model)
	assert.Equal(t, "model non-existent not found", err.Error())
}

func TestManager_RegisterModels(t *testing.T) {
	// Test registering multiple models at once.
	defaultModel := &mockModelForManager{name: "default"}
	manager := NewManager(defaultModel)

	// Register multiple models.
	models := []Model{
		&mockModelForManager{name: "model1"},
		&mockModelForManager{name: "model2"},
		&mockModelForManager{name: "model3"},
	}

	err := manager.RegisterModels(models...)
	assert.NoError(t, err)

	// Verify all models were registered.
	allModels := manager.Models()
	assert.Equal(t, 4, len(allModels)) // default + 3 new models

	// Check that all model names are present.
	modelNames := make(map[string]bool)
	for _, m := range allModels {
		modelNames[m.Info().Name] = true
	}
	assert.True(t, modelNames["default"])
	assert.True(t, modelNames["model1"])
	assert.True(t, modelNames["model2"])
	assert.True(t, modelNames["model3"])
}

func TestManager_ConcurrentAccess(t *testing.T) {
	// Test concurrent access to the manager.
	defaultModel := &mockModelForManager{name: "default"}
	manager := NewManager(defaultModel)

	// Add some models for testing.
	models := make([]Model, 10)
	for i := 0; i < 10; i++ {
		models[i] = &mockModelForManager{name: "model-" + string(rune(i+'0'))}
	}
	manager.RegisterModels(models...)

	// Test concurrent reads.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_ = manager.ActiveModel()
			_ = manager.Models()
			done <- true
		}()
	}

	// Wait for all goroutines to complete.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Test concurrent writes.
	for i := 0; i < 10; i++ {
		go func() {
			_ = manager.SwitchModel("default")
		}()
	}

	// Verify the manager is still in a consistent state.
	assert.Equal(t, defaultModel, manager.ActiveModel())
}
