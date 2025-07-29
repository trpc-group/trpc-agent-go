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

package memory

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolSet implements the ToolSet interface for memory tools.
type ToolSet struct {
	service     memory.Service
	appName     string
	userID      string
	tools       []tool.CallableTool
	mu          sync.RWMutex
	initialized bool
}

// NewMemoryToolSet creates a new memory tool set.
func NewMemoryToolSet(service memory.Service, appName string, userID string) *ToolSet {
	return &ToolSet{
		service:     service,
		appName:     appName,
		userID:      userID,
		tools:       nil,
		initialized: false,
	}
}

// Tools implements the ToolSet interface.
func (mts *ToolSet) Tools(ctx context.Context) []tool.CallableTool {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	if !mts.initialized {
		mts.initializeTools()
	}

	return mts.tools
}

// Close implements the ToolSet interface.
func (mts *ToolSet) Close() error {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	mts.tools = nil
	mts.initialized = false
	return nil
}

// initializeTools creates the memory tools.
func (mts *ToolSet) initializeTools() {
	mts.tools = []tool.CallableTool{
		newMemoryTool(mts.service, mts.appName, mts.userID, addMemoryFunction, AddToolName,
			"Add a new memory for the user. Use this when you want to remember important information "+
				"about the user that could personalize future interactions."),
		newMemoryTool(mts.service, mts.appName, mts.userID, updateMemoryFunction, UpdateToolName,
			"Update an existing memory for the user."),
		newMemoryTool(mts.service, mts.appName, mts.userID, deleteMemoryFunction, DeleteToolName,
			"Delete a specific memory for the user."),
		newMemoryTool(mts.service, mts.appName, mts.userID, clearMemoriesFunction, ClearToolName,
			"Clear all memories for the user."),
		newMemoryTool(mts.service, mts.appName, mts.userID, searchMemoriesFunction, SearchToolName,
			"Search for memories related to a specific query."),
		newMemoryTool(mts.service, mts.appName, mts.userID, loadMemoriesFunction, LoadToolName,
			"Load recent memories for the user."),
	}
	mts.initialized = true
}
