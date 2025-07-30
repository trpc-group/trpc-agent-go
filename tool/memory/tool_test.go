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

// Package memory provides memory-related tools for the agent system.
package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestMemoryTool_AddMemory(t *testing.T) {
	service := inmemory.NewMemoryService()
	tool := NewAddMemoryTool(service)

	// Create mock session with appName and userID.
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Create mock invocation with session.
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}

	// Create context with invocation.
	ctx := agent.NewContextWithInvocation(context.Background(), mockInvocation)

	// Test adding a memory with topics.
	args := map[string]any{
		"memory": "User's name is John Doe",
		"topics": []string{"personal"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(AddMemoryResponse)
	require.True(t, ok, "Expected AddMemoryResponse, got %T", result)

	assert.True(t, response.Success, "Expected success, got failure: %s", response.Message)

	// Verify response fields are correctly populated.
	assert.Equal(t, "User's name is John Doe", response.Memory, "Expected memory 'User's name is John Doe', got '%s'", response.Memory)
	assert.Len(t, response.Topics, 1, "Expected 1 topic, got %d", len(response.Topics))
	assert.Equal(t, "personal", response.Topics[0], "Expected topic 'personal', got '%s'", response.Topics[0])

	// Verify memory was added.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	memories, err := service.ReadMemories(context.Background(), userKey, 10)
	require.NoError(t, err, "Failed to read memories")

	assert.Len(t, memories, 1, "Expected 1 memory, got %d", len(memories))
	assert.Equal(t, "User's name is John Doe", memories[0].Memory.Memory, "Expected memory 'User's name is John Doe', got '%s'", memories[0].Memory.Memory)
}

func TestMemoryTool_AddMemory_WithoutTopics(t *testing.T) {
	service := inmemory.NewMemoryService()
	tool := NewAddMemoryTool(service)

	// Create mock session with appName and userID.
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Create mock invocation with session.
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}

	// Create context with invocation.
	ctx := agent.NewContextWithInvocation(context.Background(), mockInvocation)

	// Test adding a memory without topics.
	args := map[string]any{
		"memory": "User likes coffee",
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(AddMemoryResponse)
	require.True(t, ok, "Expected AddMemoryResponse, got %T", result)

	assert.True(t, response.Success, "Expected success, got failure: %s", response.Message)

	// Verify response fields are correctly populated.
	assert.Equal(t, "User likes coffee", response.Memory, "Expected memory 'User likes coffee', got '%s'", response.Memory)
	assert.NotNil(t, response.Topics, "Expected topics to be empty slice, got nil")
	assert.Len(t, response.Topics, 0, "Expected 0 topics, got %d", len(response.Topics))
}

func TestMemoryTool_AddMemory_ErrorHandling(t *testing.T) {
	service := inmemory.NewMemoryService()
	tool := NewAddMemoryTool(service)

	// Test with empty context (no invocation).
	ctx := context.Background()

	args := map[string]any{
		"memory": "User's name is John Doe",
		"topics": []string{"personal"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	_, err = tool.Call(ctx, jsonArgs)
	require.Error(t, err, "Expected error when no invocation context, got nil")

	expectedError := "no invocation context found"
	assert.Contains(t, err.Error(), expectedError, "Expected error containing '%s', got '%s'", expectedError, err.Error())

	// Test with invocation but no session.
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   nil, // No session.
	}
	ctxWithInvocation := agent.NewContextWithInvocation(context.Background(), mockInvocation)

	_, err = tool.Call(ctxWithInvocation, jsonArgs)
	require.Error(t, err, "Expected error when invocation exists but no session, got nil")

	expectedError = "invocation exists but no session available"
	assert.Contains(t, err.Error(), expectedError, "Expected error containing '%s', got '%s'", expectedError, err.Error())

	// Test with invocation and session but empty appName/userID.
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "", // Empty appName.
		UserID:    "", // Empty userID.
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}
	mockInvocationWithSession := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}
	ctxWithSession := agent.NewContextWithInvocation(context.Background(), mockInvocationWithSession)

	_, err = tool.Call(ctxWithSession, jsonArgs)
	require.Error(t, err, "Expected error when session exists but empty appName/userID, got nil")

	expectedError = "session exists but missing appName or userID"
	assert.Contains(t, err.Error(), expectedError, "Expected error containing '%s', got '%s'", expectedError, err.Error())
}

func TestMemoryTool_SearchMemory(t *testing.T) {
	service := inmemory.NewMemoryService()

	// Add some test memories first.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	service.AddMemory(context.Background(), userKey, "User works as a developer", []string{"work"})

	tool := NewSearchMemoryTool(service)

	// Create mock session with appName and userID.
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Create mock invocation with session.
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}

	// Create context with invocation.
	ctx := agent.NewContextWithInvocation(context.Background(), mockInvocation)

	// Test searching memories.
	args := map[string]any{
		"query": "coffee",
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(SearchMemoryResponse)
	require.True(t, ok, "Expected SearchMemoryResponse, got %T", result)

	assert.True(t, response.Success, "Expected success, got failure")
	assert.Equal(t, "coffee", response.Query, "Expected query 'coffee', got '%s'", response.Query)
	assert.Equal(t, 1, response.Count, "Expected 1 result, got %d", response.Count)
	assert.Len(t, response.Results, 1, "Expected 1 result, got %d", len(response.Results))
	assert.Equal(t, "User likes coffee", response.Results[0].Memory, "Expected memory 'User likes coffee', got '%s'", response.Results[0].Memory)
}

func TestMemoryTool_LoadMemory(t *testing.T) {
	service := inmemory.NewMemoryService()

	// Add some test memories first.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	service.AddMemory(context.Background(), userKey, "User works as a developer", []string{"work"})

	tool := NewLoadMemoryTool(service)

	// Create mock session with appName and userID.
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Create mock invocation with session.
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}

	// Create context with invocation.
	ctx := agent.NewContextWithInvocation(context.Background(), mockInvocation)

	// Test loading memories with limit.
	args := map[string]any{
		"limit": 1,
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(LoadMemoryResponse)
	require.True(t, ok, "Expected LoadMemoryResponse, got %T", result)

	assert.True(t, response.Success, "Expected success, got failure")
	assert.Equal(t, 1, response.Limit, "Expected limit 1, got %d", response.Limit)
	assert.Equal(t, 1, response.Count, "Expected 1 result, got %d", response.Count)
	assert.Len(t, response.Results, 1, "Expected 1 result, got %d", len(response.Results))
}

func TestMemoryTool_Declaration(t *testing.T) {
	service := inmemory.NewMemoryService()
	tool := NewAddMemoryTool(service)

	decl := tool.Declaration()
	require.NotNil(t, decl, "Expected non-nil declaration")
	assert.Equal(t, "memory_add", decl.Name, "Expected name 'memory_add', got '%s'", decl.Name)
	assert.NotEmpty(t, decl.Description, "Expected non-empty description")
	assert.NotNil(t, decl.InputSchema, "Expected non-nil input schema")
}

func TestMemoryTool_UpdateMemory(t *testing.T) {
	service := inmemory.NewMemoryService()

	// Add a memory first.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})

	// Get the memory ID.
	memories, err := service.ReadMemories(context.Background(), userKey, 1)
	require.NoError(t, err, "Failed to read memories")

	memoryID := memories[0].ID
	tool := NewUpdateMemoryTool(service)

	// Create mock session with appName and userID.
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Create mock invocation with session.
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}

	// Create context with invocation.
	ctx := agent.NewContextWithInvocation(context.Background(), mockInvocation)

	// Test updating a memory.
	args := map[string]any{
		"memory_id": memoryID,
		"memory":    "User loves coffee",
		"topics":    []string{"preferences", "drinks"},
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(UpdateMemoryResponse)
	require.True(t, ok, "Expected UpdateMemoryResponse, got %T", result)

	assert.True(t, response.Success, "Expected success, got failure: %s", response.Message)
	assert.Equal(t, memoryID, response.MemoryID, "Expected memory ID '%s', got '%s'", memoryID, response.MemoryID)
	assert.Equal(t, "User loves coffee", response.Memory, "Expected memory 'User loves coffee', got '%s'", response.Memory)
	assert.Len(t, response.Topics, 2, "Expected 2 topics, got %d", len(response.Topics))

	// Verify memory was updated.
	memories, err = service.ReadMemories(context.Background(), userKey, 1)
	require.NoError(t, err, "Failed to read memories")

	assert.Equal(t, "User loves coffee", memories[0].Memory.Memory, "Expected updated memory 'User loves coffee', got '%s'", memories[0].Memory.Memory)
}

func TestMemoryTool_UpdateMemory_WithoutTopics(t *testing.T) {
	service := inmemory.NewMemoryService()

	// Add a memory first.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})

	// Get the memory ID.
	memories, err := service.ReadMemories(context.Background(), userKey, 1)
	require.NoError(t, err, "Failed to read memories")

	memoryID := memories[0].ID
	tool := NewUpdateMemoryTool(service)

	// Create mock session with appName and userID.
	mockSession := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		State:     session.StateMap{},
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	// Create mock invocation with session.
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   mockSession,
	}

	// Create context with invocation.
	ctx := agent.NewContextWithInvocation(context.Background(), mockInvocation)

	// Test updating a memory without topics.
	args := map[string]any{
		"memory_id": memoryID,
		"memory":    "User loves coffee",
	}

	jsonArgs, err := json.Marshal(args)
	require.NoError(t, err, "Failed to marshal args")

	result, err := tool.Call(ctx, jsonArgs)
	require.NoError(t, err, "Failed to call tool")

	response, ok := result.(UpdateMemoryResponse)
	require.True(t, ok, "Expected UpdateMemoryResponse, got %T", result)

	assert.True(t, response.Success, "Expected success, got failure: %s", response.Message)
	assert.NotNil(t, response.Topics, "Expected topics to be empty slice, got nil")
	assert.Len(t, response.Topics, 0, "Expected 0 topics, got %d", len(response.Topics))
}
