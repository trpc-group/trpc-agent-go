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

// MemorySearchTool provides a tool for LLM to search memories.
type MemorySearchTool struct {
	memoryService memory.Service
	userID        string
}

// NewMemorySearchTool creates a new MemorySearchTool.
func NewMemorySearchTool(memoryService memory.Service, userID string) *MemorySearchTool {
	return &MemorySearchTool{
		memoryService: memoryService,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *MemorySearchTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "memory_search",
		Description: "Search for memories related to a query. Use this when you need to recall information about the user.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"query": {
					Type:        "string",
					Description: "The search query to find relevant memories.",
				},
				"limit": {
					Type:        "integer",
					Description: "Maximum number of memories to return (default: 10).",
				},
			},
			Required: []string{"query"},
		},
	}
}

// Call executes the memory search tool.
func (m *MemorySearchTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.memoryService == nil {
		return nil, errors.New("memory service not available")
	}

	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit,omitempty"`
	}

	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %v", err)
	}

	if args.Query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	// Set default limit.
	if args.Limit <= 0 {
		args.Limit = 10
	}

	// Search memories.
	memories, err := m.memoryService.SearchMemories(ctx, m.userID, args.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories: %v", err)
	}

	// Convert memories to a simpler format for the LLM.
	var results []map[string]any
	for _, memory := range memories {
		if memoryContent, ok := memory.Memory["memory"].(string); ok {
			results = append(results, map[string]any{
				"id":      memory.ID,
				"memory":  memoryContent,
				"created": memory.CreatedAt.Format("2006-01-02 15:04:05"),
			})
		}
	}

	return map[string]any{
		"success":  true,
		"query":    args.Query,
		"count":    len(results),
		"memories": results,
	}, nil
}
