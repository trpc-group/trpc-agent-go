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
)

func TestNewMemoryToolSet(t *testing.T) {
	service := inmemory.NewMemoryService()
	toolSet := NewMemoryToolSet(service)

	if toolSet == nil {
		t.Fatal("Expected non-nil tool set")
	}

	// Test that tools are lazily initialized
	if toolSet.tools != nil {
		t.Error("Expected tools to be nil before initialization")
	}

	// Test that tools are created when requested
	tools := toolSet.Tools(context.Background())
	if len(tools) != 6 {
		t.Errorf("Expected 6 tools, got %d", len(tools))
	}

	// Verify tool names
	expectedNames := []string{
		AddToolName,
		UpdateToolName,
		DeleteToolName,
		ClearToolName,
		SearchToolName,
		LoadToolName,
	}

	for _, expectedName := range expectedNames {
		found := false
		for _, tool := range tools {
			if tool.Declaration().Name == expectedName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected tool %s not found", expectedName)
		}
	}
}

func TestMemoryToolSet_LazyInitialization(t *testing.T) {
	service := inmemory.NewMemoryService()

	toolSet := NewMemoryToolSet(service)

	// First call should initialize tools.
	tools1 := toolSet.Tools(context.Background())
	if len(tools1) != 6 {
		t.Errorf("Expected 6 tools, got %d", len(tools1))
	}

	// Second call should return the same tools (no re-initialization).
	tools2 := toolSet.Tools(context.Background())
	if len(tools2) != 6 {
		t.Errorf("Expected 6 tools, got %d", len(tools2))
	}

	// Verify that the same tool instances are returned.
	if &tools1[0] != &tools2[0] {
		t.Error("Expected same tool instances to be returned")
	}
}

func TestMemoryToolSet_Close(t *testing.T) {
	service := inmemory.NewMemoryService()

	toolSet := NewMemoryToolSet(service)

	// Initialize tools.
	tools := toolSet.Tools(context.Background())
	if len(tools) != 6 {
		t.Errorf("Expected 6 tools, got %d", len(tools))
	}

	// Close the tool set.
	if err := toolSet.Close(); err != nil {
		t.Errorf("Failed to close tool set: %v", err)
	}

	// Verify that tools are cleared.
	if toolSet.tools != nil {
		t.Error("Expected tools to be nil after close")
	}

	if toolSet.initialized {
		t.Error("Expected initialized flag to be false after close")
	}
}
