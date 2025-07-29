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
	mu          sync.RWMutex        // mu is the mutex for the tool set.
	service     memory.Service      // service is the memory service.
	tools       []tool.CallableTool // tools is the list of memory tools.
	initialized bool                // initialized is a flag to indicate if the tool set is initialized.
}

// NewMemoryToolSet creates a new memory tool set.
// The tools will get appName and userID from the agent invocation context at runtime.
func NewMemoryToolSet(service memory.Service) *ToolSet {
	return &ToolSet{
		service:     service,
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
		newMemoryTool(mts.service, addMemoryFunction, AddToolName,
			"Add a new memory for the user. Use this when you want to remember important information "+
				"about the user that could personalize future interactions."),
		newMemoryTool(mts.service, updateMemoryFunction, UpdateToolName,
			"Update an existing memory for the user."),
		newMemoryTool(mts.service, deleteMemoryFunction, DeleteToolName,
			"Delete a specific memory for the user."),
		newMemoryTool(mts.service, clearMemoriesFunction, ClearToolName,
			"Clear all memories for the user."),
		newMemoryTool(mts.service, searchMemoriesFunction, SearchToolName,
			"Search for memories related to a specific query."),
		newMemoryTool(mts.service, loadMemoriesFunction, LoadToolName,
			"Load recent memories for the user."),
	}
	mts.initialized = true
}
