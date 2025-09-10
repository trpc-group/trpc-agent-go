//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tool provides memory-related tools for the agent system.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	memorypkg "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockMemoryService is a mock implementation of memory.Service for testing.
type mockMemoryService struct {
	memories map[string]*memorypkg.Entry
	counter  int
}

func newMockMemoryService() *mockMemoryService {
	return &mockMemoryService{
		memories: make(map[string]*memorypkg.Entry),
		counter:  0,
	}
}

func (m *mockMemoryService) AddMemory(ctx context.Context, userKey memorypkg.UserKey, memory string, topics []string) error {
	m.counter++
	memoryID := fmt.Sprintf("memory-%d", m.counter)
	key := userKey.AppName + ":" + userKey.UserID + ":" + memoryID
	m.memories[key] = &memorypkg.Entry{
		ID:        memoryID,
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    &memorypkg.Memory{Memory: memory, Topics: topics},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return nil
}

func (m *mockMemoryService) UpdateMemory(ctx context.Context, memoryKey memorypkg.Key, memory string, topics []string) error {
	key := memoryKey.AppName + ":" + memoryKey.UserID + ":" + memoryKey.MemoryID
	if entry, exists := m.memories[key]; exists {
		entry.Memory.Memory = memory
		entry.Memory.Topics = topics
		entry.UpdatedAt = time.Now()
		return nil
	}
	return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
}

func (m *mockMemoryService) DeleteMemory(ctx context.Context, memoryKey memorypkg.Key) error {
	key := memoryKey.AppName + ":" + memoryKey.UserID + ":" + memoryKey.MemoryID
	if _, exists := m.memories[key]; exists {
		delete(m.memories, key)
		return nil
	}
	return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
}

func (m *mockMemoryService) ClearMemories(ctx context.Context, userKey memorypkg.UserKey) error {
	prefix := userKey.AppName + ":" + userKey.UserID + ":"
	for key := range m.memories {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(m.memories, key)
		}
	}
	return nil
}

func (m *mockMemoryService) ReadMemories(ctx context.Context, userKey memorypkg.UserKey, limit int) ([]*memorypkg.Entry, error) {
	var results []*memorypkg.Entry
	prefix := userKey.AppName + ":" + userKey.UserID + ":"

	for key, entry := range m.memories {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, entry)
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

func (m *mockMemoryService) SearchMemories(ctx context.Context, userKey memorypkg.UserKey, query string) ([]*memorypkg.Entry, error) {
	var results []*memorypkg.Entry
	prefix := userKey.AppName + ":" + userKey.UserID + ":"

	for key, entry := range m.memories {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			// Simple search - check if query is in memory content.
			if len(query) > 0 && len(entry.Memory.Memory) > 0 {
				// Simple contains check for testing.
				if strings.Contains(entry.Memory.Memory, query) {
					results = append(results, entry)
				}
			}
		}
	}
	return results, nil
}

func (m *mockMemoryService) Tools() []tool.Tool {
	return []tool.Tool{}
}

func (m *mockMemoryService) BuildInstruction(enabledTools []string, defaultPrompt string) (string, bool) {
	return "", false
}

// createMockContext creates a mock context with session information.
func createMockContext(appName, userID string) context.Context {
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   appName,
		UserID:    userID,
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}

	return agent.NewInvocationContext(context.Background(), mockInvocation)
}

func TestMemoryTool_AddMemory(t *testing.T) {
	service := newMockMemoryService()
	tool := NewAddTool(service)

	ctx := createMockContext("test-app", "test-user")

	// Test adding a memory with topics.
	args := map[string]any{
		"memory": "User's name is John Doe",
		"topics": []string{"personal"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*AddMemoryResponse)
	require.True(t, ok, "Expected *AddMemoryResponse, got %T", result)

	assert.Equal(t, "User's name is John Doe", response.Memory, "Expected memory 'User's name is John Doe', got '%s'", response.Memory)
	assert.Len(t, response.Topics, 1, "Expected 1 topic, got %d", len(response.Topics))
	assert.Equal(t, "personal", response.Topics[0], "Expected topic 'personal', got '%s'", response.Topics[0])

	// Verify memory was added.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	memories, err := service.ReadMemories(context.Background(), userKey, 10)
	require.NoError(t, err, "Failed to read memories")

	assert.Len(t, memories, 1, "Expected 1 memory, got %d", len(memories))
	assert.Equal(t, "User's name is John Doe", memories[0].Memory.Memory, "Expected memory 'User's name is John Doe', got '%s'", memories[0].Memory.Memory)
}

func TestMemoryTool_AddMemory_WithoutTopics(t *testing.T) {
	service := newMockMemoryService()
	tool := NewAddTool(service)

	ctx := createMockContext("test-app", "test-user")

	// Test adding a memory without topics.
	args := map[string]any{
		"memory": "User likes coffee",
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*AddMemoryResponse)
	require.True(t, ok, "Expected *AddMemoryResponse, got %T", result)

	assert.Equal(t, "User likes coffee", response.Memory, "Expected memory 'User likes coffee', got '%s'", response.Memory)
	assert.NotNil(t, response.Topics, "Expected topics to be empty slice, got nil")
	assert.Len(t, response.Topics, 0, "Expected 0 topics, got %d", len(response.Topics))
}

func TestMemoryTool_Declaration(t *testing.T) {
	service := newMockMemoryService()
	tool := NewAddTool(service)

	decl := tool.Declaration()
	require.NotNil(t, decl, "Expected non-nil declaration")
	assert.Equal(t, "memory_add", decl.Name, "Expected name 'memory_add', got '%s'", decl.Name)
	assert.NotEmpty(t, decl.Description, "Expected non-empty description")
	assert.NotNil(t, decl.InputSchema, "Expected non-nil input schema")
}

func TestMemoryTool_SearchMemory(t *testing.T) {
	service := newMockMemoryService()

	// Add some test memories first.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	err := service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	require.NoError(t, err, "Failed to add first memory")

	err = service.AddMemory(context.Background(), userKey, "User works as a developer", []string{"work"})
	require.NoError(t, err, "Failed to add second memory")

	// Verify memories were added.
	memories, err := service.ReadMemories(context.Background(), userKey, 10)
	require.NoError(t, err, "Failed to read memories")
	assert.Len(t, memories, 2, "Expected 2 memories, got %d", len(memories))

	tool := NewSearchTool(service)

	ctx := createMockContext("test-app", "test-user")

	// Test searching memories.
	args := map[string]any{
		"query": "coffee",
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*SearchMemoryResponse)
	require.True(t, ok, "Expected *SearchMemoryResponse, got %T", result)

	assert.Equal(t, "coffee", response.Query, "Expected query 'coffee', got '%s'", response.Query)
	assert.Equal(t, 1, response.Count, "Expected 1 result, got %d", response.Count)
	assert.Len(t, response.Results, 1, "Expected 1 result, got %d", len(response.Results))
	assert.Equal(t, "User likes coffee", response.Results[0].Memory, "Expected memory 'User likes coffee', got '%s'", response.Results[0].Memory)
}

func TestMemoryTool_LoadMemory(t *testing.T) {
	service := newMockMemoryService()

	// Add some test memories first.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	service.AddMemory(context.Background(), userKey, "User works as a developer", []string{"work"})

	tool := NewLoadTool(service)

	ctx := createMockContext("test-app", "test-user")

	// Test loading memories with limit.
	args := map[string]any{
		"limit": 1,
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*LoadMemoryResponse)
	require.True(t, ok, "Expected *LoadMemoryResponse, got %T", result)

	assert.Equal(t, 1, response.Limit, "Expected limit 1, got %d", response.Limit)
	assert.Equal(t, 1, response.Count, "Expected 1 result, got %d", response.Count)
	assert.Len(t, response.Results, 1, "Expected 1 result, got %d", len(response.Results))
}

func TestMemoryTool_UpdateMemory(t *testing.T) {
	service := newMockMemoryService()

	// Add a test memory first.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	err := service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	require.NoError(t, err, "Failed to add memory")

	// Get the memory ID.
	memories, err := service.ReadMemories(context.Background(), userKey, 1)
	require.NoError(t, err, "Failed to read memories")
	require.Len(t, memories, 1, "Expected 1 memory")
	memoryID := memories[0].ID

	tool := NewUpdateTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test updating memory.
	args := map[string]any{
		"memory_id": memoryID,
		"memory":    "User loves coffee and tea",
		"topics":    []string{"preferences", "beverages"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*UpdateMemoryResponse)
	require.True(t, ok, "Expected *UpdateMemoryResponse, got %T", result)

	assert.Equal(t, memoryID, response.MemoryID, "Expected memory ID to match")
	assert.Equal(t, "User loves coffee and tea", response.Memory, "Expected updated memory content")
	assert.Len(t, response.Topics, 2, "Expected 2 topics")
	assert.Contains(t, response.Topics, "preferences", "Expected 'preferences' topic")
	assert.Contains(t, response.Topics, "beverages", "Expected 'beverages' topic")

	// Verify memory was updated.
	updatedMemories, err := service.ReadMemories(context.Background(), userKey, 1)
	require.NoError(t, err, "Failed to read updated memories")
	assert.Len(t, updatedMemories, 1, "Expected 1 memory")
	assert.Equal(t, "User loves coffee and tea", updatedMemories[0].Memory.Memory, "Expected updated memory content")
}

func TestMemoryTool_UpdateMemory_WithoutTopics(t *testing.T) {
	service := newMockMemoryService()

	// Add a test memory first.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	err := service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	require.NoError(t, err, "Failed to add memory")

	// Get the memory ID.
	memories, err := service.ReadMemories(context.Background(), userKey, 1)
	require.NoError(t, err, "Failed to read memories")
	require.Len(t, memories, 1, "Expected 1 memory")
	memoryID := memories[0].ID

	tool := NewUpdateTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test updating memory without topics.
	args := map[string]any{
		"memory_id": memoryID,
		"memory":    "User loves coffee and tea",
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*UpdateMemoryResponse)
	require.True(t, ok, "Expected *UpdateMemoryResponse, got %T", result)

	assert.Equal(t, memoryID, response.MemoryID, "Expected memory ID to match")
	assert.Equal(t, "User loves coffee and tea", response.Memory, "Expected updated memory content")
	assert.NotNil(t, response.Topics, "Expected topics to be empty slice, got nil")
	assert.Len(t, response.Topics, 0, "Expected 0 topics")
}

func TestMemoryTool_UpdateMemory_InvalidID(t *testing.T) {
	service := newMockMemoryService()
	tool := NewUpdateTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test updating with invalid memory ID.
	args := map[string]any{
		"memory_id": "invalid-id",
		"memory":    "Updated content",
		"topics":    []string{"test"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.Error(t, err, "Expected error for invalid memory ID")
	assert.Nil(t, result, "Expected nil result on error")
	assert.Contains(t, err.Error(), "failed to update memory", "Expected specific error message")
}

func TestMemoryTool_UpdateMemory_MissingMemoryID(t *testing.T) {
	service := newMockMemoryService()
	tool := NewUpdateTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test updating without memory ID.
	args := map[string]any{
		"memory": "Updated content",
		"topics": []string{"test"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.Error(t, err, "Expected error for missing memory ID")
	assert.Nil(t, result, "Expected nil result on error")
	assert.Contains(t, err.Error(), "memory ID is required", "Expected specific error message")
}

func TestMemoryTool_UpdateMemory_MissingMemory(t *testing.T) {
	service := newMockMemoryService()
	tool := NewUpdateTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test updating without memory content.
	args := map[string]any{
		"memory_id": "test-id",
		"topics":    []string{"test"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.Error(t, err, "Expected error for missing memory content")
	assert.Nil(t, result, "Expected nil result on error")
	assert.Contains(t, err.Error(), "memory content is required", "Expected specific error message")
}

func TestMemoryTool_DeleteMemory(t *testing.T) {
	service := newMockMemoryService()

	// Add a test memory first.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	err := service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	require.NoError(t, err, "Failed to add memory")

	// Get the memory ID.
	memories, err := service.ReadMemories(context.Background(), userKey, 1)
	require.NoError(t, err, "Failed to read memories")
	require.Len(t, memories, 1, "Expected 1 memory")
	memoryID := memories[0].ID

	tool := NewDeleteTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test deleting memory.
	args := map[string]any{
		"memory_id": memoryID,
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*DeleteMemoryResponse)
	require.True(t, ok, "Expected *DeleteMemoryResponse, got %T", result)

	assert.Equal(t, memoryID, response.MemoryID, "Expected memory ID to match")
	assert.Equal(t, "Memory deleted successfully", response.Message, "Expected success message")

	// Verify memory was deleted.
	deletedMemories, err := service.ReadMemories(context.Background(), userKey, 1)
	require.NoError(t, err, "Failed to read memories after deletion")
	assert.Len(t, deletedMemories, 0, "Expected 0 memories after deletion")
}

func TestMemoryTool_DeleteMemory_InvalidID(t *testing.T) {
	service := newMockMemoryService()
	tool := NewDeleteTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test deleting with invalid memory ID.
	args := map[string]any{
		"memory_id": "invalid-id",
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.Error(t, err, "Expected error for invalid memory ID")
	assert.Nil(t, result, "Expected nil result on error")
	assert.Contains(t, err.Error(), "failed to delete memory", "Expected specific error message")
}

func TestMemoryTool_DeleteMemory_MissingMemoryID(t *testing.T) {
	service := newMockMemoryService()
	tool := NewDeleteTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test deleting without memory ID.
	args := map[string]any{}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.Error(t, err, "Expected error for missing memory ID")
	assert.Nil(t, result, "Expected nil result on error")
	assert.Contains(t, err.Error(), "memory ID is required", "Expected specific error message")
}

func TestMemoryTool_ClearMemories(t *testing.T) {
	service := newMockMemoryService()

	// Add some test memories first.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	err := service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	require.NoError(t, err, "Failed to add first memory")
	err = service.AddMemory(context.Background(), userKey, "User works as a developer", []string{"work"})
	require.NoError(t, err, "Failed to add second memory")

	// Verify memories were added.
	memories, err := service.ReadMemories(context.Background(), userKey, 10)
	require.NoError(t, err, "Failed to read memories")
	assert.Len(t, memories, 2, "Expected 2 memories before clearing")

	tool := NewClearTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test clearing memories.
	args := map[string]any{}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*ClearMemoryResponse)
	require.True(t, ok, "Expected *ClearMemoryResponse, got %T", result)

	assert.Equal(t, "All memories cleared successfully", response.Message, "Expected success message")

	// Verify memories were cleared.
	clearedMemories, err := service.ReadMemories(context.Background(), userKey, 10)
	require.NoError(t, err, "Failed to read memories after clearing")
	assert.Len(t, clearedMemories, 0, "Expected 0 memories after clearing")
}

func TestMemoryTool_AddMemory_MissingMemory(t *testing.T) {
	service := newMockMemoryService()
	tool := NewAddTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test adding memory without content.
	args := map[string]any{
		"topics": []string{"test"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.Error(t, err, "Expected error for missing memory content")
	assert.Nil(t, result, "Expected nil result on error")
	assert.Contains(t, err.Error(), "memory content is required", "Expected specific error message")
}

func TestMemoryTool_SearchMemory_MissingQuery(t *testing.T) {
	service := newMockMemoryService()
	tool := NewSearchTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test searching without query.
	args := map[string]any{}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.Error(t, err, "Expected error for missing query")
	assert.Nil(t, result, "Expected nil result on error")
	assert.Contains(t, err.Error(), "query is required", "Expected specific error message")
}

func TestMemoryTool_LoadMemory_DefaultLimit(t *testing.T) {
	service := newMockMemoryService()

	// Add some test memories first.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	service.AddMemory(context.Background(), userKey, "User works as a developer", []string{"work"})

	tool := NewLoadTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test loading memories without specifying limit (should use default).
	args := map[string]any{}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*LoadMemoryResponse)
	require.True(t, ok, "Expected *LoadMemoryResponse, got %T", result)

	assert.Equal(t, 10, response.Limit, "Expected default limit 10, got %d", response.Limit)
	assert.Equal(t, 2, response.Count, "Expected 2 results, got %d", response.Count)
	assert.Len(t, response.Results, 2, "Expected 2 results, got %d", len(response.Results))
}

func TestMemoryTool_LoadMemory_ZeroLimit(t *testing.T) {
	service := newMockMemoryService()

	// Add some test memories first.
	userKey := memorypkg.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})

	tool := NewLoadTool(service)
	ctx := createMockContext("test-app", "test-user")

	// Test loading memories with zero limit (should use default).
	args := map[string]any{
		"limit": 0,
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(*LoadMemoryResponse)
	require.True(t, ok, "Expected *LoadMemoryResponse, got %T", result)

	assert.Equal(t, 10, response.Limit, "Expected default limit 10, got %d", response.Limit)
	assert.Equal(t, 1, response.Count, "Expected 1 result, got %d", response.Count)
}

func TestGetAppAndUserFromContext_ValidContext(t *testing.T) {
	ctx := createMockContext("test-app", "test-user")
	appName, userID, err := GetAppAndUserFromContext(ctx)
	require.NoError(t, err, "Expected no error for valid context")
	assert.Equal(t, "test-app", appName, "Expected app name 'test-app', got '%s'", appName)
	assert.Equal(t, "test-user", userID, "Expected user ID 'test-user', got '%s'", userID)
}

func TestGetAppAndUserFromContext_NoInvocation(t *testing.T) {
	ctx := context.Background()
	appName, userID, err := GetAppAndUserFromContext(ctx)
	require.Error(t, err, "Expected error for context without invocation")
	assert.Empty(t, appName, "Expected empty app name")
	assert.Empty(t, userID, "Expected empty user ID")
	assert.Contains(t, err.Error(), "no invocation context found", "Expected specific error message")
}

func TestGetAppAndUserFromContext_NilInvocation(t *testing.T) {
	ctx := agent.NewInvocationContext(context.Background(), nil)
	appName, userID, err := GetAppAndUserFromContext(ctx)
	require.Error(t, err, "Expected error for nil invocation")
	assert.Empty(t, appName, "Expected empty app name")
	assert.Empty(t, userID, "Expected empty user ID")
	assert.Contains(t, err.Error(), "no invocation context found", "Expected specific error message")
}

func TestGetAppAndUserFromContext_NoSession(t *testing.T) {
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   nil,
	}
	ctx := agent.NewInvocationContext(context.Background(), mockInvocation)
	appName, userID, err := GetAppAndUserFromContext(ctx)
	require.Error(t, err, "Expected error for invocation without session")
	assert.Empty(t, appName, "Expected empty app name")
	assert.Empty(t, userID, "Expected empty user ID")
	assert.Contains(t, err.Error(), "no session available", "Expected specific error message")
}

func TestGetAppAndUserFromContext_MissingAppName(t *testing.T) {
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "",
		UserID:    "test-user",
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}
	ctx := agent.NewInvocationContext(context.Background(), mockInvocation)
	appName, userID, err := GetAppAndUserFromContext(ctx)
	require.Error(t, err, "Expected error for missing app name")
	assert.Empty(t, appName, "Expected empty app name")
	assert.Empty(t, userID, "Expected empty user ID")
	assert.Contains(t, err.Error(), "missing appName or userID", "Expected specific error message")
}

func TestGetAppAndUserFromContext_MissingUserID(t *testing.T) {
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "",
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}
	ctx := agent.NewInvocationContext(context.Background(), mockInvocation)
	appName, userID, err := GetAppAndUserFromContext(ctx)
	require.Error(t, err, "Expected error for missing user ID")
	assert.Empty(t, appName, "Expected empty app name")
	assert.Empty(t, userID, "Expected empty user ID")
	assert.Contains(t, err.Error(), "missing appName or userID", "Expected specific error message")
}

func TestMemoryTool_Declaration_AllTools(t *testing.T) {
	service := newMockMemoryService()

	// Test all tool declarations.
	tools := []struct {
		name     string
		creator  func(memorypkg.Service) tool.CallableTool
		expected string
	}{
		{"AddTool", NewAddTool, "memory_add"},
		{"UpdateTool", NewUpdateTool, "memory_update"},
		{"DeleteTool", NewDeleteTool, "memory_delete"},
		{"ClearTool", NewClearTool, "memory_clear"},
		{"SearchTool", NewSearchTool, "memory_search"},
		{"LoadTool", NewLoadTool, "memory_load"},
	}

	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			tool := tt.creator(service)
			decl := tool.Declaration()
			require.NotNil(t, decl, "Expected non-nil declaration for %s", tt.name)
			assert.Equal(t, tt.expected, decl.Name, "Expected name '%s' for %s, got '%s'", tt.expected, tt.name, decl.Name)
			assert.NotEmpty(t, decl.Description, "Expected non-empty description for %s", tt.name)
			assert.NotNil(t, decl.InputSchema, "Expected non-nil input schema for %s", tt.name)
		})
	}
}
