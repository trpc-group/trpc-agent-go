//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates user context propagation and authorization using Invocation State.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	userID    = flag.String("user-id", "alice", "User ID for authentication")
	role      = flag.String("role", "user", "User role (admin, user, guest)")
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Println("🔐 User Context and Authorization Example")
	fmt.Println("This example demonstrates how to use Invocation State for user context propagation and tool authorization.")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println()

	// Create the example.
	example := &userContextExample{
		userID: *userID,
		role:   *role,
	}

	// Setup and run.
	if err := example.run(); err != nil {
		log.Fatalf("Example failed: %v", err)
	}
}

// userContextExample demonstrates user context propagation and authorization.
type userContextExample struct {
	runner    runner.Runner
	userID    string
	role      string
	sessionID string
}

// run executes the user context example.
func (e *userContextExample) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := e.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer e.runner.Close()

	// Run the example.
	return e.runExample(ctx)
}

// setup creates the runner with LLM agent and tools.
func (e *userContextExample) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(*modelName)

	// Create tools with different permission requirements.
	tools := e.createTools()

	// Create callbacks for authorization and audit.
	agentCallbacks := e.createAgentCallbacks()
	toolCallbacks := e.createToolCallbacks()

	// Create LLM agent.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
	}
	llmAgent := llmagent.New(
		"file-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An AI assistant that helps with file operations"),
		llmagent.WithInstruction("Help users with file operations. Always check permissions before performing operations."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithToolCallbacks(toolCallbacks),
		llmagent.WithAgentCallbacks(agentCallbacks),
	)

	// Create runner.
	e.runner = runner.NewRunner(
		"user-context-example",
		llmAgent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)

	// Setup session.
	e.sessionID = fmt.Sprintf("session-%s-%d", e.userID, time.Now().Unix())

	fmt.Printf("✅ Example ready!\n")
	fmt.Printf("   User: %s\n", e.userID)
	fmt.Printf("   Role: %s\n", e.role)
	fmt.Printf("   Permissions: %v\n", getPermissionsForRole(e.role))
	fmt.Printf("   Session: %s\n", e.sessionID)
	fmt.Println()

	return nil
}

// createTools creates the tools for the agent.
func (e *userContextExample) createTools() []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			e.readFile,
			function.WithName(toolReadFile),
			function.WithDescription("Read the contents of a file (requires read permission)"),
		),
		function.NewFunctionTool(
			e.writeFile,
			function.WithName(toolWriteFile),
			function.WithDescription("Write content to a file (requires write permission)"),
		),
		function.NewFunctionTool(
			e.deleteFile,
			function.WithName(toolDeleteFile),
			function.WithDescription("Delete a file (requires admin role)"),
		),
		function.NewFunctionTool(
			e.listFiles,
			function.WithName(toolListFiles),
			function.WithDescription("List files in a directory (no special permission required)"),
		),
	}
}

// runExample executes the interactive chat session.
func (e *userContextExample) runExample(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 User Context Example - Interactive Chat")
	fmt.Println()
	fmt.Println("Try these commands:")
	fmt.Println("   - list files in the current directory")
	fmt.Println("   - read the config.txt file")
	fmt.Println("   - write 'hello' to test.txt")
	fmt.Println("   - delete the old_data.txt file")
	fmt.Println()
	fmt.Println("Special commands:")
	fmt.Println("   /switch <user-id> <role>  - Switch to a different user")
	fmt.Println("   /whoami                   - Show current user info")
	fmt.Println("   /exit                     - End the conversation")
	fmt.Println()

	for {
		fmt.Printf("👤 You (%s, %s): ", e.userID, e.role)
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle special commands.
		if strings.HasPrefix(userInput, "/") {
			if e.handleCommand(userInput) {
				return nil
			}
			continue
		}

		// Process the user message.
		if err := e.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// handleCommand handles special commands.
func (e *userContextExample) handleCommand(cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}

	switch parts[0] {
	case "/exit":
		fmt.Println("👋 Goodbye!")
		return true

	case "/whoami":
		fmt.Printf("Current user: %s\n", e.userID)
		fmt.Printf("Role: %s\n", e.role)
		fmt.Printf("Permissions: %v\n", getPermissionsForRole(e.role))
		fmt.Println()

	case "/switch":
		if len(parts) != 3 {
			fmt.Println("Usage: /switch <user-id> <role>")
			fmt.Println("Example: /switch bob admin")
			fmt.Println()
			return false
		}
		oldUser := e.userID
		oldRole := e.role
		e.userID = parts[1]
		e.role = parts[2]
		e.sessionID = fmt.Sprintf("session-%s-%d", e.userID, time.Now().Unix())
		fmt.Printf("🔄 Switched user:\n")
		fmt.Printf("   From: %s (%s)\n", oldUser, oldRole)
		fmt.Printf("   To:   %s (%s)\n", e.userID, e.role)
		fmt.Printf("   New session: %s\n", e.sessionID)
		fmt.Println()

	default:
		fmt.Printf("Unknown command: %s\n", parts[0])
		fmt.Println()
	}

	return false
}

// processMessage handles a single message exchange.
func (e *userContextExample) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := e.runner.Run(ctx, e.userID, e.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return e.processResponse(eventChan)
}

// processResponse handles the response from the agent.
func (e *userContextExample) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			return nil
		}

		// Handle tool calls.
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			fmt.Printf("\n🔧 Tool calls:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s\n", toolCall.Function.Name)
			}
			fmt.Println()
		}

		// Handle content.
		if len(event.Response.Choices) > 0 && event.Response.Choices[0].Message.Content != "" {
			fmt.Print(event.Response.Choices[0].Message.Content)
		}

		// Check if this is the final event.
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
