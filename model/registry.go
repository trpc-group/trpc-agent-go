// Package model provides interfaces and implementations for working with LLMs.
package model

import (
	"fmt"
	"sync"
)

// Registry is a registry of model configurations and instances.
type Registry struct {
	mu            sync.RWMutex
	configs       map[string]*Config
	models        map[string]Model
	defaultConfig *Config
}

// NewRegistry creates a new model registry.
func NewRegistry() *Registry {
	return &Registry{
		configs: make(map[string]*Config),
		models:  make(map[string]Model),
	}
}

// RegisterConfig registers a model configuration.
func (r *Registry) RegisterConfig(config *Config) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if config.Name == "" {
		return fmt.Errorf("model name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.configs[config.Name] = config

	// Set as default if this is the first config
	if r.defaultConfig == nil {
		r.defaultConfig = config
	}

	return nil
}

// RegisterModel registers a model instance.
func (r *Registry) RegisterModel(model Model) error {
	if model == nil {
		return fmt.Errorf("model cannot be nil")
	}

	name := model.Name()
	if name == "" {
		return fmt.Errorf("model name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.models[name] = model

	return nil
}

// GetConfig returns a model configuration by name.
func (r *Registry) GetConfig(name string) (*Config, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	config, exists := r.configs[name]
	return config, exists
}

// GetModel returns a model instance by name.
func (r *Registry) GetModel(name string) (Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	model, exists := r.models[name]
	return model, exists
}

// GetDefaultConfig returns the default model configuration.
func (r *Registry) GetDefaultConfig() (*Config, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.defaultConfig == nil {
		return nil, false
	}
	return r.defaultConfig, true
}

// SetDefaultConfig sets the default model configuration.
func (r *Registry) SetDefaultConfig(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	config, exists := r.configs[name]
	if !exists {
		return fmt.Errorf("model configuration %s not found", name)
	}

	r.defaultConfig = config
	return nil
}

// ListConfigs returns a list of all registered model configurations.
func (r *Registry) ListConfigs() []*Config {
	r.mu.RLock()
	defer r.mu.RUnlock()

	configs := make([]*Config, 0, len(r.configs))
	for _, config := range r.configs {
		configs = append(configs, config)
	}
	return configs
}

// ListModels returns a list of all registered model instances.
func (r *Registry) ListModels() []Model {
	r.mu.RLock()
	defer r.mu.RUnlock()

	models := make([]Model, 0, len(r.models))
	for _, model := range r.models {
		models = append(models, model)
	}
	return models
}

// HasModel checks if a model with the given name exists.
func (r *Registry) HasModel(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.models[name]
	return exists
}

// HasConfig checks if a model configuration with the given name exists.
func (r *Registry) HasConfig(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.configs[name]
	return exists
}

// DefaultRegistry is the global model registry.
var DefaultRegistry = NewRegistry()
