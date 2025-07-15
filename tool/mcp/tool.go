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

package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// mcpTool implements the Tool interface for MCP tools.
type mcpTool struct {
	mcpToolRef     *mcp.Tool
	inputSchema    *tool.Schema
	sessionManager *mcpSessionManager
}

// newMCPTool creates a new MCP tool wrapper.
func newMCPTool(mcpToolData mcp.Tool, sessionManager *mcpSessionManager) *mcpTool {
	mcpTool := &mcpTool{
		mcpToolRef:     &mcpToolData,
		sessionManager: sessionManager,
	}

	// Convert MCP input schema to inner Schema.
	if mcpToolData.InputSchema != nil {
		mcpTool.inputSchema = convertMCPSchemaToSchema(mcpToolData.InputSchema)
	}

	return mcpTool
}

// Call implements the Tool interface.
func (t *mcpTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	log.Debug("Calling MCP tool", "name", t.mcpToolRef.Name)

	// Parse raw arguments.
	var rawArguments map[string]any
	if len(jsonArgs) > 0 {
		if err := json.Unmarshal(jsonArgs, &rawArguments); err != nil {
			return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
		}
	} else {
		rawArguments = make(map[string]any)
	}

	return t.callOnce(ctx, rawArguments)
}

// callOnce performs a single call to the MCP tool.
func (t *mcpTool) callOnce(ctx context.Context, arguments map[string]any) (any, error) {
	content, err := t.sessionManager.callTool(ctx, t.mcpToolRef.Name, arguments)
	if err != nil {
		return nil, err
	}

	return content, nil
}

// Declaration implements the Tool interface.
func (t *mcpTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.mcpToolRef.Name,
		Description: t.mcpToolRef.Description,
		InputSchema: t.inputSchema,
	}
}
