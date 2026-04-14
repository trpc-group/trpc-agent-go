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
	"log"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	server := mcp.NewStdioServer("broker-stdio-test", "1.0.0")

	echoTool := mcp.NewTool(
		"echo",
		mcp.WithDescription("Echo text."),
		mcp.WithString("text", mcp.Required(), mcp.Description("Text to echo.")),
	)
	server.RegisterTool(echoTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, _ := req.Params.Arguments["text"].(string)
		return mcp.NewTextResult("Echo: " + text), nil
	})

	addTool := mcp.NewTool(
		"add",
		mcp.WithDescription("Add two numbers."),
		mcp.WithNumber("a", mcp.Required(), mcp.Description("First number.")),
		mcp.WithNumber("b", mcp.Required(), mcp.Description("Second number.")),
	)
	server.RegisterTool(addTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, _ := req.Params.Arguments["a"].(float64)
		b, _ := req.Params.Arguments["b"].(float64)
		return mcp.NewTextResult(fmt.Sprintf("%g", a+b)), nil
	})

	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}
