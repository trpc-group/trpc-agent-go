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
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockToolSet is a mock implementation of tool.ToolSet for testing.
type mockToolSet struct {
	name  string
	tools []tool.Tool
}

func (m *mockToolSet) Tools(ctx context.Context) []tool.Tool {
	return m.tools
}

func (m *mockToolSet) Close() error {
	return nil
}

func (m *mockToolSet) Name() string {
	return m.name
}

func TestToolSetRegistry_Register(t *testing.T) {
	registry := NewToolSetRegistry()
	toolSet := &mockToolSet{name: "test_toolset"}

	// Test successful registration
	err := registry.Register("test", toolSet)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Test duplicate registration
	err = registry.Register("test", toolSet)
	if err == nil {
		t.Error("Expected error for duplicate registration")
	}

	// Test empty name
	err = registry.Register("", toolSet)
	if err == nil {
		t.Error("Expected error for empty name")
	}

	// Test nil toolset
	err = registry.Register("nil_test", nil)
	if err == nil {
		t.Error("Expected error for nil toolset")
	}
}

func TestToolSetRegistry_Get(t *testing.T) {
	registry := NewToolSetRegistry()
	toolSet := &mockToolSet{name: "test_toolset"}

	registry.MustRegister("test", toolSet)

	// Test successful get
	retrieved, err := registry.Get("test")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if retrieved != toolSet {
		t.Error("Retrieved toolset does not match registered toolset")
	}

	// Test get non-existent
	_, err = registry.Get("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent toolset")
	}
}

func TestToolSetRegistry_Has(t *testing.T) {
	registry := NewToolSetRegistry()
	toolSet := &mockToolSet{name: "test_toolset"}

	registry.MustRegister("test", toolSet)

	if !registry.Has("test") {
		t.Error("Expected Has to return true for registered toolset")
	}

	if registry.Has("nonexistent") {
		t.Error("Expected Has to return false for non-existent toolset")
	}
}

func TestToolSetRegistry_List(t *testing.T) {
	registry := NewToolSetRegistry()
	toolSet1 := &mockToolSet{name: "toolset1"}
	toolSet2 := &mockToolSet{name: "toolset2"}

	registry.MustRegister("test1", toolSet1)
	registry.MustRegister("test2", toolSet2)

	names := registry.List()
	if len(names) != 2 {
		t.Errorf("Expected 2 toolsets, got %d", len(names))
	}

	// Check that both names are present
	found1, found2 := false, false
	for _, name := range names {
		if name == "test1" {
			found1 = true
		}
		if name == "test2" {
			found2 = true
		}
	}

	if !found1 || !found2 {
		t.Error("Not all registered toolsets found in list")
	}
}

func TestToolSetRegistry_GetMultiple(t *testing.T) {
	registry := NewToolSetRegistry()
	toolSet1 := &mockToolSet{name: "toolset1"}
	toolSet2 := &mockToolSet{name: "toolset2"}

	registry.MustRegister("test1", toolSet1)
	registry.MustRegister("test2", toolSet2)

	// Test successful get multiple
	toolSets, err := registry.GetMultiple([]string{"test1", "test2"})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(toolSets) != 2 {
		t.Errorf("Expected 2 toolsets, got %d", len(toolSets))
	}

	// Test get multiple with non-existent
	_, err = registry.GetMultiple([]string{"test1", "nonexistent"})
	if err == nil {
		t.Error("Expected error for non-existent toolset")
	}
}

func TestToolSetRegistry_GetAll(t *testing.T) {
	registry := NewToolSetRegistry()
	toolSet1 := &mockToolSet{name: "toolset1"}
	toolSet2 := &mockToolSet{name: "toolset2"}

	registry.MustRegister("test1", toolSet1)
	registry.MustRegister("test2", toolSet2)

	all := registry.GetAll()
	if len(all) != 2 {
		t.Errorf("Expected 2 toolsets, got %d", len(all))
	}

	if all["test1"] != toolSet1 || all["test2"] != toolSet2 {
		t.Error("GetAll returned incorrect toolsets")
	}
}

func TestToolSetRegistry_Unregister(t *testing.T) {
	registry := NewToolSetRegistry()
	toolSet := &mockToolSet{name: "test_toolset"}

	registry.MustRegister("test", toolSet)

	if !registry.Has("test") {
		t.Error("Toolset should be registered")
	}

	registry.Unregister("test")

	if registry.Has("test") {
		t.Error("Toolset should be unregistered")
	}
}

func TestToolSetRegistry_Clear(t *testing.T) {
	registry := NewToolSetRegistry()
	toolSet1 := &mockToolSet{name: "toolset1"}
	toolSet2 := &mockToolSet{name: "toolset2"}

	registry.MustRegister("test1", toolSet1)
	registry.MustRegister("test2", toolSet2)

	if len(registry.List()) != 2 {
		t.Error("Expected 2 toolsets before clear")
	}

	registry.Clear()

	if len(registry.List()) != 0 {
		t.Error("Expected 0 toolsets after clear")
	}
}

func TestDefaultToolSetRegistry(t *testing.T) {
	// Test that DefaultToolSetRegistry exists
	if DefaultToolSetRegistry == nil {
		t.Fatal("DefaultToolSetRegistry should not be nil")
	}

	// Note: Built-in toolsets are registered in the builtin package's init()
	// We can't test that here due to import cycles
	// See builtin/tools_builtin_test.go for tests of auto-registration
}
