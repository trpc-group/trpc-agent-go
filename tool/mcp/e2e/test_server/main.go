//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Simple STDIO server for integration testing.
package main

import (
	"context"
	"log"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	// Create STDIO server with simple tools for testing.
	server := mcp.NewStdioServer("filter-test-server", "1.0.0")

	// Register 3 tools for filter testing
	server.RegisterTool(
		mcp.NewTool("tool1", mcp.WithDescription("First test tool")),
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewTextResult("tool1 executed"), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("tool2", mcp.WithDescription("Second test tool")),
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewTextResult("tool2 executed"), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("tool3", mcp.WithDescription("Third test tool")),
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewTextResult("tool3 executed"), nil
		},
	)

	log.Printf("Starting filter integration test STDIO server...")
	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
