//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	key := userKey.AppName + ":" + userKey.UserID + ":" + fmt.Sprintf("memory-%d", m.counter)
	m.memories[key] = &memorypkg.Entry{
		ID:        key,
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
	return assert.AnError
}

func (m *mockMemoryService) DeleteMemory(ctx context.Context, memoryKey memorypkg.Key) error {
	key := memoryKey.AppName + ":" + memoryKey.UserID + ":" + memoryKey.MemoryID
	if _, exists := m.memories[key]; exists {
		delete(m.memories, key)
		return nil
	}
	return assert.AnError
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

	for _, entry := range m.memories {
		if len(entry.ID) > len(prefix) && entry.ID[:len(prefix)] == prefix {
			results = append(results, entry)
			if len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

func (m *mockMemoryService) SearchMemories(ctx context.Context, userKey memorypkg.UserKey, query string) ([]*memorypkg.Entry, error) {
	var results []*memorypkg.Entry
	prefix := userKey.AppName + ":" + userKey.UserID + ":"

	for _, entry := range m.memories {
		if len(entry.ID) > len(prefix) && entry.ID[:len(prefix)] == prefix {
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

	return agent.NewContextWithInvocation(context.Background(), mockInvocation)
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
