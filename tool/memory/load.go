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

// MemoryLoadTool is a tool for loading recent memories.
type MemoryLoadTool struct {
	memoryService memory.Service
	appName       string
	userID        string
}

// NewMemoryLoadTool creates a new MemoryLoadTool.
func NewMemoryLoadTool(memoryService memory.Service, appName string, userID string) *MemoryLoadTool {
	return &MemoryLoadTool{
		memoryService: memoryService,
		appName:       appName,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *MemoryLoadTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "memory_load",
		Description: "Load recent memories for the user. Use this when you want to get an overview of what you know " +
			"about the user to provide personalized assistance. This helps you understand the user's background, " +
			"preferences, and past interactions to tailor your responses accordingly.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"limit": {
					Type: "integer",
					Description: "Maximum number of memories to load (default: 10). Use a higher number to get more " +
						"context about the user.",
				},
			},
		},
	}
}

// Call executes the memory load tool.
func (m *MemoryLoadTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.memoryService == nil {
		return nil, errors.New("memory service not available")
	}

	var args struct {
		Limit int `json:"limit,omitempty"`
	}

	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %v", err)
	}

	// Set default limit.
	if args.Limit <= 0 {
		args.Limit = 10
	}

	// Create user key.
	userKey := memory.UserKey{
		AppName: m.appName,
		UserID:  m.userID,
	}

	// Load memories.
	memories, err := m.memoryService.ReadMemories(ctx, userKey, args.Limit)
	if err != nil {
		return nil, fmt.Errorf("failed to load memories: %v", err)
	}

	// Convert memories to MemoryResult format.
	var results []MemoryResult
	for _, memory := range memories {
		results = append(results, MemoryResult{
			ID:      memory.ID,
			Memory:  memory.Memory.Memory,
			Topics:  memory.Memory.Topics,
			Created: memory.CreatedAt,
		})
	}

	return LoadMemoryResponse{
		Success: true,
		Limit:   args.Limit,
		Results: results,
		Count:   len(results),
	}, nil
}
