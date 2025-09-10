//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides in-memory memory service implementation.
package inmemory

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewMemoryService(t *testing.T) {
	service := NewMemoryService()
	if service == nil {
		t.Fatal("NewMemoryService should not return nil")
	}
}

func TestMemoryService_AddMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}
	memoryStr := "Test memory content"
	topics := []string{"test", "memory"}

	// Test adding memory.
	err := service.AddMemory(ctx, userKey, memoryStr, topics)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Test reading memories.
	memories, err := service.ReadMemories(ctx, userKey, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 1 {
		t.Fatalf("Expected 1 memory, got %d", len(memories))
	}

	if memories[0].Memory.Memory != memoryStr {
		t.Fatalf("Expected memory content %s, got %s", memoryStr, memories[0].Memory.Memory)
	}

	if len(memories[0].Memory.Topics) != 2 {
		t.Fatalf("Expected 2 topics, got %d", len(memories[0].Memory.Topics))
	}
}

func TestMemoryService_UpdateMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add a memory first.
	err := service.AddMemory(ctx, userKey, "first memory", nil)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Read memories to get the ID.
	memories, err := service.ReadMemories(ctx, userKey, 1)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	memoryKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memories[0].ID,
	}

	// Update the memory.
	err = service.UpdateMemory(ctx, memoryKey, "updated memory", []string{"updated"})
	if err != nil {
		t.Fatalf("UpdateMemory failed: %v", err)
	}

	// Read memories again to verify the update.
	memories, err = service.ReadMemories(ctx, userKey, 1)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if memories[0].Memory.Memory != "updated memory" {
		t.Fatalf("Expected updated memory content, got %s", memories[0].Memory.Memory)
	}
}

func TestMemoryService_DeleteMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add a memory first.
	err := service.AddMemory(ctx, userKey, "test memory", nil)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Read memories to get the ID.
	memories, err := service.ReadMemories(ctx, userKey, 1)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	memoryKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memories[0].ID,
	}

	// Delete the memory.
	err = service.DeleteMemory(ctx, memoryKey)
	if err != nil {
		t.Fatalf("DeleteMemory failed: %v", err)
	}

	// Read memories again to verify the deletion.
	memories, err = service.ReadMemories(ctx, userKey, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 0 {
		t.Fatalf("Expected 0 memories after deletion, got %d", len(memories))
	}
}

func TestMemoryService_ClearMemories(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add multiple memories.
	err := service.AddMemory(ctx, userKey, "first memory", nil)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	err = service.AddMemory(ctx, userKey, "second memory", nil)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Verify memories were added.
	memories, err := service.ReadMemories(ctx, userKey, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 2 {
		t.Fatalf("Expected 2 memories, got %d", len(memories))
	}

	// Clear all memories.
	err = service.ClearMemories(ctx, userKey)
	if err != nil {
		t.Fatalf("ClearMemories failed: %v", err)
	}

	// Verify memories were cleared.
	memories, err = service.ReadMemories(ctx, userKey, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 0 {
		t.Fatalf("Expected 0 memories after clearing, got %d", len(memories))
	}
}

func TestMemoryService_SearchMemories(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add memories with different content.
	err := service.AddMemory(ctx, userKey, "User likes coffee", []string{"preferences"})
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	err = service.AddMemory(ctx, userKey, "User works as a developer", []string{"work"})
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Search for coffee-related memories.
	results, err := service.SearchMemories(ctx, userKey, "coffee")
	if err != nil {
		t.Fatalf("SearchMemories failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result for 'coffee' search, got %d", len(results))
	}

	// Search for work-related memories.
	results, err = service.SearchMemories(ctx, userKey, "developer")
	if err != nil {
		t.Fatalf("SearchMemories failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result for 'developer' search, got %d", len(results))
	}

	// Search for non-existent content.
	results, err = service.SearchMemories(ctx, userKey, "nonexistent")
	if err != nil {
		t.Fatalf("SearchMemories failed: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("Expected 0 results for 'nonexistent' search, got %d", len(results))
	}
}

func TestMemoryService_ReadMemoriesWithLimit(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add multiple memories.
	for i := 0; i < 5; i++ {
		err := service.AddMemory(ctx, userKey, fmt.Sprintf("memory %d", i), nil)
		if err != nil {
			t.Fatalf("AddMemory failed: %v", err)
		}
	}

	// Test reading with limit.
	memories, err := service.ReadMemories(ctx, userKey, 3)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 3 {
		t.Fatalf("Expected 3 memories with limit, got %d", len(memories))
	}

	// Test reading without limit.
	memories, err = service.ReadMemories(ctx, userKey, 0)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 5 {
		t.Fatalf("Expected 5 memories without limit, got %d", len(memories))
	}
}

func TestMemoryService_Concurrency(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Test concurrent access.
	const numGoroutines = 10
	const memoriesPerGoroutine = 5

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*memoriesPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < memoriesPerGoroutine; j++ {
				memoryStr := fmt.Sprintf("memory from goroutine %d, item %d", id, j)
				err := service.AddMemory(ctx, userKey, memoryStr, nil)
				if err != nil {
					errChan <- fmt.Errorf("goroutine %d failed to add memory %d: %v", id, j, err)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors.
	for err := range errChan {
		t.Errorf("Concurrency test error: %v", err)
	}

	// Verify all memories were added.
	memories, err := service.ReadMemories(ctx, userKey, 0)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	expectedCount := numGoroutines * memoriesPerGoroutine
	if len(memories) != expectedCount {
		t.Fatalf("Expected %d memories, got %d", expectedCount, len(memories))
	}
}

func TestMemoryService_Tools(t *testing.T) {
	// New design has default tools enabled by default.
	service := NewMemoryService()
	tools := service.Tools()
	// Should have 4 default enabled tools: add, update, search, load.
	if len(tools) != 4 {
		t.Errorf("expected 4 default tools, got %d", len(tools))
	}

	// Register some tools.
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		}),
		WithCustomTool(memory.SearchToolName, func() tool.Tool {
			return &mockTool{name: memory.SearchToolName}
		}),
	)
	tools = service.Tools()
	toolNames := map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	if !toolNames[memory.AddToolName] || !toolNames[memory.SearchToolName] {
		t.Errorf("expected enabled tools to be present")
	}
	// Should have 4 tools total (2 custom + 2 default enabled).
	if len(tools) != 4 {
		t.Errorf("expected 4 tools (2 custom + 2 default enabled), got %d", len(tools))
	}

	// Custom tool should be returned when provided.
	custom := &mockTool{name: memory.AddToolName}
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return custom
		}),
	)
	tools = service.Tools()
	found := false
	for _, tool := range tools {
		if tool.Declaration().Name == memory.AddToolName {
			if tool == custom {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected custom tool to be returned for %s", memory.AddToolName)
	}

	// Test tool enable/disable functionality.
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		}),
		WithCustomTool(memory.SearchToolName, func() tool.Tool {
			return &mockTool{name: memory.SearchToolName}
		}),
		WithToolEnabled(memory.AddToolName, false),
	)
	tools = service.Tools()
	toolNames = map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	if toolNames[memory.AddToolName] {
		t.Errorf("expected %s to be disabled", memory.AddToolName)
	}
	if !toolNames[memory.SearchToolName] {
		t.Errorf("expected %s to be enabled", memory.SearchToolName)
	}

	// Test tool builder functionality.
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName + "_built"}
		}),
	)
	tools = service.Tools()
	found = false
	for _, tool := range tools {
		if tool.Declaration().Name == memory.AddToolName+"_built" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tool built by builder to be present")
	}

	// Test disabling all tools.
	service = NewMemoryService(
		WithToolEnabled(memory.AddToolName, false),
		WithToolEnabled(memory.UpdateToolName, false),
		WithToolEnabled(memory.SearchToolName, false),
		WithToolEnabled(memory.LoadToolName, false),
	)
	tools = service.Tools()
	if len(tools) != 0 {
		t.Errorf("expected no tools when all disabled, got %d", len(tools))
	}
}

// mockTool implements tool.Tool for testing.
type mockTool struct{ name string }

func (m *mockTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: m.name} }

func TestMemoryService_ToolNameValidation(t *testing.T) {
	// Test that valid tool names work correctly.
	service := NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		}),
		WithToolEnabled(memory.SearchToolName, true),
	)
	tools := service.Tools()
	toolNames := map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	if !toolNames[memory.AddToolName] {
		t.Errorf("expected valid tool name %s to be registered", memory.AddToolName)
	}

	// Test that invalid tool names are ignored.
	service = NewMemoryService(
		WithCustomTool("invalid_tool_name", func() tool.Tool {
			return &mockTool{name: "invalid_tool_name"}
		}),
		WithToolEnabled("another_invalid_name", true),
	)
	tools = service.Tools()
	toolNames = map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	if toolNames["invalid_tool_name"] {
		t.Errorf("expected invalid tool name to be ignored")
	}

	// Test that mixed valid and invalid tool names work correctly.
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		}),
		WithCustomTool("invalid_tool", func() tool.Tool {
			return &mockTool{name: "invalid_tool"}
		}),
		WithToolEnabled(memory.SearchToolName, true),
		WithToolEnabled("invalid_enable", true),
	)
	tools = service.Tools()
	toolNames = map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	if !toolNames[memory.AddToolName] {
		t.Errorf("expected valid tool name %s to be registered", memory.AddToolName)
	}
	if toolNames["invalid_tool"] {
		t.Errorf("expected invalid tool name to be ignored")
	}
	if toolNames["invalid_enable"] {
		t.Errorf("expected invalid tool name in WithToolEnabled to be ignored")
	}
}
