//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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

// mcpToolResult wraps MCP tool result for backward compatibility.
// It marshals as the Content slice, but provides Meta access via GetMeta().
type mcpToolResult struct {
	Content []mcp.Content
	Meta    map[string]any
}

// MarshalJSON implements json.Marshaler.
// It marshals only the Content slice for backward compatibility.
func (r *mcpToolResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Content)
}

// GetMeta returns the metadata from the tool result.
func (r *mcpToolResult) GetMeta() map[string]any {
	return r.Meta
}

// mcpTool implements the Tool interface for MCP tools.
type mcpTool struct {
	mcpToolRef     *mcp.Tool
	inputSchema    *tool.Schema
	outputSchema   *tool.Schema
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

	// Convert MCP output schema to inner Schema.
	if mcpToolData.OutputSchema != nil {
		mcpTool.outputSchema = convertMCPSchemaToSchema(mcpToolData.OutputSchema)
	}

	return mcpTool
}

// Call implements the Tool interface.
func (t *mcpTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	log.DebugContext(
		ctx,
		"Calling MCP tool",
		"name",
		t.mcpToolRef.Name,
	)

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
// Returns a wrapped result that marshals as Content for backward compatibility.
func (t *mcpTool) callOnce(ctx context.Context, arguments map[string]any) (any, error) {
	result, err := t.sessionManager.callTool(ctx, t.mcpToolRef.Name, arguments)
	if err != nil {
		return nil, err
	}
	// Wrap for backward compatibility: marshals as Content array, but Meta is accessible
	return &mcpToolResult{
		Content: result.Content,
		Meta:    result.Meta,
	}, nil
}

// Declaration implements the Tool interface.
func (t *mcpTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         t.mcpToolRef.Name,
		Description:  t.mcpToolRef.Description,
		InputSchema:  t.inputSchema,
		OutputSchema: t.outputSchema,
	}
}
