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

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ClearTool is a tool for clearing all memories.
type ClearTool struct {
	memoryService memory.Service
	appName       string
	userID        string
}

// NewClearTool creates a new ClearTool.
func NewClearTool(memoryService memory.Service, appName string, userID string) *ClearTool {
	return &ClearTool{
		memoryService: memoryService,
		appName:       appName,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *ClearTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "memory_clear",
		Description: "Clear all memories for the user. Use this when the user asks for all memories to be forgotten " +
			"or when starting fresh. This will remove all stored information about the user.",
		InputSchema: &tool.Schema{
			Type:       "object",
			Properties: map[string]*tool.Schema{},
		},
	}
}

// Call executes the memory clear tool.
func (m *ClearTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.memoryService == nil {
		return nil, errors.New("memory service not available")
	}

	// Create user key.
	userKey := memory.UserKey{
		AppName: m.appName,
		UserID:  m.userID,
	}

	// Clear all memories for the user.
	err := m.memoryService.ClearMemories(ctx, userKey)
	if err != nil {
		return nil, errors.New("failed to clear memories")
	}

	return ClearMemoryResponse{
		Success: true,
		Message: "All memories cleared successfully",
	}, nil
}
