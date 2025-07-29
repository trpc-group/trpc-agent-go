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
//

package memory

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
)

func TestAddTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"
	tool := NewAddTool(service, appName, userID)

	// Test adding memory.
	args := map[string]interface{}{
		"memory": "User likes coffee",
		"input":  "User said: I like coffee",
		"topics": []string{"preferences"},
	}
	jsonArgs, _ := json.Marshal(args)

	result, err := tool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	resultStruct, ok := result.(AddMemoryResponse)
	if !ok {
		t.Fatal("Expected AddMemoryResponse")
	}

	if !resultStruct.Success {
		t.Fatal("Expected success to be true")
	}

	if resultStruct.Memory != "User likes coffee" {
		t.Fatalf("Expected memory 'User likes coffee', got %s", resultStruct.Memory)
	}

	// Verify memory was added.
	userKey := memory.UserKey{
		AppName: appName,
		UserID:  userID,
	}
	memories, err := service.ReadMemories(context.Background(), userKey, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 1 {
		t.Fatalf("Expected 1 memory, got %d", len(memories))
	}

	if memories[0].Memory.Memory != "User likes coffee" {
		t.Fatalf("Expected memory content 'User likes coffee', got %s", memories[0].Memory.Memory)
	}
}

func TestSearchTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Add some test memories.
	userKey := memory.UserKey{
		AppName: appName,
		UserID:  userID,
	}
	service.AddMemory(context.Background(), userKey, "User likes coffee", "User said: I like coffee", nil)
	service.AddMemory(context.Background(), userKey, "User works as a developer", "User said: I work as a developer", nil)
	service.AddMemory(context.Background(), userKey, "User has a dog", "User said: I have a dog", nil)

	tool := NewSearchTool(service, appName, userID)

	// Test searching for coffee.
	args := map[string]interface{}{
		"query": "coffee",
	}
	jsonArgs, _ := json.Marshal(args)

	result, err := tool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	resultStruct, ok := result.(SearchMemoryResponse)
	if !ok {
		t.Fatal("Expected SearchMemoryResponse")
	}

	if !resultStruct.Success {
		t.Fatal("Expected success to be true")
	}

	if len(resultStruct.Results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(resultStruct.Results))
	}

	if resultStruct.Results[0].Memory != "User likes coffee" {
		t.Fatalf("Expected memory 'User likes coffee', got %s", resultStruct.Results[0].Memory)
	}
}

func TestLoadTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Add some test memories.
	userKey := memory.UserKey{
		AppName: appName,
		UserID:  userID,
	}
	service.AddMemory(context.Background(), userKey, "User likes coffee", "User said: I like coffee", nil)
	service.AddMemory(context.Background(), userKey, "User works as a developer", "User said: I work as a developer", nil)

	tool := NewLoadTool(service, appName, userID)

	// Test loading memories with limit.
	args := map[string]interface{}{
		"limit": 1,
	}
	jsonArgs, _ := json.Marshal(args)

	result, err := tool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	resultStruct, ok := result.(LoadMemoryResponse)
	if !ok {
		t.Fatal("Expected LoadMemoryResponse")
	}

	if !resultStruct.Success {
		t.Fatal("Expected success to be true")
	}

	if len(resultStruct.Results) != 1 {
		t.Fatalf("Expected 1 result with limit, got %d", len(resultStruct.Results))
	}
}

func TestNewMemoryTools(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	tools := NewMemoryTools(service, appName, userID)

	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify tool types.
	addTool, ok := tools[0].(*AddTool)
	if !ok {
		t.Fatal("Expected AddTool")
	}
	if addTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, addTool.appName)
	}
	if addTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, addTool.userID)
	}

	updateTool, ok := tools[1].(*UpdateTool)
	if !ok {
		t.Fatal("Expected UpdateTool")
	}
	if updateTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, updateTool.appName)
	}
	if updateTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, updateTool.userID)
	}

	deleteTool, ok := tools[2].(*DeleteTool)
	if !ok {
		t.Fatal("Expected DeleteTool")
	}
	if deleteTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, deleteTool.appName)
	}
	if deleteTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, deleteTool.userID)
	}

	clearTool, ok := tools[3].(*ClearTool)
	if !ok {
		t.Fatal("Expected ClearTool")
	}
	if clearTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, clearTool.appName)
	}
	if clearTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, clearTool.userID)
	}

	searchTool, ok := tools[4].(*SearchTool)
	if !ok {
		t.Fatal("Expected SearchTool")
	}
	if searchTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, searchTool.appName)
	}
	if searchTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, searchTool.userID)
	}

	loadTool, ok := tools[5].(*LoadTool)
	if !ok {
		t.Fatal("Expected LoadTool")
	}
	if loadTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, loadTool.appName)
	}
	if loadTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, loadTool.userID)
	}
}
