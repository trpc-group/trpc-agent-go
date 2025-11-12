//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use HTTPBeforeRequest to dynamically set HTTP headers for MCP tool calls.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

// Context keys for passing dynamic data
type contextKey string

const (
	requestIDKey contextKey = "request-id"
	userIDKey    contextKey = "user-id"
	sessionIDKey contextKey = "session-id"
	timestampKey contextKey = "timestamp"
)

func main() {
	fmt.Printf("🚀 MCP HTTP Headers Example\n")
	fmt.Printf("This example shows how to dynamically set HTTP headers for MCP tool calls\n")
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Println(strings.Repeat("=", 50))

	chat := &httpHeadersChat{
		modelName: "deepseek-chat",
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

type httpHeadersChat struct {
	modelName  string
	runner     runner.Runner
	userID     string
	sessionID  string
	mcpToolSet *mcp.ToolSet
}

func (c *httpHeadersChat) run() error {
	ctx := context.Background()

	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	return c.startChat(ctx)
}

func (c *httpHeadersChat) setup(ctx context.Context) error {
	// Generate IDs first (needed for setup context)
	c.userID = "user-123"
	c.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())

	// Create OpenAI model
	modelInstance := openai.New(c.modelName)

	// Create HTTPBeforeRequest function that extracts headers from context
	beforeRequest := func(ctx context.Context, req *http.Request) error {
		// Extract request ID from context
		if requestID, ok := ctx.Value(requestIDKey).(string); ok {
			req.Header.Set("X-Request-ID", requestID)
			fmt.Printf("📤 Setting header: X-Request-ID = %s\n", requestID)
		}

		// Extract user ID from context
		if userID, ok := ctx.Value(userIDKey).(string); ok {
			req.Header.Set("X-User-ID", userID)
			fmt.Printf("📤 Setting header: X-User-ID = %s\n", userID)
		}

		// Extract session ID from context
		if sessionID, ok := ctx.Value(sessionIDKey).(string); ok {
			req.Header.Set("X-Session-ID", sessionID)
			fmt.Printf("📤 Setting header: X-Session-ID = %s\n", sessionID)
		}

		// Extract timestamp from context
		if timestamp, ok := ctx.Value(timestampKey).(string); ok {
			req.Header.Set("X-Timestamp", timestamp)
			fmt.Printf("📤 Setting header: X-Timestamp = %s\n", timestamp)
		}

		// Log the request
		fmt.Printf("📤 HTTP %s %s\n", req.Method, req.URL.Path)

		return nil
	}

	// Create SSE MCP toolset with HTTPBeforeRequest
	c.mcpToolSet = mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "streamable",
			ServerURL: "http://localhost:3000/mcp",
			Timeout:   10 * time.Second,
		},
		mcp.WithMCPOptions(
			tmcp.WithHTTPBeforeRequest(beforeRequest),
		),
	)

	// Create context with values for setup phase
	// This ensures that all MCP requests (initialize, tools/list, GET SSE) have headers
	setupRequestID := fmt.Sprintf("req-setup-%d", time.Now().UnixNano())
	setupTimestamp := time.Now().Format(time.RFC3339)

	setupCtx := context.WithValue(ctx, requestIDKey, setupRequestID)
	setupCtx = context.WithValue(setupCtx, userIDKey, c.userID)
	setupCtx = context.WithValue(setupCtx, sessionIDKey, c.sessionID)
	setupCtx = context.WithValue(setupCtx, timestampKey, setupTimestamp)

	fmt.Printf("\n🔧 Setup phase - initializing MCP connection:\n")
	fmt.Printf("   Request ID: %s\n", setupRequestID)
	fmt.Printf("   User ID: %s\n", c.userID)
	fmt.Printf("   Session ID: %s\n", c.sessionID)
	fmt.Printf("   Timestamp: %s\n\n", setupTimestamp)

	// Get tools from the toolset (using context with values)
	// This will trigger: initialize, initialized notification, tools/list, GET SSE
	// All these requests will have the custom headers from setupCtx
	tools := c.mcpToolSet.Tools(setupCtx)
	if len(tools) == 0 {
		return fmt.Errorf("no tools available from MCP server")
	}

	fmt.Printf("\n✅ Loaded %d tools from MCP server\n", len(tools))
	fmt.Println()

	// Create LLM agent with generation config
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "http-headers-demo"
	agent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A demo agent showing HTTP header propagation"),
		llmagent.WithInstruction("Use the available MCP tools when appropriate. "+
			"Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		// IMPORTANT: Use WithTools instead of WithToolSets
		//
		// Two approaches for integrating MCP tools:
		//
		// Approach A (used here): WithTools(tools)
		//   - Manually call toolSet.Tools(ctx) with your own context
		//   - Gives you full control over the context for initialize/tools/list
		//   - All POST requests (initialize, tools/list, tools/call) have dynamic headers
		//   - Recommended when you need per-request authentication or tracing
		//
		// Approach B (alternative): WithToolSets([]tool.ToolSet{toolSet})
		//   - Agent calls toolSet.Tools(context.Background()) internally
		//   - initialize/tools/list won't have dynamic headers from context
		//   - Only tools/call will have dynamic headers
		//   - Suitable when you only need static headers (e.g., API keys)
		//   - Can combine with WithRequestHeader for static headers
		//
		// This example uses Approach A to demonstrate full dynamic header control.
		llmagent.WithTools(tools),
	)

	// Create runner
	appName := "http-headers-example"
	c.runner = runner.NewRunner(appName, agent)

	return nil
}

func (c *httpHeadersChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	c.mcpToolSet.Close()
	return nil
}

func (c *httpHeadersChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Generate a unique request ID for this message
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	timestamp := time.Now().Format(time.RFC3339)

	// Add dynamic values to context
	// These will be extracted by the HTTPBeforeRequest function
	ctx = context.WithValue(ctx, requestIDKey, requestID)
	ctx = context.WithValue(ctx, userIDKey, c.userID)
	ctx = context.WithValue(ctx, sessionIDKey, c.sessionID)
	ctx = context.WithValue(ctx, timestampKey, timestamp)

	fmt.Printf("\n🔄 Processing message with context:\n")
	fmt.Printf("   Request ID: %s\n", requestID)
	fmt.Printf("   User ID: %s\n", c.userID)
	fmt.Printf("   Session ID: %s\n", c.sessionID)
	fmt.Printf("   Timestamp: %s\n\n", timestamp)

	// Run the agent - context will flow through to HTTPBeforeRequest
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	return c.processStreamingResponse(eventChan)
}

func (c *httpHeadersChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect tool calls
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("\n🔧 Tool calls initiated:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n")
		}

		// Detect tool responses
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ Tool response (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				fmt.Println()
				continue
			}
		}

		// Process streaming content
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]
			if choice.Delta.Content != "" {
				if !assistantStarted && toolCallsDetected {
					fmt.Print("🤖 Assistant: ")
				}
				assistantStarted = true
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}
	}

	if assistantStarted {
		fmt.Println()
	}

	return nil
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
