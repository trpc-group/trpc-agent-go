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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// MemoryUpdateTool is a tool for updating existing memories.
type MemoryUpdateTool struct {
	memoryService memory.Service
	appName       string
	userID        string
}

// NewMemoryUpdateTool creates a new MemoryUpdateTool.
func NewMemoryUpdateTool(memoryService memory.Service, appName string, userID string) *MemoryUpdateTool {
	return &MemoryUpdateTool{
		memoryService: memoryService,
		appName:       appName,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *MemoryUpdateTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "memory_update",
		Description: "Update an existing memory for the user. Use this when you need to modify or append information " +
			"to an existing memory. When updating, append new information to the existing memory rather than completely " +
			"overwriting it. This is useful when the user's preferences change or when you learn additional details " +
			"about something you already know about the user.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"memory_id": {
					Type: "string",
					Description: "The ID of the memory to update. You can get this from memory_load or memory_search " +
						"results.",
				},
				"memory": {
					Type: "string",
					Description: "The updated memory content. Should be a brief, third-person statement that captures " +
						"the updated information about the user.",
				},
				"input": {
					Type:        "string",
					Description: "The original user input that led to this memory update.",
				},
				"topics": {
					Type:  "array",
					Items: &tool.Schema{Type: "string"},
					Description: "Optional updated topics for categorizing the memory (e.g. ['name', 'hobbies', " +
						"'location', 'work', 'preferences']).",
				},
			},
			Required: []string{"memory_id", "memory", "input"},
		},
	}
}

// Call executes the memory update tool.
func (m *MemoryUpdateTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.memoryService == nil {
		return nil, errors.New("memory service not available")
	}

	var args struct {
		MemoryID string   `json:"memory_id"`
		Memory   string   `json:"memory"`
		Input    string   `json:"input"`
		Topics   []string `json:"topics,omitempty"`
	}

	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %v", err)
	}

	if args.MemoryID == "" {
		return nil, errors.New("memory_id cannot be empty")
	}

	if args.Memory == "" {
		return nil, errors.New("memory content cannot be empty")
	}

	if args.Input == "" {
		return nil, errors.New("input content cannot be empty")
	}

	// Create memory key.
	memoryKey := memory.MemoryKey{
		AppName:  m.appName,
		UserID:   m.userID,
		MemoryID: args.MemoryID,
	}

	// Update memory in the service.
	err := m.memoryService.UpdateMemory(ctx, memoryKey, args.Memory)
	if err != nil {
		return nil, fmt.Errorf("failed to update memory: %v", err)
	}

	return UpdateMemoryResponse{
		Success:  true,
		Message:  "Memory updated successfully",
		MemoryID: args.MemoryID,
		Memory:   args.Memory,
		Input:    args.Input,
		Topics:   args.Topics,
	}, nil
}
