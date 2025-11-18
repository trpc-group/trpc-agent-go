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

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// ReducerRegistry manages state reducer registration and lookup.
// It provides a central place to register and retrieve reducers for state schema.
//
// Reducers can be registered in two ways:
// 1. Framework built-in reducers (registered at init time)
// 2. Business custom reducers (registered before service starts)
//
// This follows the same pattern as ModelRegistry and ToolRegistry.
type ReducerRegistry struct {
	mu       sync.RWMutex
	reducers map[string]graph.StateReducer
}

// NewReducerRegistry creates a new reducer registry with built-in reducers pre-registered.
func NewReducerRegistry() *ReducerRegistry {
	r := &ReducerRegistry{
		reducers: make(map[string]graph.StateReducer),
	}

	// Register framework built-in reducers
	r.registerBuiltinReducers()

	return r
}

// registerBuiltinReducers registers all framework built-in reducers.
func (r *ReducerRegistry) registerBuiltinReducers() {
	// Basic reducers
	r.reducers["default"] = graph.DefaultReducer
	r.reducers["append"] = graph.AppendReducer
	r.reducers["merge"] = graph.MergeReducer

	// Message-specific reducer
	r.reducers["message"] = graph.MessageReducer

	// Slice reducers
	r.reducers["string_slice"] = graph.StringSliceReducer
}

// Register registers a reducer with the given name.
// The name should be descriptive and follow snake_case convention (e.g., "append_map_slice", "int_sum").
func (r *ReducerRegistry) Register(name string, reducer graph.StateReducer) error {
	if name == "" {
		return fmt.Errorf("reducer name cannot be empty")
	}

	if reducer == nil {
		return fmt.Errorf("reducer cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.reducers[name]; exists {
		return fmt.Errorf("reducer %q already registered", name)
	}

	r.reducers[name] = reducer
	return nil
}

// MustRegister registers a reducer and panics if registration fails.
// This is useful for init-time registration of built-in reducers.
func (r *ReducerRegistry) MustRegister(name string, reducer graph.StateReducer) {
	if err := r.Register(name, reducer); err != nil {
		panic(err)
	}
}

// Get retrieves a reducer by name.
func (r *ReducerRegistry) Get(name string) (graph.StateReducer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reducer, exists := r.reducers[name]
	return reducer, exists
}

// Has checks if a reducer is registered.
func (r *ReducerRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.reducers[name]
	return exists
}

// List returns all registered reducer names.
func (r *ReducerRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.reducers))
	for name := range r.reducers {
		names = append(names, name)
	}
	return names
}

// Unregister removes a reducer from the registry.
// This is mainly for testing purposes.
func (r *ReducerRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.reducers, name)
}

// Clear removes all reducers from the registry.
// This is mainly for testing purposes.
func (r *ReducerRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reducers = make(map[string]graph.StateReducer)
	// Re-register built-in reducers
	r.registerBuiltinReducers()
}
