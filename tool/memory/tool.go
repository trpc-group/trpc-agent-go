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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// memoryTool wraps a memory service method as a function tool.
type memoryTool struct {
	service  memory.Service
	function func(context.Context, memory.Service, string, string, any) (any, error)
	name     string
	desc     string
}

// newMemoryTool creates a new memory function tool.
func newMemoryTool(
	service memory.Service,
	fn func(context.Context, memory.Service, string, string, any) (any, error),
	name string,
	desc string,
) *memoryTool {
	return &memoryTool{
		service:  service,
		function: fn,
		name:     name,
		desc:     desc,
	}
}

// Call executes the memory function tool.
func (m *memoryTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.service == nil {
		return nil, errors.New("memory service not available")
	}

	// Get appName and userID from context
	appName, userID, err := getAppAndUserFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get app and user from context: %v", err)
	}

	// Parse arguments based on the function type
	var args any
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %v", err)
	}

	return m.function(ctx, m.service, appName, userID, args)
}

// Declaration returns the tool declaration.
func (m *memoryTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.desc,
		InputSchema: m.getInputSchema(),
	}
}

const (
	// AddToolName is the name of the add memory tool.
	AddToolName = "memory_add"
	// UpdateToolName is the name of the update memory tool.
	UpdateToolName = "memory_update"
	// DeleteToolName is the name of the delete memory tool.
	DeleteToolName = "memory_delete"
	// ClearToolName is the name of the clear memory tool.
	ClearToolName = "memory_clear"
	// SearchToolName is the name of the search memory tool.
	SearchToolName = "memory_search"
	// LoadToolName is the name of the load memory tool.
	LoadToolName = "memory_load"
)

// getInputSchema returns the input schema based on the function type.
func (m *memoryTool) getInputSchema() *tool.Schema {
	switch m.name {
	case AddToolName:
		return &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"memory": {
					Type: "string",
					Description: "The memory content to store. Should be a brief, third-person statement that " +
						"captures key information about the user.",
				},
				"topics": {
					Type:        "array",
					Items:       &tool.Schema{Type: "string"},
					Description: "Optional topics for categorizing the memory.",
				},
			},
			Required: []string{"memory"},
		}
	case UpdateToolName:
		return &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"memory_id": {
					Type:        "string",
					Description: "The ID of the memory to update.",
				},
				"memory": {
					Type:        "string",
					Description: "The updated memory content.",
				},
				"topics": {
					Type:        "array",
					Items:       &tool.Schema{Type: "string"},
					Description: "Optional topics for categorizing the memory.",
				},
			},
			Required: []string{"memory_id", "memory"},
		}
	case DeleteToolName:
		return &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"memory_id": {
					Type:        "string",
					Description: "The ID of the memory to delete.",
				},
			},
			Required: []string{"memory_id"},
		}
	case ClearToolName:
		return &tool.Schema{
			Type:        "object",
			Description: "No parameters required. Clears all memories for the user.",
		}
	case SearchToolName:
		return &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"query": {
					Type:        "string",
					Description: "The search query to find relevant memories.",
				},
			},
			Required: []string{"query"},
		}
	case LoadToolName:
		return &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"limit": {
					Type:        "integer",
					Description: "Maximum number of memories to load (default: 10).",
				},
			},
		}
	default:
		return &tool.Schema{Type: "object"}
	}
}

// getAppAndUserFromContext extracts appName and userID from the context.
// This function looks for these values in the agent invocation context.
func getAppAndUserFromContext(ctx context.Context) (string, string, error) {
	// Try to get from agent invocation context.
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return "", "", errors.New("no invocation context found")
	}

	// Try to get from session.
	if invocation.Session == nil {
		return "", "", errors.New("invocation exists but no session available")
	}

	// Session has AppName and UserID fields.
	if invocation.Session.AppName != "" && invocation.Session.UserID != "" {
		return invocation.Session.AppName, invocation.Session.UserID, nil
	}

	// Return error if session exists but missing required fields.
	return "", "", fmt.Errorf("session exists but missing appName or userID: appName=%s, userID=%s",
		invocation.Session.AppName, invocation.Session.UserID)
}

// Memory function implementations.

// addMemoryFunction implements the add memory functionality.
func addMemoryFunction(ctx context.Context, service memory.Service, appName string, userID string, args any) (any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, errors.New("invalid arguments format")
	}

	memoryStr, ok := argsMap["memory"].(string)
	if !ok || memoryStr == "" {
		return nil, errors.New("memory content is required")
	}

	var topics []string
	if topicsInterface, ok := argsMap["topics"]; ok {
		if topicsSlice, ok := topicsInterface.([]any); ok {
			for _, topic := range topicsSlice {
				if topicStr, ok := topic.(string); ok {
					topics = append(topics, topicStr)
				}
			}
		}
	}
	// Ensure topics is never nil
	if topics == nil {
		topics = []string{}
	}

	userKey := memory.UserKey{AppName: appName, UserID: userID}
	err := service.AddMemory(ctx, userKey, memoryStr, topics)
	if err != nil {
		return nil, fmt.Errorf("failed to add memory: %v", err)
	}

	return AddMemoryResponse{
		Success: true,
		Message: "Memory added successfully",
		Memory:  memoryStr,
		Topics:  topics,
	}, nil
}

// updateMemoryFunction implements the update memory functionality.
func updateMemoryFunction(ctx context.Context, service memory.Service, appName string, userID string, args any) (any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, errors.New("invalid arguments format")
	}

	memoryID, ok := argsMap["memory_id"].(string)
	if !ok || memoryID == "" {
		return nil, errors.New("memory_id is required")
	}

	memoryStr, ok := argsMap["memory"].(string)
	if !ok || memoryStr == "" {
		return nil, errors.New("memory content is required")
	}

	var topics []string
	if topicsInterface, ok := argsMap["topics"]; ok {
		if topicsSlice, ok := topicsInterface.([]any); ok {
			for _, topic := range topicsSlice {
				if topicStr, ok := topic.(string); ok {
					topics = append(topics, topicStr)
				}
			}
		}
	}
	// Ensure topics is never nil
	if topics == nil {
		topics = []string{}
	}

	memoryKey := memory.Key{AppName: appName, UserID: userID, MemoryID: memoryID}
	err := service.UpdateMemory(ctx, memoryKey, memoryStr, topics)
	if err != nil {
		return nil, fmt.Errorf("failed to update memory: %v", err)
	}

	return UpdateMemoryResponse{
		Success:  true,
		Message:  "Memory updated successfully",
		MemoryID: memoryID,
		Memory:   memoryStr,
		Topics:   topics,
	}, nil
}

// deleteMemoryFunction implements the delete memory functionality.
func deleteMemoryFunction(ctx context.Context, service memory.Service, appName string, userID string, args any) (any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, errors.New("invalid arguments format")
	}

	memoryID, ok := argsMap["memory_id"].(string)
	if !ok || memoryID == "" {
		return nil, errors.New("memory_id is required")
	}

	memoryKey := memory.Key{AppName: appName, UserID: userID, MemoryID: memoryID}
	err := service.DeleteMemory(ctx, memoryKey)
	if err != nil {
		return nil, fmt.Errorf("failed to delete memory: %v", err)
	}

	return DeleteMemoryResponse{
		Success:  true,
		Message:  "Memory deleted successfully",
		MemoryID: memoryID,
	}, nil
}

// clearMemoriesFunction implements the clear memories functionality.
func clearMemoriesFunction(ctx context.Context, service memory.Service, appName string, userID string, args any) (any, error) {
	userKey := memory.UserKey{AppName: appName, UserID: userID}
	err := service.ClearMemories(ctx, userKey)
	if err != nil {
		return nil, fmt.Errorf("failed to clear memories: %v", err)
	}

	return ClearMemoryResponse{
		Success: true,
		Message: "All memories cleared successfully",
	}, nil
}

// searchMemoriesFunction implements the search memories functionality.
func searchMemoriesFunction(ctx context.Context, service memory.Service, appName string, userID string, args any) (any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return nil, errors.New("invalid arguments format")
	}

	query, ok := argsMap["query"].(string)
	if !ok || query == "" {
		return nil, errors.New("query is required")
	}

	userKey := memory.UserKey{AppName: appName, UserID: userID}
	memories, err := service.SearchMemories(ctx, userKey, query)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories: %v", err)
	}

	// Convert MemoryEntry to MemoryResult
	results := make([]Result, len(memories))
	for i, memory := range memories {
		results[i] = Result{
			ID:      memory.ID,
			Memory:  memory.Memory.Memory,
			Topics:  memory.Memory.Topics,
			Created: memory.CreatedAt,
		}
	}

	return SearchMemoryResponse{
		Success: true,
		Query:   query,
		Results: results,
		Count:   len(results),
	}, nil
}

// loadMemoriesFunction implements the load memories functionality.
func loadMemoriesFunction(ctx context.Context, service memory.Service, appName string, userID string, args any) (any, error) {
	limit := 10 // Default limit
	if argsMap, ok := args.(map[string]any); ok {
		if limitInterface, ok := argsMap["limit"]; ok {
			if limitFloat, ok := limitInterface.(float64); ok {
				limit = int(limitFloat)
			}
		}
	}

	userKey := memory.UserKey{AppName: appName, UserID: userID}
	memories, err := service.ReadMemories(ctx, userKey, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to load memories: %v", err)
	}

	// Convert MemoryEntry to MemoryResult
	results := make([]Result, len(memories))
	for i, memory := range memories {
		results[i] = Result{
			ID:      memory.ID,
			Memory:  memory.Memory.Memory,
			Topics:  memory.Memory.Topics,
			Created: memory.CreatedAt,
		}
	}

	return LoadMemoryResponse{
		Success: true,
		Limit:   limit,
		Results: results,
		Count:   len(results),
	}, nil
}
