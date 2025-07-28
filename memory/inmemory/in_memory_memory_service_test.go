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

// Package inmemory provides in-memory memory service implementation.
package inmemory

import (
	"context"
	"fmt"
	"testing"
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
	userID := "test-user"
	memoryStr := "Test memory content"

	// Test adding memory.
	err := service.AddMemory(ctx, userID, memoryStr)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Test basic memory operations.
	memories, err := service.ReadMemories(ctx, userID, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("Expected 1 memory, got %d", len(memories))
	}
	if memories[0].Memory["memory"] != memoryStr {
		t.Fatalf("Expected memory content %s, got %s", memoryStr, memories[0].Memory["memory"])
	}

	// Test memory limit.
	service = NewMemoryService(WithMemoryLimit(1))
	err = service.AddMemory(ctx, userID, "first memory")
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	err = service.AddMemory(ctx, userID, "second memory")
	if err == nil {
		t.Fatal("AddMemory should fail when memory limit is exceeded")
	}
}

func TestMemoryService_UpdateMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userID := "test-user"
	originalMemory := "Original memory"
	updatedMemory := "Updated memory"

	// Add initial memory.
	err := service.AddMemory(ctx, userID, originalMemory)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Get the memory ID.
	memories, err := service.ReadMemories(ctx, userID, 1)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if len(memories) == 0 {
		t.Fatal("No memories found")
	}

	memoryID := memories[0].ID

	// Update memory.
	err = service.UpdateMemory(ctx, userID, memoryID, updatedMemory)
	if err != nil {
		t.Fatalf("UpdateMemory failed: %v", err)
	}

	// Verify update.
	memories, err = service.ReadMemories(ctx, userID, 1)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if memories[0].Memory["memory"] != updatedMemory {
		t.Fatalf("Expected updated memory %s, got %s", updatedMemory, memories[0].Memory["memory"])
	}

	// Test updating non-existent memory.
	err = service.UpdateMemory(ctx, userID, "non-existent-id", "new content")
	if err == nil {
		t.Fatal("UpdateMemory should fail for non-existent memory")
	}
}

func TestMemoryService_DeleteMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userID := "test-user"
	memoryStr := "Memory to delete"

	// Add memory.
	err := service.AddMemory(ctx, userID, memoryStr)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Get memory ID.
	memories, err := service.ReadMemories(ctx, userID, 1)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if len(memories) == 0 {
		t.Fatal("No memories found")
	}

	memoryID := memories[0].ID

	// Delete memory.
	err = service.DeleteMemory(ctx, userID, memoryID)
	if err != nil {
		t.Fatalf("DeleteMemory failed: %v", err)
	}

	// Verify deletion.
	memories, err = service.ReadMemories(ctx, userID, 1)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if len(memories) != 0 {
		t.Fatalf("Expected 0 memories after deletion, got %d", len(memories))
	}

	// Test deleting non-existent memory.
	err = service.DeleteMemory(ctx, userID, "non-existent-id")
	if err == nil {
		t.Fatal("DeleteMemory should fail for non-existent memory")
	}
}

func TestMemoryService_ClearMemories(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userID1 := "user1"
	userID2 := "user2"

	// Add memories for two users.
	err := service.AddMemory(ctx, userID1, "Memory 1 for user 1")
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}
	err = service.AddMemory(ctx, userID1, "Memory 2 for user 1")
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}
	err = service.AddMemory(ctx, userID2, "Memory for user 2")
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}

	// Clear memories for user 1.
	err = service.ClearMemories(ctx, userID1)
	if err != nil {
		t.Fatalf("ClearMemories failed: %v", err)
	}

	// Verify user 1 memories are cleared.
	memories1, err := service.ReadMemories(ctx, userID1, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if len(memories1) != 0 {
		t.Fatalf("Expected 0 memories for user 1, got %d", len(memories1))
	}

	// Verify user 2 memories are still there.
	memories2, err := service.ReadMemories(ctx, userID2, 10)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if len(memories2) != 1 {
		t.Fatalf("Expected 1 memory for user 2, got %d", len(memories2))
	}
}

func TestMemoryService_SearchMemories(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userID := "test-user"

	// Add multiple memories.
	memories := []string{
		"Apple is a fruit",
		"Banana is yellow",
		"Orange is citrus",
		"Grape is purple",
	}

	for _, memory := range memories {
		err := service.AddMemory(ctx, userID, memory)
		if err != nil {
			t.Fatalf("AddMemory failed: %v", err)
		}
	}

	// Test search for "fruit".
	results, err := service.SearchMemories(ctx, userID, "fruit")
	if err != nil {
		t.Fatalf("SearchMemories failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for 'fruit', got %d", len(results))
	}

	// Test search for "yellow".
	results, err = service.SearchMemories(ctx, userID, "yellow")
	if err != nil {
		t.Fatalf("SearchMemories failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for 'yellow', got %d", len(results))
	}

	// Test search for non-existent term.
	results, err = service.SearchMemories(ctx, userID, "xyz")
	if err != nil {
		t.Fatalf("SearchMemories failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Expected 0 results for 'xyz', got %d", len(results))
	}
}

func TestMemoryService_ReadMemoriesWithLimit(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userID := "test-user"

	// Add multiple memories.
	for i := 0; i < 5; i++ {
		memoryStr := fmt.Sprintf("Memory %d", i)
		err := service.AddMemory(ctx, userID, memoryStr)
		if err != nil {
			t.Fatalf("AddMemory failed: %v", err)
		}
	}

	// Test reading with limit.
	memories, err := service.ReadMemories(ctx, userID, 3)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if len(memories) != 3 {
		t.Fatalf("Expected 3 memories, got %d", len(memories))
	}

	// Test reading without limit.
	memories, err = service.ReadMemories(ctx, userID, 0)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}
	if len(memories) != 5 {
		t.Fatalf("Expected 5 memories, got %d", len(memories))
	}
}

func TestMemoryService_Concurrency(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userID := "test-user"

	// Test concurrent access.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			memoryStr := fmt.Sprintf("Memory %d", index)
			err := service.AddMemory(ctx, userID, memoryStr)
			if err != nil {
				t.Errorf("AddMemory failed: %v", err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all memories were added.
	memories, err := service.ReadMemories(ctx, userID, 20)
	if err != nil {
		t.Fatalf("ReadMemories failed: %v", err)
	}

	if len(memories) != 10 {
		t.Fatalf("Expected 10 memories, got %d", len(memories))
	}
}
