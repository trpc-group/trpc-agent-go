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

// SearchTool is a tool for searching memories.
type SearchTool struct {
	memoryService memory.Service
	appName       string
	userID        string
}

// NewSearchTool creates a new SearchTool.
func NewSearchTool(memoryService memory.Service, appName string, userID string) *SearchTool {
	return &SearchTool{
		memoryService: memoryService,
		appName:       appName,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *SearchTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "memory_search",
		Description: "Search for memories related to a query. Use this when you want to find relevant information " +
			"from the user's memory to provide personalized responses. Search through both memory content and topics " +
			"to find relevant information about the user's preferences, background, or past interactions.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"query": {
					Type: "string",
					Description: "The search query to find relevant memories. Can be keywords, topics, or specific " +
						"information you're looking for about the user.",
				},
			},
			Required: []string{"query"},
		},
	}
}

// Call executes the memory search tool.
func (m *SearchTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.memoryService == nil {
		return nil, errors.New("memory service not available")
	}

	var args struct {
		Query string `json:"query"`
	}

	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %v", err)
	}

	if args.Query == "" {
		return nil, errors.New("search query cannot be empty")
	}

	// Create user key.
	userKey := memory.UserKey{
		AppName: m.appName,
		UserID:  m.userID,
	}

	// Search memories.
	memories, err := m.memoryService.SearchMemories(ctx, userKey, args.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories: %v", err)
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

	return SearchMemoryResponse{
		Success: true,
		Query:   args.Query,
		Results: results,
		Count:   len(results),
	}, nil
}
