//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package registry

import (
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolSetRegistry is a thread-safe registry for managing ToolSet instances.
// ToolSets are registered at application startup and referenced by name in DSL graphs.
//
// Note: Built-in ToolSets (like file) are automatically registered in
// DefaultToolSetRegistry when you import:
//
//	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin"
type ToolSetRegistry struct {
	mu       sync.RWMutex
	toolSets map[string]tool.ToolSet
}

// NewToolSetRegistry creates a new empty ToolSetRegistry.
func NewToolSetRegistry() *ToolSetRegistry {
	return &ToolSetRegistry{
		toolSets: make(map[string]tool.ToolSet),
	}
}

// Register registers a ToolSet with the given name.
// Returns an error if a ToolSet with the same name already exists.
func (r *ToolSetRegistry) Register(name string, ts tool.ToolSet) error {
	if name == "" {
		return fmt.Errorf("toolset name cannot be empty")
	}
	if ts == nil {
		return fmt.Errorf("toolset cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.toolSets[name]; exists {
		return fmt.Errorf("toolset %q already registered", name)
	}

	r.toolSets[name] = ts
	return nil
}

// MustRegister registers a ToolSet and panics if registration fails.
// This is useful for init-time registration.
func (r *ToolSetRegistry) MustRegister(name string, ts tool.ToolSet) {
	if err := r.Register(name, ts); err != nil {
		panic(err)
	}
}

// Get retrieves a ToolSet by name.
// Returns an error if the ToolSet is not found.
func (r *ToolSetRegistry) Get(name string) (tool.ToolSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ts, exists := r.toolSets[name]
	if !exists {
		return nil, fmt.Errorf("toolset %q not found in registry", name)
	}

	return ts, nil
}

// GetMultiple retrieves multiple ToolSets by names.
// Returns a map of name -> ToolSet and an error if any ToolSet is not found.
func (r *ToolSetRegistry) GetMultiple(names []string) (map[string]tool.ToolSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]tool.ToolSet, len(names))
	for _, name := range names {
		ts, exists := r.toolSets[name]
		if !exists {
			return nil, fmt.Errorf("toolset %q not found in registry", name)
		}
		result[name] = ts
	}

	return result, nil
}

// GetAll returns all registered ToolSets as a map.
func (r *ToolSetRegistry) GetAll() map[string]tool.ToolSet {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]tool.ToolSet, len(r.toolSets))
	for name, ts := range r.toolSets {
		result[name] = ts
	}

	return result
}

// Has checks if a ToolSet with the given name exists.
func (r *ToolSetRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.toolSets[name]
	return exists
}

// List returns a list of all registered ToolSet names.
func (r *ToolSetRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.toolSets))
	for name := range r.toolSets {
		names = append(names, name)
	}

	return names
}

// Unregister removes a ToolSet from the registry.
// This is mainly for testing purposes.
func (r *ToolSetRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.toolSets, name)
}

// Clear removes all ToolSets from the registry.
// This is mainly for testing purposes.
func (r *ToolSetRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.toolSets = make(map[string]tool.ToolSet)
}
