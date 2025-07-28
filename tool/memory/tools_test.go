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

	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
)

func TestMemoryAddTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	userID := "test-user"
	tool := NewMemoryAddTool(service, userID)

	// Test tool declaration.
	declaration := tool.Declaration()
	if declaration.Name != "memory_add" {
		t.Fatalf("Expected tool name 'memory_add', got '%s'", declaration.Name)
	}

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

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Expected map result")
	}

	if resultMap["success"] != true {
		t.Fatal("Expected success to be true")
	}

	// Verify memory was added.
	memories, err := service.ReadMemories(context.Background(), userID, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 1 {
		t.Fatalf("Expected 1 memory, got %d", len(memories))
	}
}

func TestMemorySearchTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	userID := "test-user"

	// Add some test memories.
	service.AddMemory(context.Background(), userID, "User likes coffee", "User said: I like coffee", nil)
	service.AddMemory(context.Background(), userID, "User works as a developer", "User said: I work as a developer", nil)
	service.AddMemory(context.Background(), userID, "User has a dog", "User said: I have a dog", nil)

	tool := NewMemorySearchTool(service, userID)

	// Test tool declaration.
	declaration := tool.Declaration()
	if declaration.Name != "memory_search" {
		t.Fatalf("Expected tool name 'memory_search', got '%s'", declaration.Name)
	}

	// Test searching memories.
	args := map[string]interface{}{
		"query": "coffee",
		"limit": 5,
	}
	jsonArgs, _ := json.Marshal(args)

	result, err := tool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Expected map result")
	}

	if resultMap["success"] != true {
		t.Fatal("Expected success to be true")
	}

	if resultMap["count"].(int) != 1 {
		t.Fatalf("Expected 1 result, got %d", resultMap["count"])
	}
}

func TestMemoryLoadTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	userID := "test-user"

	// Add some test memories.
	service.AddMemory(context.Background(), userID, "User likes coffee", "User said: I like coffee", nil)
	service.AddMemory(context.Background(), userID, "User works as a developer", "User said: I work as a developer", nil)

	tool := NewMemoryLoadTool(service, userID)

	// Test tool declaration.
	declaration := tool.Declaration()
	if declaration.Name != "memory_load" {
		t.Fatalf("Expected tool name 'memory_load', got '%s'", declaration.Name)
	}

	// Test loading memories.
	args := map[string]interface{}{
		"limit": 5,
	}
	jsonArgs, _ := json.Marshal(args)

	result, err := tool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Expected map result")
	}

	if resultMap["success"] != true {
		t.Fatal("Expected success to be true")
	}

	if resultMap["count"].(int) != 2 {
		t.Fatalf("Expected 2 memories, got %d", resultMap["count"])
	}
}

func TestGetMemoryTools(t *testing.T) {
	service := inmemory.NewMemoryService()
	userID := "test-user"

	tools := GetMemoryTools(service, userID)
	if len(tools) != 3 {
		t.Fatalf("Expected 3 tools, got %d", len(tools))
	}

	// Verify tool types.
	_, ok := tools[0].(*MemoryAddTool)
	if !ok {
		t.Fatal("Expected MemoryAddTool")
	}

	_, ok = tools[1].(*MemorySearchTool)
	if !ok {
		t.Fatal("Expected MemorySearchTool")
	}

	_, ok = tools[2].(*MemoryLoadTool)
	if !ok {
		t.Fatal("Expected MemoryLoadTool")
	}
}
