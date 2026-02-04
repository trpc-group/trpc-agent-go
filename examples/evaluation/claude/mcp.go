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
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

const (
	// claudeMCPServerName is the local MCP server name registered via the Claude CLI.
	claudeMCPServerName = "eva_eval_example"
)

// calculatorArgs defines the MCP calculator tool arguments.
type calculatorArgs struct {
	Operation *string  `json:"operation"`
	A         *float64 `json:"a"`
	B         *float64 `json:"b"`
}

// calculatorResult defines the MCP calculator tool response payload.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

// startLocalMCPSSEServer starts a local SSE MCP server and returns its endpoint and a shutdown function.
func startLocalMCPSSEServer(_ context.Context) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	mux := http.NewServeMux()

	srv := &http.Server{}
	server := mcp.NewSSEServer(
		"EVA Eval Example MCP Server",
		"1.0.0",
		mcp.WithHTTPServer(srv),
	)
	mux.Handle("/", server)
	srv.Handler = mux

	calculatorTool := mcp.NewTool("calculator",
		mcp.WithDescription("Simple arithmetic calculator that computes operation(a, b)."),
		mcp.WithString("operation", mcp.Required(), mcp.Description("Operation like add, subtract, multiply, or divide.")),
		mcp.WithNumber("a", mcp.Required(), mcp.Description("First operand.")),
		mcp.WithNumber("b", mcp.Required(), mcp.Description("Second operand.")),
	)
	server.RegisterTool(calculatorTool, handleMCPCalculator)
	server.RegisterNotificationHandler("notifications/initialized", func(ctx context.Context, notification *mcp.JSONRPCNotification) error {
		return nil
	})

	go func() {
		_ = srv.Serve(listener)
	}()

	hostPort := listener.Addr().String()
	sseURL := "http://" + hostPort + "/sse"

	return sseURL, func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}, nil
}

// handleMCPCalculator handles the MCP calculator tool by validating arguments and returning a JSON result.
func handleMCPCalculator(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args calculatorArgs
	raw, err := json.Marshal(req.Params.Arguments)
	if err != nil {
		return nil, fmt.Errorf("marshal args: %w", err)
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if args.Operation == nil || strings.TrimSpace(*args.Operation) == "" {
		return nil, fmt.Errorf("missing required parameter: operation")
	}
	if args.A == nil {
		return nil, fmt.Errorf("missing required parameter: a")
	}
	if args.B == nil {
		return nil, fmt.Errorf("missing required parameter: b")
	}

	op := strings.TrimSpace(*args.Operation)
	a := *args.A
	b := *args.B

	result, err := calculate(op, a, b)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(calculatorResult{
		Operation: op,
		A:         a,
		B:         b,
		Result:    result,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(data)),
		},
	}, nil
}

// calculate executes a basic arithmetic operation on a and b.
func calculate(operation string, a, b float64) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "add", "+":
		return a + b, nil
	case "subtract", "-":
		return a - b, nil
	case "multiply", "*":
		return a * b, nil
	case "divide", "/":
		if b == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return a / b, nil
	default:
		return 0, fmt.Errorf("unsupported operation: %s", operation)
	}
}
