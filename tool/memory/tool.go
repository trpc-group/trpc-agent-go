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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

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

// Memory function implementations using function.NewFunctionTool.

// NewAddMemoryTool creates a function tool for adding memories.
func NewAddMemoryTool(service memory.Service) tool.CallableTool {
	addFunc := func(ctx context.Context, req AddMemoryRequest) (AddMemoryResponse, error) {
		// Get appName and userID from context.
		appName, userID, err := getAppAndUserFromContext(ctx)
		if err != nil {
			return AddMemoryResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to get app and user from context: %v", err),
			}, fmt.Errorf("failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req.Memory == "" {
			return AddMemoryResponse{
				Success: false,
				Message: "Memory content is required",
			}, errors.New("memory content is required")
		}

		// Ensure topics is never nil.
		if req.Topics == nil {
			req.Topics = []string{}
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		err = service.AddMemory(ctx, userKey, req.Memory, req.Topics)
		if err != nil {
			return AddMemoryResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to add memory: %v", err),
			}, fmt.Errorf("failed to add memory: %v", err)
		}

		return AddMemoryResponse{
			Success: true,
			Message: "Memory added successfully",
			Memory:  req.Memory,
			Topics:  req.Topics,
		}, nil
	}

	return function.NewFunctionTool(
		addFunc,
		function.WithName(AddToolName),
		function.WithDescription("Add a new memory about the user. Use this tool to store important information about the user's preferences, background, or past interactions."),
	)
}

// NewUpdateMemoryTool creates a function tool for updating memories.
func NewUpdateMemoryTool(service memory.Service) tool.CallableTool {
	updateFunc := func(ctx context.Context, req UpdateMemoryRequest) (UpdateMemoryResponse, error) {
		// Get appName and userID from context.
		appName, userID, err := getAppAndUserFromContext(ctx)
		if err != nil {
			return UpdateMemoryResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to get app and user from context: %v", err),
			}, fmt.Errorf("failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req.MemoryID == "" {
			return UpdateMemoryResponse{
				Success: false,
				Message: "Memory ID is required",
			}, errors.New("memory ID is required")
		}

		if req.Memory == "" {
			return UpdateMemoryResponse{
				Success: false,
				Message: "Memory content is required",
			}, errors.New("memory content is required")
		}

		// Ensure topics is never nil.
		if req.Topics == nil {
			req.Topics = []string{}
		}

		memoryKey := memory.Key{AppName: appName, UserID: userID, MemoryID: req.MemoryID}
		err = service.UpdateMemory(ctx, memoryKey, req.Memory, req.Topics)
		if err != nil {
			return UpdateMemoryResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to update memory: %v", err),
			}, fmt.Errorf("failed to update memory: %v", err)
		}

		return UpdateMemoryResponse{
			Success:  true,
			Message:  "Memory updated successfully",
			MemoryID: req.MemoryID,
			Memory:   req.Memory,
			Topics:   req.Topics,
		}, nil
	}

	return function.NewFunctionTool(
		updateFunc,
		function.WithName(UpdateToolName),
		function.WithDescription("Update an existing memory. Use this tool to modify stored information about the user."),
	)
}

// NewDeleteMemoryTool creates a function tool for deleting memories.
func NewDeleteMemoryTool(service memory.Service) tool.CallableTool {
	deleteFunc := func(ctx context.Context, req DeleteMemoryRequest) (DeleteMemoryResponse, error) {
		// Get appName and userID from context.
		appName, userID, err := getAppAndUserFromContext(ctx)
		if err != nil {
			return DeleteMemoryResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to get app and user from context: %v", err),
			}, fmt.Errorf("failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req.MemoryID == "" {
			return DeleteMemoryResponse{
				Success: false,
				Message: "Memory ID is required",
			}, errors.New("memory ID is required")
		}

		memoryKey := memory.Key{AppName: appName, UserID: userID, MemoryID: req.MemoryID}
		err = service.DeleteMemory(ctx, memoryKey)
		if err != nil {
			return DeleteMemoryResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to delete memory: %v", err),
			}, fmt.Errorf("failed to delete memory: %v", err)
		}

		return DeleteMemoryResponse{
			Success:  true,
			Message:  "Memory deleted successfully",
			MemoryID: req.MemoryID,
		}, nil
	}

	return function.NewFunctionTool(
		deleteFunc,
		function.WithName(DeleteToolName),
		function.WithDescription("Delete a specific memory. Use this tool to remove outdated or incorrect information about the user."),
	)
}

// NewClearMemoryTool creates a function tool for clearing all memories.
func NewClearMemoryTool(service memory.Service) tool.CallableTool {
	clearFunc := func(ctx context.Context, _ struct{}) (ClearMemoryResponse, error) {
		// Get appName and userID from context.
		appName, userID, err := getAppAndUserFromContext(ctx)
		if err != nil {
			return ClearMemoryResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to get app and user from context: %v", err),
			}, fmt.Errorf("failed to get app and user from context: %v", err)
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		err = service.ClearMemories(ctx, userKey)
		if err != nil {
			return ClearMemoryResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to clear memories: %v", err),
			}, fmt.Errorf("failed to clear memories: %v", err)
		}

		return ClearMemoryResponse{
			Success: true,
			Message: "All memories cleared successfully",
		}, nil
	}

	return function.NewFunctionTool(
		clearFunc,
		function.WithName(ClearToolName),
		function.WithDescription("Clear all memories for the user. Use this tool to reset the user's memory completely."),
	)
}

// NewSearchMemoryTool creates a function tool for searching memories.
func NewSearchMemoryTool(service memory.Service) tool.CallableTool {
	searchFunc := func(ctx context.Context, req SearchMemoryRequest) (SearchMemoryResponse, error) {
		// Get appName and userID from context.
		appName, userID, err := getAppAndUserFromContext(ctx)
		if err != nil {
			return SearchMemoryResponse{
				Success: false,
				Query:   "",
				Results: []Result{},
				Count:   0,
			}, fmt.Errorf("failed to get app and user from context: %v", err)
		}

		// Validate input.
		if req.Query == "" {
			return SearchMemoryResponse{
				Success: false,
				Query:   "",
				Results: []Result{},
				Count:   0,
			}, errors.New("query is required")
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		memories, err := service.SearchMemories(ctx, userKey, req.Query)
		if err != nil {
			return SearchMemoryResponse{
				Success: false,
				Query:   req.Query,
				Results: []Result{},
				Count:   0,
			}, fmt.Errorf("failed to search memories: %v", err)
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

		return SearchMemoryResponse{
			Success: true,
			Query:   req.Query,
			Results: results,
			Count:   len(results),
		}, nil
	}

	return function.NewFunctionTool(
		searchFunc,
		function.WithName(SearchToolName),
		function.WithDescription("Search for relevant memories about the user. Use this tool to find stored information that matches the query."),
	)
}

// NewLoadMemoryTool creates a function tool for loading memories.
func NewLoadMemoryTool(service memory.Service) tool.CallableTool {
	loadFunc := func(ctx context.Context, req LoadMemoryRequest) (LoadMemoryResponse, error) {
		// Get appName and userID from context.
		appName, userID, err := getAppAndUserFromContext(ctx)
		if err != nil {
			return LoadMemoryResponse{
				Success: false,
				Limit:   0,
				Results: []Result{},
				Count:   0,
			}, fmt.Errorf("failed to get app and user from context: %v", err)
		}

		// Set default limit.
		limit := req.Limit
		if limit <= 0 {
			limit = 10
		}

		userKey := memory.UserKey{AppName: appName, UserID: userID}
		memories, err := service.ReadMemories(ctx, userKey, limit)
		if err != nil {
			return LoadMemoryResponse{
				Success: false,
				Limit:   limit,
				Results: []Result{},
				Count:   0,
			}, fmt.Errorf("failed to load memories: %v", err)
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

		return LoadMemoryResponse{
			Success: true,
			Limit:   limit,
			Results: results,
			Count:   len(results),
		}, nil
	}

	return function.NewFunctionTool(
		loadFunc,
		function.WithName(LoadToolName),
		function.WithDescription("Load recent memories about the user. Use this tool to retrieve stored information to provide context for the conversation."),
	)
}
