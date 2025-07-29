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

// DeleteTool is a tool for deleting memories.
type DeleteTool struct {
	memoryService memory.Service
	appName       string
	userID        string
}

// NewDeleteTool creates a new DeleteTool.
func NewDeleteTool(memoryService memory.Service, appName string, userID string) *DeleteTool {
	return &DeleteTool{
		memoryService: memoryService,
		appName:       appName,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *DeleteTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "memory_delete",
		Description: "Delete a specific memory for the user. Use this when the user asks for a memory to be forgotten " +
			"or when information becomes outdated and should be removed. Don't say 'The user used to like...' - " +
			"completely remove the reference to the information that should be forgotten.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"memory_id": {
					Type: "string",
					Description: "The ID of the memory to delete. You can get this from memory_load or memory_search " +
						"results.",
				},
			},
			Required: []string{"memory_id"},
		},
	}
}

// Call executes the memory delete tool.
func (m *DeleteTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.memoryService == nil {
		return nil, errors.New("memory service not available")
	}

	var args struct {
		MemoryID string `json:"memory_id"`
	}

	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %v", err)
	}

	if args.MemoryID == "" {
		return nil, errors.New("memory_id cannot be empty")
	}

	// Create memory key.
	memoryKey := memory.MemoryKey{
		AppName:  m.appName,
		UserID:   m.userID,
		MemoryID: args.MemoryID,
	}

	// Delete memory from the service.
	err := m.memoryService.DeleteMemory(ctx, memoryKey)
	if err != nil {
		return nil, fmt.Errorf("failed to delete memory: %v", err)
	}

	return DeleteMemoryResponse{
		Success:  true,
		Message:  "Memory deleted successfully",
		MemoryID: args.MemoryID,
	}, nil
}
