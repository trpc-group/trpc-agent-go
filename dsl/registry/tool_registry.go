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

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolRegistry is a thread-safe registry for managing tool instances.
// Tools are registered at application startup and referenced by name in DSL graphs.
//
// Note: Built-in tools (like duckduckgo_search) are automatically registered in
// DefaultToolRegistry. You can use NewToolRegistryWithBuiltins() to create a new
// registry with built-in tools pre-registered, or use NewToolRegistry() to create
// an empty registry and register tools manually.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]tool.Tool
}

// NewToolRegistry creates a new empty ToolRegistry without any built-in tools.
// If you want built-in tools pre-registered, use NewToolRegistryWithBuiltins() instead.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]tool.Tool),
	}
}

// Register registers a tool with the given name.
// Returns an error if a tool with the same name already exists.
func (r *ToolRegistry) Register(name string, t tool.Tool) error {
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}
	if t == nil {
		return fmt.Errorf("tool cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}

	r.tools[name] = t
	return nil
}

// MustRegister registers a tool and panics if registration fails.
// This is useful for init-time registration.
func (r *ToolRegistry) MustRegister(name string, t tool.Tool) {
	if err := r.Register(name, t); err != nil {
		panic(err)
	}
}

// Get retrieves a tool by name.
// Returns an error if the tool is not found.
func (r *ToolRegistry) Get(name string) (tool.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool %q not found in registry", name)
	}

	return t, nil
}

// GetMultiple retrieves multiple tools by names.
// Returns a map of name -> tool and an error if any tool is not found.
func (r *ToolRegistry) GetMultiple(names []string) (map[string]tool.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]tool.Tool, len(names))
	for _, name := range names {
		t, exists := r.tools[name]
		if !exists {
			return nil, fmt.Errorf("tool %q not found in registry", name)
		}
		result[name] = t
	}

	return result, nil
}

// GetAll returns all registered tools as a map.
func (r *ToolRegistry) GetAll() map[string]tool.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]tool.Tool, len(r.tools))
	for name, t := range r.tools {
		result[name] = t
	}

	return result
}

// Has checks if a tool with the given name exists.
func (r *ToolRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.tools[name]
	return exists
}

// List returns a list of all registered tool names.
func (r *ToolRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}

	return names
}

// Unregister removes a tool from the registry.
// This is mainly for testing purposes.
func (r *ToolRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.tools, name)
}

// Clear removes all tools from the registry.
// This is mainly for testing purposes.
func (r *ToolRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tools = make(map[string]tool.Tool)
}
