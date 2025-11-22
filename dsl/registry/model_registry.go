//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package registry

import (
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ModelRegistry manages LLM model instances.
// Models are registered at application startup and referenced by name in DSL.
// This follows the pattern used by Flowise and Langflow where models are
// components that can be configured and reused across the workflow.
type ModelRegistry struct {
	mu     sync.RWMutex
	models map[string]model.Model
}

// NewModelRegistry creates a new model registry.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		models: make(map[string]model.Model),
	}
}

// Register registers a model instance with a given name.
// The name is used in DSL to reference the model.
//
// Example:
//   registry.Register("deepseek-chat", openai.New("deepseek-chat"))
//   registry.Register("gpt-4", openai.New("gpt-4"))
func (r *ModelRegistry) Register(name string, model model.Model) error {
	if name == "" {
		return fmt.Errorf("model name cannot be empty")
	}
	if model == nil {
		return fmt.Errorf("model instance cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.models[name]; exists {
		return fmt.Errorf("model %q already registered", name)
	}

	r.models[name] = model
	return nil
}

// MustRegister registers a model and panics if registration fails.
// Useful for initialization code where failure should be fatal.
func (r *ModelRegistry) MustRegister(name string, model model.Model) {
	if err := r.Register(name, model); err != nil {
		panic(err)
	}
}

// Get retrieves a model by name.
// Returns an error if the model is not found.
func (r *ModelRegistry) Get(name string) (model.Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	model, exists := r.models[name]
	if !exists {
		return nil, fmt.Errorf("model %q not found in registry", name)
	}

	return model, nil
}

// Has checks if a model with the given name is registered.
func (r *ModelRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.models[name]
	return exists
}

// List returns all registered model names.
func (r *ModelRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.models))
	for name := range r.models {
		names = append(names, name)
	}
	return names
}

// Unregister removes a model from the registry.
// Returns an error if the model is not found.
func (r *ModelRegistry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.models[name]; !exists {
		return fmt.Errorf("model %q not found in registry", name)
	}

	delete(r.models, name)
	return nil
}

// Clear removes all models from the registry.
func (r *ModelRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.models = make(map[string]model.Model)
}

// Count returns the number of registered models.
func (r *ModelRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.models)
}
