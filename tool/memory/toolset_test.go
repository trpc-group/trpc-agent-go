//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

package memory

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewMemoryToolSet(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Test creating memory tool set.
	toolSet := NewMemoryToolSet(service, appName, userID)

	if toolSet == nil {
		t.Fatal("Expected tool set to be created")
	}

	// Test that tool set implements ToolSet interface.
	var _ tool.ToolSet = toolSet

	// Test initial state.
	tools := toolSet.Tools(context.Background())
	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify tool names.
	expectedNames := []string{
		"memory_add",
		"memory_update",
		"memory_delete",
		"memory_clear",
		"memory_search",
		"memory_load",
	}

	for i, expectedName := range expectedNames {
		if i >= len(tools) {
			t.Fatalf("Expected tool %s at index %d", expectedName, i)
		}
		decl := tools[i].Declaration()
		if decl.Name != expectedName {
			t.Fatalf("Expected tool name %s, got %s", expectedName, decl.Name)
		}
	}

	// Test Close method.
	if err := toolSet.Close(); err != nil {
		t.Errorf("Failed to close tool set: %v", err)
	}
}

func TestMemoryToolSet_LazyInitialization(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	toolSet := NewMemoryToolSet(service, appName, userID)

	// First call should initialize tools.
	tools1 := toolSet.Tools(context.Background())
	if len(tools1) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools1))
	}

	// Second call should return cached tools.
	tools2 := toolSet.Tools(context.Background())
	if len(tools2) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools2))
	}

	// Verify tools are the same instances (cached).
	if &tools1[0] != &tools2[0] {
		t.Error("Expected tools to be cached, but got different instances")
	}
}

func TestMemoryToolSet_Close(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	toolSet := NewMemoryToolSet(service, appName, userID)

	// Initialize tools.
	tools := toolSet.Tools(context.Background())
	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Close tool set.
	if err := toolSet.Close(); err != nil {
		t.Errorf("Failed to close tool set: %v", err)
	}

	// After close, tools should be re-initialized.
	toolsAfterClose := toolSet.Tools(context.Background())
	if len(toolsAfterClose) != 6 {
		t.Fatalf("Expected 6 tools after close, got %d", len(toolsAfterClose))
	}
}
