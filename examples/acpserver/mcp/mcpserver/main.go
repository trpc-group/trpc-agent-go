//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides a stdio MCP server for the ACP MCP example.
package main

import (
	"context"
	"fmt"
	"os"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

const greetingEnv = "ACP_EXAMPLE_MCP_GREETING"

func main() {
	server := mcp.NewStdioServer(
		"acp-stdio-mcp-example",
		"1.0.0",
		mcp.WithStdioServerLogger(mcp.GetDefaultLogger()),
	)
	server.RegisterTool(
		mcp.NewTool(
			"greet",
			mcp.WithDescription(
				"Return the greeting configured in "+greetingEnv+".",
			),
		),
		greet,
	)
	fmt.Fprintln(os.Stderr, "Starting ACP stdio MCP example server")
	if err := server.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func greet(
	_ context.Context,
	_ *mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	greeting := os.Getenv(greetingEnv)
	if greeting == "" {
		return nil, fmt.Errorf("%s is not set", greetingEnv)
	}
	return mcp.NewTextResult(greeting), nil
}
