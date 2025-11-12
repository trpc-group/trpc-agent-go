//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides a simple SSE MCP server that logs received HTTP headers.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	fmt.Println("🚀 Starting SSE MCP Server with HTTP Header Logging...")
	fmt.Println("Server will log all received HTTP headers")
	fmt.Println("Listening on http://localhost:3000/mcp")
	fmt.Println(strings.Repeat("=", 50))

	// Create MCP server
	server := mcp.NewServer(
		"header-demo-server",
		"1.0.0",
		mcp.WithServerAddress(":3000"),
		mcp.WithServerPath("/mcp"),
	)

	// Register tools
	registerWeatherTool(server)
	registerEchoTool(server)

	// Wrap the server's HTTP handler to log headers
	originalHandler := server.HTTPHandler()
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logHeaders(r)
		originalHandler.ServeHTTP(w, r)
	})

	fmt.Println("✅ Server ready")
	log.Fatal(http.ListenAndServe(":3000", nil))
}

func logHeaders(r *http.Request) {
	fmt.Printf("\n📥 Received %s %s\n", r.Method, r.URL.Path)

	// Read body content
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		fmt.Printf("Error reading body: %v\n", err)
	} else {
		fmt.Printf("Body: %s\n", string(bodyBytes))
		// Restore the body for subsequent handlers
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	fmt.Println("Headers:")

	// Log custom headers
	customHeaders := []string{
		"X-Request-ID",
		"X-User-ID",
		"X-Session-ID",
		"X-Timestamp",
	}

	for _, header := range customHeaders {
		if value := r.Header.Get(header); value != "" {
			fmt.Printf("   %s: %s\n", header, value)
		}
	}

	// Log other interesting headers
	if value := r.Header.Get("User-Agent"); value != "" {
		fmt.Printf("   User-Agent: %s\n", value)
	}
	if value := r.Header.Get("Content-Type"); value != "" {
		fmt.Printf("   Content-Type: %s\n", value)
	}

	fmt.Println()
}

func registerWeatherTool(server *mcp.Server) {
	tool := mcp.NewTool("get_weather",
		mcp.WithDescription("Get the current weather for a location"),
		mcp.WithString("location",
			mcp.Required(),
			mcp.Description("The city and state, e.g. San Francisco, CA"),
		),
	)

	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		location, _ := req.Params.Arguments["location"].(string)
		if location == "" {
			location = "Unknown"
		}

		weather := fmt.Sprintf("Weather in %s: Sunny, 72°F", location)
		return mcp.NewTextResult(weather), nil
	}

	server.RegisterTool(tool, handler)
	fmt.Printf("✅ Registered tool: %s\n", tool.Name)
}

func registerEchoTool(server *mcp.Server) {
	tool := mcp.NewTool("echo",
		mcp.WithDescription("Echo back the input message"),
		mcp.WithString("message",
			mcp.Required(),
			mcp.Description("The message to echo back"),
		),
	)

	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		message, _ := req.Params.Arguments["message"].(string)
		if message == "" {
			message = "(empty)"
		}

		return mcp.NewTextResult(fmt.Sprintf("Echo: %s", message)), nil
	}

	server.RegisterTool(tool, handler)
	fmt.Printf("✅ Registered tool: %s\n", tool.Name)
}
