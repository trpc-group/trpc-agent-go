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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestMemoryTool_AddMemory(t *testing.T) {
	service := inmemory.NewMemoryService()

	tool := newMemoryTool(service, addMemoryFunction, "memory_add", "Add memory")

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

	// Test adding a memory.
	args := map[string]any{
		"memory": "User's name is John Doe",
		"topics": []string{"personal"},
	}

	jsonArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	result, err := tool.Call(ctx, jsonArgs)
	if err != nil {
		t.Fatalf("Failed to call tool: %v", err)
	}

	response, ok := result.(AddMemoryResponse)
	if !ok {
		t.Fatalf("Expected AddMemoryResponse, got %T", result)
	}

	if !response.Success {
		t.Errorf("Expected success, got failure: %s", response.Message)
	}

	// Verify response fields are correctly populated.
	if response.Memory != "User's name is John Doe" {
		t.Errorf("Expected memory 'User's name is John Doe', got '%s'", response.Memory)
	}

	if len(response.Topics) != 1 {
		t.Errorf("Expected 1 topic, got %d", len(response.Topics))
	}

	if response.Topics[0] != "personal" {
		t.Errorf("Expected topic 'personal', got '%s'", response.Topics[0])
	}

	// Verify memory was added.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	memories, err := service.ReadMemories(context.Background(), userKey, 10)
	if err != nil {
		t.Fatalf("Failed to read memories: %v", err)
	}

	if len(memories) != 1 {
		t.Fatalf("Expected 1 memory, got %d", len(memories))
	}

	if memories[0].Memory.Memory != "User's name is John Doe" {
		t.Errorf("Expected memory 'User's name is John Doe', got '%s'", memories[0].Memory.Memory)
	}
}

func TestMemoryTool_AddMemory_WithoutTopics(t *testing.T) {
	service := inmemory.NewMemoryService()

	tool := newMemoryTool(service, addMemoryFunction, "memory_add", "Add memory")

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
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	result, err := tool.Call(ctx, jsonArgs)
	if err != nil {
		t.Fatalf("Failed to call tool: %v", err)
	}

	response, ok := result.(AddMemoryResponse)
	if !ok {
		t.Fatalf("Expected AddMemoryResponse, got %T", result)
	}

	if !response.Success {
		t.Errorf("Expected success, got failure: %s", response.Message)
	}

	// Verify response fields are correctly populated.
	if response.Memory != "User likes coffee" {
		t.Errorf("Expected memory 'User likes coffee', got '%s'", response.Memory)
	}

	if response.Topics == nil {
		t.Error("Expected topics to be empty slice, got nil")
	}

	if len(response.Topics) != 0 {
		t.Errorf("Expected 0 topics, got %d", len(response.Topics))
	}
}

func TestMemoryTool_AddMemory_ErrorHandling(t *testing.T) {
	service := inmemory.NewMemoryService()

	tool := newMemoryTool(service, addMemoryFunction, "memory_add", "Add memory")

	// Test with empty context (no invocation).
	ctx := context.Background()

	args := map[string]any{
		"memory": "User's name is John Doe",
		"topics": []string{"personal"},
	}

	jsonArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	_, err = tool.Call(ctx, jsonArgs)
	if err == nil {
		t.Fatal("Expected error when no invocation context, got nil")
	}

	expectedError := "no invocation context found"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error containing '%s', got '%s'", expectedError, err.Error())
	}

	// Test with invocation but no session.
	mockInvocation := &agent.Invocation{
		AgentName: "test-agent",
		Session:   nil, // No session.
	}
	ctxWithInvocation := agent.NewContextWithInvocation(context.Background(), mockInvocation)

	_, err = tool.Call(ctxWithInvocation, jsonArgs)
	if err == nil {
		t.Fatal("Expected error when invocation exists but no session, got nil")
	}

	expectedError = "invocation exists but no session available"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error containing '%s', got '%s'", expectedError, err.Error())
	}

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
	if err == nil {
		t.Fatal("Expected error when session exists but empty appName/userID, got nil")
	}

	expectedError = "session exists but missing appName or userID"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error containing '%s', got '%s'", expectedError, err.Error())
	}
}

func TestMemoryTool_SearchMemory(t *testing.T) {
	service := inmemory.NewMemoryService()

	// Add some test memories first.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	service.AddMemory(context.Background(), userKey, "User works as a developer", []string{"work"})

	tool := newMemoryTool(service, searchMemoriesFunction, "memory_search", "Search memory")

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
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	result, err := tool.Call(ctx, jsonArgs)
	if err != nil {
		t.Fatalf("Failed to call tool: %v", err)
	}

	response, ok := result.(SearchMemoryResponse)
	if !ok {
		t.Fatalf("Expected SearchMemoryResponse, got %T", result)
	}

	if !response.Success {
		t.Errorf("Expected success, got failure")
	}

	if response.Count != 1 {
		t.Errorf("Expected 1 result, got %d", response.Count)
	}

	if response.Results[0].Memory != "User likes coffee" {
		t.Errorf("Expected memory 'User likes coffee', got '%s'", response.Results[0].Memory)
	}
}

func TestMemoryTool_LoadMemory(t *testing.T) {
	service := inmemory.NewMemoryService()

	// Add some test memories first.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	service.AddMemory(context.Background(), userKey, "User works as a developer", []string{"work"})

	tool := newMemoryTool(service, loadMemoriesFunction, "memory_load", "Load memory")

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

	// Test loading memories.
	args := map[string]any{
		"limit": 5,
	}

	jsonArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	result, err := tool.Call(ctx, jsonArgs)
	if err != nil {
		t.Fatalf("Failed to call tool: %v", err)
	}

	response, ok := result.(LoadMemoryResponse)
	if !ok {
		t.Fatalf("Expected LoadMemoryResponse, got %T", result)
	}

	if !response.Success {
		t.Errorf("Expected success, got failure")
	}

	if response.Count != 2 {
		t.Errorf("Expected 2 memories, got %d", response.Count)
	}

	// Verify memories are returned in correct order (newest first).
	if response.Results[0].Memory != "User works as a developer" {
		t.Errorf("Expected first memory 'User works as a developer', got '%s'", response.Results[0].Memory)
	}

	if response.Results[1].Memory != "User likes coffee" {
		t.Errorf("Expected second memory 'User likes coffee', got '%s'", response.Results[1].Memory)
	}
}

func TestMemoryTool_Declaration(t *testing.T) {
	service := inmemory.NewMemoryService()

	tool := newMemoryTool(service, addMemoryFunction, "memory_add", "Add memory")

	decl := tool.Declaration()
	if decl.Name != "memory_add" {
		t.Errorf("Expected name 'memory_add', got '%s'", decl.Name)
	}

	if decl.Description != "Add memory" {
		t.Errorf("Expected description 'Add memory', got '%s'", decl.Description)
	}

	if decl.InputSchema == nil {
		t.Error("Expected non-nil input schema")
	}
}

func TestMemoryTool_UpdateMemory(t *testing.T) {
	service := inmemory.NewMemoryService()

	// Add a memory first.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	err := service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	if err != nil {
		t.Fatalf("Failed to add memory: %v", err)
	}

	// Get the memory ID.
	memories, err := service.ReadMemories(context.Background(), userKey, 1)
	if err != nil {
		t.Fatalf("Failed to read memories: %v", err)
	}
	if len(memories) == 0 {
		t.Fatal("No memories found")
	}
	memoryID := memories[0].ID

	tool := newMemoryTool(service, updateMemoryFunction, "memory_update", "Update memory")

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

	// Test updating memory with new content and topics.
	args := map[string]any{
		"memory_id": memoryID,
		"memory":    "User loves coffee and tea",
		"topics":    []string{"preferences", "beverages"},
	}

	jsonArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	result, err := tool.Call(ctx, jsonArgs)
	if err != nil {
		t.Fatalf("Failed to call tool: %v", err)
	}

	response, ok := result.(UpdateMemoryResponse)
	if !ok {
		t.Fatalf("Expected UpdateMemoryResponse, got %T", result)
	}

	if !response.Success {
		t.Errorf("Expected success, got failure: %s", response.Message)
	}

	// Verify response fields are correctly populated.
	if response.MemoryID != memoryID {
		t.Errorf("Expected memory ID %s, got %s", memoryID, response.MemoryID)
	}

	if response.Memory != "User loves coffee and tea" {
		t.Errorf("Expected memory 'User loves coffee and tea', got '%s'", response.Memory)
	}

	if len(response.Topics) != 2 {
		t.Errorf("Expected 2 topics, got %d", len(response.Topics))
	}

	if response.Topics[0] != "preferences" || response.Topics[1] != "beverages" {
		t.Errorf("Expected topics ['preferences', 'beverages'], got %v", response.Topics)
	}

	// Verify memory was actually updated.
	updatedMemories, err := service.ReadMemories(context.Background(), userKey, 1)
	if err != nil {
		t.Fatalf("Failed to read updated memories: %v", err)
	}

	if len(updatedMemories) == 0 {
		t.Fatal("No updated memories found")
	}

	if updatedMemories[0].Memory.Memory != "User loves coffee and tea" {
		t.Errorf("Expected updated memory 'User loves coffee and tea', got '%s'", updatedMemories[0].Memory.Memory)
	}

	if len(updatedMemories[0].Memory.Topics) != 2 {
		t.Errorf("Expected 2 updated topics, got %d", len(updatedMemories[0].Memory.Topics))
	}
}

func TestMemoryTool_UpdateMemory_WithoutTopics(t *testing.T) {
	service := inmemory.NewMemoryService()

	// Add a memory first.
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	err := service.AddMemory(context.Background(), userKey, "User likes coffee", []string{"preferences"})
	if err != nil {
		t.Fatalf("Failed to add memory: %v", err)
	}

	// Get the memory ID.
	memories, err := service.ReadMemories(context.Background(), userKey, 1)
	if err != nil {
		t.Fatalf("Failed to read memories: %v", err)
	}
	if len(memories) == 0 {
		t.Fatal("No memories found")
	}
	memoryID := memories[0].ID

	tool := newMemoryTool(service, updateMemoryFunction, "memory_update", "Update memory")

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

	// Test updating memory without topics.
	args := map[string]any{
		"memory_id": memoryID,
		"memory":    "User loves coffee and tea",
	}

	jsonArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	result, err := tool.Call(ctx, jsonArgs)
	if err != nil {
		t.Fatalf("Failed to call tool: %v", err)
	}

	response, ok := result.(UpdateMemoryResponse)
	if !ok {
		t.Fatalf("Expected UpdateMemoryResponse, got %T", result)
	}

	if !response.Success {
		t.Errorf("Expected success, got failure: %s", response.Message)
	}

	// Verify response fields are correctly populated.
	if response.MemoryID != memoryID {
		t.Errorf("Expected memory ID %s, got %s", memoryID, response.MemoryID)
	}

	if response.Memory != "User loves coffee and tea" {
		t.Errorf("Expected memory 'User loves coffee and tea', got '%s'", response.Memory)
	}

	if response.Topics == nil {
		t.Error("Expected topics to be empty slice, got nil")
	}

	if len(response.Topics) != 0 {
		t.Errorf("Expected 0 topics, got %d", len(response.Topics))
	}
}
