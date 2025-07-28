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

// MemoryLoadTool provides a tool for LLM to load recent memories.
type MemoryLoadTool struct {
	memoryService memory.Service
	userID        string
}

// NewMemoryLoadTool creates a new MemoryLoadTool.
func NewMemoryLoadTool(memoryService memory.Service, userID string) *MemoryLoadTool {
	return &MemoryLoadTool{
		memoryService: memoryService,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *MemoryLoadTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "memory_load",
		Description: "Load recent memories for the user. Use this to get an overview of what you know about the user.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"limit": {
					Type:        "integer",
					Description: "Maximum number of memories to return (default: 10).",
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
		return nil, errors.New("failed to parse arguments")
	}

	// Set default limit.
	if args.Limit <= 0 {
		args.Limit = 10
	}

	// Load memories.
	memories, err := m.memoryService.ReadMemories(ctx, m.userID, args.Limit)
	if err != nil {
		return nil, fmt.Errorf("failed to load memories: %v", err)
	}

	// Convert memories to a simpler format for the LLM.
	var results []map[string]any
	for _, memory := range memories {
		results = append(results, map[string]any{
			"id":      memory.ID,
			"memory":  memory.Memory.Memory,
			"topics":  memory.Memory.Topics,
			"created": memory.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	return map[string]any{
		"success":  true,
		"count":    len(results),
		"memories": results,
	}, nil
}
