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
	"testing"
)

func TestDefaultToolRegistry(t *testing.T) {
	// DefaultToolRegistry is created empty by design; built-in tools are
	// registered either via:
	//   1) importing the builtin registry package (which calls
	//      registry.DefaultToolRegistry.MustRegister(...) in init), or
	//   2) explicitly calling RegisterBuiltinTools / NewToolRegistryWithBuiltins.
	//
	// Here we exercise the second path to avoid relying on import side effects.

	// Start from a fresh registry for this test.
	reg := NewToolRegistry()

	// Before registration, duckduckgo_search should not exist.
	if reg.Has("duckduckgo_search") {
		t.Error("new ToolRegistry should NOT have duckduckgo_search registered by default")
	}

	// Register built-in tools into this registry.
	RegisterBuiltinTools(reg)

	// After registration, the built-in tool should be available.
	if !reg.Has("duckduckgo_search") {
		t.Error("registry should have duckduckgo_search registered after RegisterBuiltinTools")
	}

	tool, err := reg.Get("duckduckgo_search")
	if err != nil {
		t.Errorf("Failed to get duckduckgo_search: %v", err)
	}
	if tool == nil {
		t.Error("duckduckgo_search tool should not be nil")
	}
}

func TestNewToolRegistryWithBuiltins(t *testing.T) {
	// Create a new registry with built-ins
	registry := NewToolRegistryWithBuiltins()

	// Test that built-in tools are registered
	if !registry.Has("duckduckgo_search") {
		t.Error("NewToolRegistryWithBuiltins should have duckduckgo_search registered")
	}

	// Test that we can get the tool
	tool, err := registry.Get("duckduckgo_search")
	if err != nil {
		t.Errorf("Failed to get duckduckgo_search: %v", err)
	}
	if tool == nil {
		t.Error("duckduckgo_search tool should not be nil")
	}
}

func TestNewToolRegistry(t *testing.T) {
	// Create a new empty registry
	registry := NewToolRegistry()

	// Test that built-in tools are NOT registered
	if registry.Has("duckduckgo_search") {
		t.Error("NewToolRegistry should NOT have duckduckgo_search registered")
	}

	// Test that we get an error when trying to get the tool
	_, err := registry.Get("duckduckgo_search")
	if err == nil {
		t.Error("Should get error when getting non-existent tool")
	}
}

func TestRegisterBuiltinTools(t *testing.T) {
	// Create a new empty registry
	registry := NewToolRegistry()

	// Verify it's empty
	if registry.Has("duckduckgo_search") {
		t.Error("Registry should be empty initially")
	}

	// Register built-in tools
	RegisterBuiltinTools(registry)

	// Verify built-in tools are now registered
	if !registry.Has("duckduckgo_search") {
		t.Error("Registry should have duckduckgo_search after RegisterBuiltinTools")
	}
}

func TestGetBuiltinTools(t *testing.T) {
	tools := GetBuiltinTools()

	// Test that we have at least one built-in tool
	if len(tools) == 0 {
		t.Error("GetBuiltinTools should return at least one tool")
	}

	// Test that duckduckgo_search is in the list
	found := false
	for _, tool := range tools {
		if tool == "duckduckgo_search" {
			found = true
			break
		}
	}
	if !found {
		t.Error("GetBuiltinTools should include duckduckgo_search")
	}
}

func TestIsBuiltinTool(t *testing.T) {
	// Test that duckduckgo_search is a built-in tool
	if !IsBuiltinTool("duckduckgo_search") {
		t.Error("duckduckgo_search should be a built-in tool")
	}

	// Test that a non-existent tool is not a built-in tool
	if IsBuiltinTool("non_existent_tool") {
		t.Error("non_existent_tool should not be a built-in tool")
	}
}

func TestGetBuiltinTool(t *testing.T) {
	// Test that we can get a built-in tool instance
	tool := GetBuiltinTool("duckduckgo_search")
	if tool == nil {
		t.Error("GetBuiltinTool should return a tool instance for duckduckgo_search")
	}

	// Test that we get nil for non-existent tool
	tool = GetBuiltinTool("non_existent_tool")
	if tool != nil {
		t.Error("GetBuiltinTool should return nil for non-existent tool")
	}
}
