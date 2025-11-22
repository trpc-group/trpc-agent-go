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
)

// Registry manages component registration and lookup.
// It provides a central place to register and retrieve components for DSL workflows.
//
// Components can be registered in two ways:
// 1. Framework built-in components (registered at init time)
// 2. Business custom components (registered before service starts)
type Registry struct {
	mu         sync.RWMutex
	components map[string]Component
}

// NewRegistry creates a new component registry.
func NewRegistry() *Registry {
	return &Registry{
		components: make(map[string]Component),
	}
}

// Register registers a component with the given name.
// The name should follow the format: "namespace.component" (e.g., "builtin.llm", "custom.processor")
func (r *Registry) Register(component Component) error {
	metadata := component.Metadata()
	if metadata.Name == "" {
		return fmt.Errorf("component name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.components[metadata.Name]; exists {
		return fmt.Errorf("component %q already registered", metadata.Name)
	}

	r.components[metadata.Name] = component
	return nil
}

// MustRegister registers a component and panics if registration fails.
// This is useful for init-time registration of built-in components.
func (r *Registry) MustRegister(component Component) {
	if err := r.Register(component); err != nil {
		panic(err)
	}
}

// Get retrieves a component by name.
func (r *Registry) Get(name string) (Component, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	component, exists := r.components[name]
	return component, exists
}

// Has checks if a component is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.components[name]
	return exists
}

// List returns all registered component names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.components))
	for name := range r.components {
		names = append(names, name)
	}
	return names
}

// ListMetadata returns metadata for all registered components.
// This is useful for frontend to discover available components.
func (r *Registry) ListMetadata() []ComponentMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	metadata := make([]ComponentMetadata, 0, len(r.components))
	for _, component := range r.components {
		metadata = append(metadata, component.Metadata())
	}
	return metadata
}

// GetMetadata retrieves metadata for a specific component.
func (r *Registry) GetMetadata(name string) (ComponentMetadata, error) {
	component, exists := r.Get(name)
	if !exists {
		return ComponentMetadata{}, fmt.Errorf("component %q not found", name)
	}
	return component.Metadata(), nil
}

// Unregister removes a component from the registry.
// This is mainly for testing purposes.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.components, name)
}

// Clear removes all components from the registry.
// This is mainly for testing purposes.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = make(map[string]Component)
}

// DefaultRegistry is the global default registry.
// Built-in components register themselves here at init time.
var DefaultRegistry = NewRegistry()

// Register registers a component in the default registry.
func Register(component Component) error {
	return DefaultRegistry.Register(component)
}

// MustRegister registers a component in the default registry and panics on error.
func MustRegister(component Component) {
	DefaultRegistry.MustRegister(component)
}

// Get retrieves a component from the default registry.
func Get(name string) (Component, bool) {
	return DefaultRegistry.Get(name)
}

// Has checks if a component exists in the default registry.
func Has(name string) bool {
	return DefaultRegistry.Has(name)
}

// List returns all component names from the default registry.
func List() []string {
	return DefaultRegistry.List()
}

// ListMetadata returns all component metadata from the default registry.
func ListMetadata() []ComponentMetadata {
	return DefaultRegistry.ListMetadata()
}

// GetMetadata retrieves component metadata from the default registry.
func GetMetadata(name string) (ComponentMetadata, error) {
	return DefaultRegistry.GetMetadata(name)
}
