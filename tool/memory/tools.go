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
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// NewMemoryTools returns memory tools with options.
func NewMemoryTools(memoryService memory.Service, appName string, userID string, opts ...MemoryToolsOption) []tool.Tool {
	options := NewMemoryToolsOptions(memoryService, appName, userID)

	// Apply all options.
	for _, opt := range opts {
		opt(options)
	}

	return options.BuildTools()
}
