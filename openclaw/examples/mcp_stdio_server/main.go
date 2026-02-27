//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main starts a tiny MCP STDIO server for OpenClaw demos.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	server := mcp.NewStdioServer("openclaw-mcp-demo", "1.0.0")

	server.RegisterTool(
		mcp.NewTool(
			"echo",
			mcp.WithDescription("Echoes back the input JSON arguments."),
		),
		func(ctx context.Context, req *mcp.CallToolRequest) (
			*mcp.CallToolResult,
			error,
		) {
			body, _ := json.Marshal(req.Params.Arguments)
			return mcp.NewTextResult(string(body)), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool(
			"add",
			mcp.WithDescription("Adds two numbers: a + b."),
		),
		func(ctx context.Context, req *mcp.CallToolRequest) (
			*mcp.CallToolResult,
			error,
		) {
			var args struct {
				A float64 `json:"a"`
				B float64 `json:"b"`
			}
			body, _ := json.Marshal(req.Params.Arguments)
			if err := json.Unmarshal(body, &args); err != nil {
				return nil, err
			}
			return mcp.NewTextResult(fmt.Sprintf("%g", args.A+args.B)), nil
		},
	)

	log.Printf("Starting MCP STDIO demo server...")
	if err := server.Start(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
