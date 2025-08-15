//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package model provides interfaces for working with LLMs.
package model

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrModelNameEmpty is returned when the model name is empty.
	ErrModelNameEmpty = errors.New("model name cannot be empty")
)

// Manager interface for managing multiple models and switching between them.
type Manager interface {
	// ActiveModel returns the currently active model.
	ActiveModel() Model
	// SwitchModel switches to the specified model by name.
	SwitchModel(name string) error
	// Models returns a list of all available models.
	Models() []Model
	// RegisterModels registers multiple models at once.
	RegisterModels(models ...Model) error
	// GetModel retrieves a model by name.
	GetModel(name string) (Model, error)
}

// manager implements the Manager interface.
type manager struct {
	models       map[string]Model
	activeModel  Model
	defaultModel Model
	mu           sync.RWMutex
}

// NewManager creates a new model manager with the specified default model.
func NewManager(defaultModel Model) Manager {
	m := &manager{
		models:       make(map[string]Model),
		defaultModel: defaultModel,
		activeModel:  defaultModel,
	}
	// Register the default model in the models map if it has a valid name.
	if defaultModel != nil && defaultModel.Info().Name != "" {
		m.models[defaultModel.Info().Name] = defaultModel
	}
	return m
}

// ActiveModel returns the currently active model.
func (m *manager) ActiveModel() Model {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeModel
}

// SwitchModel switches to the specified model by name.
func (m *manager) SwitchModel(name string) error {
	if name == "" {
		return ErrModelNameEmpty
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	model, exists := m.models[name]
	if !exists {
		return fmt.Errorf("model %s not found", name)
	}

	m.activeModel = model
	return nil
}

// Models returns a list of all available models.
func (m *manager) Models() []Model {
	m.mu.RLock()
	defer m.mu.RUnlock()

	models := make([]Model, 0, len(m.models))
	for _, m := range m.models {
		models = append(models, m)
	}
	return models
}

// RegisterModels registers multiple models at once.
// Nil models and models with empty names are silently ignored.
func (m *manager) RegisterModels(models ...Model) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Register all valid models in a single lock operation.
	for _, model := range models {
		if model != nil && model.Info().Name != "" {
			m.models[model.Info().Name] = model
		}
	}

	return nil
}

// GetModel retrieves a model by name.
func (m *manager) GetModel(name string) (Model, error) {
	if name == "" {
		return nil, ErrModelNameEmpty
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	model, exists := m.models[name]
	if !exists {
		return nil, fmt.Errorf("model %s not found", name)
	}

	return model, nil
}
