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

func TestMemoryAddTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"
	tool := NewMemoryAddTool(service, appName, userID)

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

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatal("Expected map result")
	}

	if resultMap["success"] != true {
		t.Fatal("Expected success to be true")
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

func TestMemorySearchTool(t *testing.T) {
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

	tool := NewMemorySearchTool(service, appName, userID)

	// Test searching for coffee.
	args := map[string]interface{}{
		"query": "coffee",
	}
	jsonArgs, _ := json.Marshal(args)

	result, err := tool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatal("Expected map result")
	}

	if resultMap["success"] != true {
		t.Fatal("Expected success to be true")
	}

	results, ok := resultMap["results"].([]map[string]any)
	if !ok {
		t.Fatal("Expected results array")
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0]["memory"] != "User likes coffee" {
		t.Fatalf("Expected memory 'User likes coffee', got %s", results[0]["memory"])
	}
}

func TestMemoryLoadTool(t *testing.T) {
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

	tool := NewMemoryLoadTool(service, appName, userID)

	// Test loading memories with limit.
	args := map[string]interface{}{
		"limit": 1,
	}
	jsonArgs, _ := json.Marshal(args)

	result, err := tool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatal("Expected map result")
	}

	if resultMap["success"] != true {
		t.Fatal("Expected success to be true")
	}

	results, ok := resultMap["results"].([]map[string]any)
	if !ok {
		t.Fatal("Expected results array")
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result with limit, got %d", len(results))
	}
}

func TestGetMemoryTools(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	tools := GetMemoryTools(service, appName, userID)

	if len(tools) != 3 {
		t.Fatalf("Expected 3 tools, got %d", len(tools))
	}

	// Verify tool types.
	addTool, ok := tools[0].(*MemoryAddTool)
	if !ok {
		t.Fatal("Expected MemoryAddTool")
	}
	if addTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, addTool.appName)
	}
	if addTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, addTool.userID)
	}

	searchTool, ok := tools[1].(*MemorySearchTool)
	if !ok {
		t.Fatal("Expected MemorySearchTool")
	}
	if searchTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, searchTool.appName)
	}
	if searchTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, searchTool.userID)
	}

	loadTool, ok := tools[2].(*MemoryLoadTool)
	if !ok {
		t.Fatal("Expected MemoryLoadTool")
	}
	if loadTool.appName != appName {
		t.Fatalf("Expected appName %s, got %s", appName, loadTool.appName)
	}
	if loadTool.userID != userID {
		t.Fatalf("Expected userID %s, got %s", userID, loadTool.userID)
	}
}
