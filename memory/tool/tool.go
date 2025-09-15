//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tool provides memory-related tools for the agent system.
package tool

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Memory function implementations using function.NewFunctionTool.

// NewAddTool creates a function tool for adding memories.
func NewAddTool() tool.CallableTool {
	addFunc := func(ctx context.Context, req *AddMemoryRequest) (*AddMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory add tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory add tool: failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req.Memory == "" {
			return nil, fmt.Errorf("memory add tool: memory content is required for app %s and user %s", appName, userID)
		}

		// Ensure topics is never nil.
		if req.Topics == nil {
			req.Topics = []string{}
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		err = memoryService.AddMemory(ctx, userKey, req.Memory, req.Topics)
		if err != nil {
			return nil, fmt.Errorf("failed to add memory: %v", err)
		}

		return &AddMemoryResponse{
			Message: "Memory added successfully",
			Memory:  req.Memory,
			Topics:  req.Topics,
		}, nil
	}

	return function.NewFunctionTool(
		addFunc,
		function.WithName(memory.AddToolName),
		function.WithDescription("Add a new memory about the user. Use this tool to store "+
			"important information about the user's preferences, background, or past interactions."),
	)
}

// NewUpdateTool creates a function tool for updating memories.
func NewUpdateTool() tool.CallableTool {
	updateFunc := func(ctx context.Context, req *UpdateMemoryRequest) (*UpdateMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, err
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory update tool: failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req.MemoryID == "" {
			return nil, fmt.Errorf("memory update tool: memory ID is required for app %s and user %s", appName, userID)
		}

		if req.Memory == "" {
			return nil, fmt.Errorf("memory update tool: memory content is required for app %s and user %s", appName, userID)
		}

		// Ensure topics is never nil.
		if req.Topics == nil {
			req.Topics = []string{}
		}

		memoryKey := memory.Key{AppName: appName, UserID: userID, MemoryID: req.MemoryID}
		err = memoryService.UpdateMemory(ctx, memoryKey, req.Memory, req.Topics)
		if err != nil {
			return nil, fmt.Errorf("failed to update memory: %v", err)
		}

		return &UpdateMemoryResponse{
			Message:  "Memory updated successfully",
			MemoryID: req.MemoryID,
			Memory:   req.Memory,
			Topics:   req.Topics,
		}, nil
	}

	return function.NewFunctionTool(
		updateFunc,
		function.WithName(memory.UpdateToolName),
		function.WithDescription("Update an existing memory. Use this tool to modify stored "+
			"information about the user."),
	)
}

// NewDeleteTool creates a function tool for deleting memories.
func NewDeleteTool() tool.CallableTool {
	deleteFunc := func(ctx context.Context, req *DeleteMemoryRequest) (*DeleteMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory delete tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory delete tool: failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req.MemoryID == "" {
			return nil, fmt.Errorf("memory delete tool: memory ID is required for app %s and user %s", appName, userID)
		}

		memoryKey := memory.Key{AppName: appName, UserID: userID, MemoryID: req.MemoryID}
		err = memoryService.DeleteMemory(ctx, memoryKey)
		if err != nil {
			return nil, fmt.Errorf("failed to delete memory: %v", err)
		}

		return &DeleteMemoryResponse{
			Message:  "Memory deleted successfully",
			MemoryID: req.MemoryID,
		}, nil
	}

	return function.NewFunctionTool(
		deleteFunc,
		function.WithName(memory.DeleteToolName),
		function.WithDescription("Delete a specific memory. Use this tool to remove outdated "+
			"or incorrect information about the user."),
	)
}

// NewClearTool creates a function tool for clearing all memories.
func NewClearTool() tool.CallableTool {
	clearFunc := func(ctx context.Context, _ *ClearMemoryRequest) (*ClearMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory clear tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory clear tool: failed to get app and user from context: %v", err)
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		err = memoryService.ClearMemories(ctx, userKey)
		if err != nil {
			return nil, fmt.Errorf("memory clear tool: failed to clear memories: %v", err)
		}

		return &ClearMemoryResponse{
			Message: "All memories cleared successfully",
		}, nil
	}

	return function.NewFunctionTool(
		clearFunc,
		function.WithName(memory.ClearToolName),
		function.WithDescription("Clear all memories for the user. Use this tool to reset the "+
			"user's memory completely."),
	)
}

// NewSearchTool creates a function tool for searching memories.
func NewSearchTool() tool.CallableTool {
	searchFunc := func(ctx context.Context, req *SearchMemoryRequest) (*SearchMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory search tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory search tool: failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req.Query == "" {
			return nil, fmt.Errorf("memory search tool: query is required for app %s and user %s", appName, userID)
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		memories, err := memoryService.SearchMemories(ctx, userKey, req.Query)
		if err != nil {
			return nil, fmt.Errorf("failed to search memories: %v", err)
		}

		// Convert MemoryEntry to MemoryResult.
		results := make([]Result, len(memories))
		for i, memory := range memories {
			results[i] = Result{
				ID:      memory.ID,
				Memory:  memory.Memory.Memory,
				Topics:  memory.Memory.Topics,
				Created: memory.CreatedAt,
			}
		}

		return &SearchMemoryResponse{
			Query:   req.Query,
			Results: results,
			Count:   len(results),
		}, nil
	}

	return function.NewFunctionTool(
		searchFunc,
		function.WithName(memory.SearchToolName),
		function.WithDescription("Search for relevant memories about the user. Use this tool to "+
			"find stored information that matches the query."),
	)
}

// NewLoadTool creates a function tool for loading memories.
func NewLoadTool() tool.CallableTool {
	loadFunc := func(ctx context.Context, req *LoadMemoryRequest) (*LoadMemoryResponse, error) {
		// Get MemoryService from context.
		memoryService, err := GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory load tool: failed to get memory service from context: %v", err)
		}

		// Get appName and userID from context.
		appName, userID, err := GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("memory load tool: failed to get app and user from context: %v", err)
		}

		// Set default limit.
		limit := req.Limit
		if limit <= 0 {
			limit = 10
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		memories, err := memoryService.ReadMemories(ctx, userKey, limit)
		if err != nil {
			return nil, fmt.Errorf("failed to load memories: %v", err)
		}

		// Convert MemoryEntry to MemoryResult.
		results := make([]Result, len(memories))
		for i, memory := range memories {
			results[i] = Result{
				ID:      memory.ID,
				Memory:  memory.Memory.Memory,
				Topics:  memory.Memory.Topics,
				Created: memory.CreatedAt,
			}
		}

		return &LoadMemoryResponse{
			Limit:   limit,
			Results: results,
			Count:   len(results),
		}, nil
	}

	return function.NewFunctionTool(
		loadFunc,
		function.WithName(memory.LoadToolName),
		function.WithDescription("Load recent memories about the user. Use this tool to retrieve "+
			"stored information to provide context for the conversation."),
	)
}

// GetMemoryServiceFromContext extracts MemoryService from the invocation context.
// This function looks for the MemoryService in the agent invocation context.
//
// This function is exported to allow users to implement custom memory tools
// that need access to the memory service from the invocation context.
func GetMemoryServiceFromContext(ctx context.Context) (memory.Service, error) {
	// Get invocation from context.
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return nil, errors.New("no invocation context found")
	}

	// Check if MemoryService is available.
	if invocation.MemoryService == nil {
		return nil, errors.New("memory service is not available")
	}

	return invocation.MemoryService, nil
}

// GetAppAndUserFromContext extracts appName and userID from the context.
// This function looks for these values in the agent invocation context.
//
// This function is exported to allow users to implement custom memory tools
// that need access to app and user information from the invocation context.
func GetAppAndUserFromContext(ctx context.Context) (string, string, error) {
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
