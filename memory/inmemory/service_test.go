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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewMemoryService(t *testing.T) {
	service := NewMemoryService()
	require.NotNil(t, service, "NewMemoryService should not return nil")
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
	require.NoError(t, service.AddMemory(ctx, userKey, memoryStr, topics), "AddMemory failed")

	// Test reading memories.
	memories, err := service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")

	assert.Len(t, memories, 1, "Expected 1 memory")
	assert.Equal(t, memoryStr, memories[0].Memory.Memory, "Expected memory content")
	assert.Len(t, memories[0].Memory.Topics, 2, "Expected 2 topics")
}

func TestMemoryService_UpdateMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add a memory first.
	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", nil), "AddMemory failed")

	// Read memories to get the ID.
	memories, err := service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err, "ReadMemories failed")

	memoryKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memories[0].ID,
	}

	// Update the memory.
	require.NoError(t, service.UpdateMemory(ctx, memoryKey, "updated memory", []string{"updated"}), "UpdateMemory failed")

	// Read memories again to verify the update.
	memories, err = service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err, "ReadMemories failed")

	assert.Equal(t, "updated memory", memories[0].Memory.Memory, "Expected updated memory content")
}

func TestMemoryService_DeleteMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add a memory first.
	require.NoError(t, service.AddMemory(ctx, userKey, "test memory", nil), "AddMemory failed")

	// Read memories to get the ID.
	memories, err := service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err, "ReadMemories failed")

	memoryKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memories[0].ID,
	}

	// Delete the memory.
	require.NoError(t, service.DeleteMemory(ctx, memoryKey), "DeleteMemory failed")

	// Read memories again to verify the deletion.
	memories, err = service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")

	assert.Len(t, memories, 0, "Expected 0 memories after deletion")
}

func TestMemoryService_ClearMemories(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add multiple memories.
	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", nil), "AddMemory failed")
	require.NoError(t, service.AddMemory(ctx, userKey, "second memory", nil), "AddMemory failed")

	// Verify memories were added.
	memories, err := service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 2, "Expected 2 memories")

	// Clear all memories.
	require.NoError(t, service.ClearMemories(ctx, userKey), "ClearMemories failed")

	// Verify memories were cleared.
	memories, err = service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 0, "Expected 0 memories after clearing")
}

func TestMemoryService_SearchMemories(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add memories with different content.
	require.NoError(t, service.AddMemory(ctx, userKey, "User likes coffee", []string{"preferences"}), "AddMemory failed")
	require.NoError(t, service.AddMemory(ctx, userKey, "User works as a developer", []string{"work"}), "AddMemory failed")

	// Search for coffee-related memories.
	results, err := service.SearchMemories(ctx, userKey, "coffee")
	require.NoError(t, err, "SearchMemories failed")
	assert.Len(t, results, 1, "Expected 1 result for 'coffee' search")

	// Search for work-related memories.
	results, err = service.SearchMemories(ctx, userKey, "developer")
	require.NoError(t, err, "SearchMemories failed")
	assert.Len(t, results, 1, "Expected 1 result for 'developer' search")

	// Search for non-existent content.
	results, err = service.SearchMemories(ctx, userKey, "nonexistent")
	require.NoError(t, err, "SearchMemories failed")
	assert.Len(t, results, 0, "Expected 0 results for 'nonexistent' search")
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
		require.NoError(t, service.AddMemory(ctx, userKey, fmt.Sprintf("memory %d", i), nil), "AddMemory failed")
	}

	// Test reading with limit.
	memories, err := service.ReadMemories(ctx, userKey, 3)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 3, "Expected 3 memories with limit")

	// Test reading without limit.
	memories, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 5, "Expected 5 memories without limit")
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
		assert.NoError(t, err, "Concurrency test error")
	}

	// Verify all memories were added.
	memories, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err, "ReadMemories failed")

	expectedCount := numGoroutines * memoriesPerGoroutine
	assert.Len(t, memories, expectedCount, "Expected memories count")
}

func TestMemoryService_Tools(t *testing.T) {
	// New design has default tools enabled by default.
	service := NewMemoryService()
	tools := service.Tools()
	// Should have 4 default enabled tools: add, update, search, load.
	assert.Len(t, tools, 4, "Expected 4 default tools")

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
	assert.True(t, toolNames[memory.AddToolName], "Expected enabled tools to be present")
	assert.True(t, toolNames[memory.SearchToolName], "Expected enabled tools to be present")
	// Should have 4 tools total (2 custom + 2 default enabled).
	assert.Len(t, tools, 4, "Expected 4 tools (2 custom + 2 default enabled)")

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
	assert.True(t, found, "Expected custom tool to be returned for %s", memory.AddToolName)

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
	assert.False(t, toolNames[memory.AddToolName], "Expected %s to be disabled", memory.AddToolName)
	assert.True(t, toolNames[memory.SearchToolName], "Expected %s to be enabled", memory.SearchToolName)

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
	assert.True(t, found, "Expected tool built by builder to be present")

	// Test disabling all tools.
	service = NewMemoryService(
		WithToolEnabled(memory.AddToolName, false),
		WithToolEnabled(memory.UpdateToolName, false),
		WithToolEnabled(memory.SearchToolName, false),
		WithToolEnabled(memory.LoadToolName, false),
	)
	tools = service.Tools()
	assert.Len(t, tools, 0, "Expected no tools when all disabled")
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
	assert.True(t, toolNames[memory.AddToolName], "Expected valid tool name %s to be registered", memory.AddToolName)

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
	assert.False(t, toolNames["invalid_tool_name"], "Expected invalid tool name to be ignored")

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
	assert.True(t, toolNames[memory.AddToolName], "Expected valid tool name %s to be registered", memory.AddToolName)
	assert.False(t, toolNames["invalid_tool"], "Expected invalid tool name to be ignored")
	assert.False(t, toolNames["invalid_enable"], "Expected invalid tool name in WithToolEnabled to be ignored")

	// Test that nil creator is ignored.
	service = NewMemoryService(
		WithCustomTool(memory.UpdateToolName, nil),
	)
	tools = service.Tools()
	toolNames = map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	// UpdateToolName should still be present from defaults, not from nil creator.
	assert.True(t, toolNames[memory.UpdateToolName], "Expected default tool to still be available")
}

func TestWithMemoryLimit(t *testing.T) {
	service := NewMemoryService(WithMemoryLimit(2))
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add memories up to the limit.
	require.NoError(t, service.AddMemory(ctx, userKey, "memory 1", nil), "AddMemory failed")
	require.NoError(t, service.AddMemory(ctx, userKey, "memory 2", nil), "AddMemory failed")

	// Try to add one more memory beyond the limit.
	err := service.AddMemory(ctx, userKey, "memory 3", nil)
	require.Error(t, err, "Expected error when exceeding memory limit")

	// Verify the error message mentions the limit.
	assert.Contains(t, err.Error(), "memory limit exceeded", "Expected error to mention memory limit")
}

func TestAddMemory_InvalidKey(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with empty app name.
	err := service.AddMemory(ctx, memory.UserKey{AppName: "", UserID: "user"}, "test", nil)
	require.Error(t, err, "Expected error with empty app name")

	// Test with empty user id.
	err = service.AddMemory(ctx, memory.UserKey{AppName: "app", UserID: ""}, "test", nil)
	require.Error(t, err, "Expected error with empty user id")
}

func TestUpdateMemory_Errors(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with invalid key.
	err := service.UpdateMemory(ctx, memory.Key{AppName: "", UserID: "user", MemoryID: "id"}, "test", nil)
	require.Error(t, err, "Expected error with empty app name")

	// Test with non-existent user.
	err = service.UpdateMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: "id"}, "test", nil)
	require.Error(t, err, "Expected error with non-existent user")

	// Add a memory.
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "test memory", nil), "AddMemory failed")

	// Test with non-existent memory id.
	err = service.UpdateMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: "non-existent"}, "test", nil)
	require.Error(t, err, "Expected error with non-existent memory id")
}

func TestDeleteMemory_Errors(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with invalid key.
	err := service.DeleteMemory(ctx, memory.Key{AppName: "", UserID: "user", MemoryID: "id"})
	require.Error(t, err, "Expected error with empty app name")

	// Test with non-existent user.
	err = service.DeleteMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: "id"})
	require.Error(t, err, "Expected error with non-existent user")

	// Add a memory.
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "test memory", nil), "AddMemory failed")

	// Test with non-existent memory id.
	err = service.DeleteMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: "non-existent"})
	require.Error(t, err, "Expected error with non-existent memory id")
}

func TestClearMemories_InvalidKey(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with empty app name.
	err := service.ClearMemories(ctx, memory.UserKey{AppName: "", UserID: "user"})
	require.Error(t, err, "Expected error with empty app name")

	// Test with empty user id.
	err = service.ClearMemories(ctx, memory.UserKey{AppName: "app", UserID: ""})
	require.Error(t, err, "Expected error with empty user id")
}

func TestReadMemories_InvalidKey(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with empty app name.
	_, err := service.ReadMemories(ctx, memory.UserKey{AppName: "", UserID: "user"}, 10)
	require.Error(t, err, "Expected error with empty app name")

	// Test with empty user id.
	_, err = service.ReadMemories(ctx, memory.UserKey{AppName: "app", UserID: ""}, 10)
	require.Error(t, err, "Expected error with empty user id")
}

func TestSearchMemories_InvalidKey(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with empty app name.
	_, err := service.SearchMemories(ctx, memory.UserKey{AppName: "", UserID: "user"}, "query")
	require.Error(t, err, "Expected error with empty app name")

	// Test with empty user id.
	_, err = service.SearchMemories(ctx, memory.UserKey{AppName: "app", UserID: ""}, "query")
	require.Error(t, err, "Expected error with empty user id")
}

func TestReadMemories_NilUser(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "non-existent-user",
	}

	// Reading memories for non-existent user should return empty slice.
	memories, err := service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 0, "Expected 0 memories for non-existent user")
}

func TestSearchMemories_NilUser(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "non-existent-user",
	}

	// Searching memories for non-existent user should return empty slice.
	results, err := service.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err, "SearchMemories failed")
	assert.Len(t, results, 0, "Expected 0 results for non-existent user")
}
