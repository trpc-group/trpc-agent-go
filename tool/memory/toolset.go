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
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolSet implements tool.ToolSet for memory tools.
// It provides lazy initialization of memory tools.
type ToolSet struct {
	mu          sync.RWMutex
	service     memory.Service
	tools       []tool.CallableTool
	initialized bool
}

// NewMemoryToolSet creates a new memory tool set.
func NewMemoryToolSet(service memory.Service) *ToolSet {
	return &ToolSet{
		service:     service,
		tools:       nil,
		initialized: false,
	}
}

// Tools returns the memory tools.
// This method implements lazy initialization.
func (ts *ToolSet) Tools(ctx context.Context) []tool.CallableTool {
	ts.mu.RLock()
	if ts.initialized {
		tools := ts.tools
		ts.mu.RUnlock()
		return tools
	}
	ts.mu.RUnlock()

	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Double-check after acquiring write lock.
	if ts.initialized {
		return ts.tools
	}

	// Create memory tools using the new function-based approach.
	ts.tools = []tool.CallableTool{
		NewAddMemoryTool(ts.service),
		NewUpdateMemoryTool(ts.service),
		NewDeleteMemoryTool(ts.service),
		NewClearMemoryTool(ts.service),
		NewSearchMemoryTool(ts.service),
		NewLoadMemoryTool(ts.service),
	}

	ts.initialized = true
	return ts.tools
}

// Close cleans up resources used by the tool set.
func (ts *ToolSet) Close() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.tools = nil
	ts.initialized = false
	return nil
}
