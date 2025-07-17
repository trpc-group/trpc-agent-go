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

package main

import (
	"context"
	"fmt"
	"log"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	server := mcp.NewStdioServer("simple-stdio-server", "1.0.0",
		mcp.WithStdioServerLogger(mcp.GetDefaultLogger()),
	)

	// Register echo tool
	echoTool := mcp.NewTool("echo",
		mcp.WithDescription("Simple echo tool that returns the input message with an optional prefix"),
		mcp.WithString("message", mcp.Required(), mcp.Description("The message to echo")),
		mcp.WithString("prefix", mcp.Description("Optional prefix, default is 'Echo: '")),
	)
	server.RegisterTool(echoTool, handleEcho)

	// Register add tool
	addTool := mcp.NewTool("add",
		mcp.WithDescription("Simple addition tool that adds two numbers"),
		mcp.WithNumber("a", mcp.Required(), mcp.Description("First number")),
		mcp.WithNumber("b", mcp.Required(), mcp.Description("Second number")),
	)
	server.RegisterTool(addTool, handleAdd)

	log.Printf("Starting Simple STDIO MCP Server...")
	log.Printf("Available tools: echo, add")
	log.Printf("Using simplified implementation")

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// handleEcho handles the echo tool.
func handleEcho(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse message parameter.
	message := ""
	if msgArg, ok := req.Params.Arguments["message"]; ok {
		if msgStr, ok := msgArg.(string); ok {
			message = msgStr
		}
	}
	if message == "" {
		return nil, fmt.Errorf("missing required parameter: message")
	}

	// Parse prefix parameter.
	prefix := "Echo: "
	if prefixArg, ok := req.Params.Arguments["prefix"]; ok {
		if prefixStr, ok := prefixArg.(string); ok && prefixStr != "" {
			prefix = prefixStr
		}
	}

	result := prefix + message

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(result),
		},
	}, nil
}

// handleAdd handles the add tool.
func handleAdd(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse a parameter.
	var a float64
	if aArg, ok := req.Params.Arguments["a"]; ok {
		if aFloat, ok := aArg.(float64); ok {
			a = aFloat
		} else if aInt, ok := aArg.(int); ok {
			a = float64(aInt)
		} else {
			return nil, fmt.Errorf("invalid parameter 'a': must be a number")
		}
	} else {
		return nil, fmt.Errorf("missing required parameter: a")
	}

	// Parse b parameter.
	var b float64
	if bArg, ok := req.Params.Arguments["b"]; ok {
		if bFloat, ok := bArg.(float64); ok {
			b = bFloat
		} else if bInt, ok := bArg.(int); ok {
			b = float64(bInt)
		} else {
			return nil, fmt.Errorf("invalid parameter 'b': must be a number")
		}
	} else {
		return nil, fmt.Errorf("missing required parameter: b")
	}

	result := a + b
	resultText := fmt.Sprintf("%.2f + %.2f = %.2f", a, b, result)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(resultText),
		},
	}, nil
}
