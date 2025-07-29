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

// GetMemoryTools returns all memory tools.
func GetMemoryTools(memoryService memory.Service, appName string, userID string) []tool.Tool {
	return []tool.Tool{
		NewMemoryAddTool(memoryService, appName, userID),
		NewMemoryUpdateTool(memoryService, appName, userID),
		NewMemoryDeleteTool(memoryService, appName, userID),
		NewMemoryClearTool(memoryService, appName, userID),
		NewMemorySearchTool(memoryService, appName, userID),
		NewMemoryLoadTool(memoryService, appName, userID),
	}
}
