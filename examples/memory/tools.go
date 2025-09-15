//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	toolmemory "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Custom clear tool with enhanced logging.
func customClearMemoryTool() tool.Tool {
	clearFunc := func(ctx context.Context, _ *toolmemory.ClearMemoryRequest) (*toolmemory.ClearMemoryResponse, error) {
		fmt.Println("🧹 [Custom Clear Tool] Clearing memories with extra sparkle... ✨")

		// Get memory service from invocation context.
		memSvc, err := toolmemory.GetMemoryServiceFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("custom clear tool: %w", err)
		}

		// Resolve app and user from context.
		appName, userID, err := toolmemory.GetAppAndUserFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("custom clear tool: %w", err)
		}

		// Clear all memories for the user.
		if err := memSvc.ClearMemories(ctx, memory.UserKey{AppName: appName, UserID: userID}); err != nil {
			return nil, fmt.Errorf("custom clear tool: failed to clear memories: %w", err)
		}

		return &toolmemory.ClearMemoryResponse{
			Message: "🎉 All memories cleared successfully with custom magic! ✨",
		}, nil
	}

	return function.NewFunctionTool(
		clearFunc,
		function.WithName(memory.ClearToolName),
		function.WithDescription("🧹 Custom clear tool: Clear all memories for the user with extra sparkle! ✨"),
	)
}
