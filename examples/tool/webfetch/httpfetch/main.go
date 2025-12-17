//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates interactive chat using HTTP web fetch tool.
// The tool provides the ability to fetch and extract content from web pages.
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
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch"
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("🚀 HTTP Web Fetch Chat Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: web_fetch\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &webFetchChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// webFetchChat manages the conversation with web fetch capability.
type webFetchChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session.
func (c *webFetchChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and web fetch tool.
func (c *webFetchChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create HTTP web fetch tool.
	fetchTool := httpfetch.NewTool(
		httpfetch.WithMaxContentLength(50000),       // Limit single URL content to 50KB
		httpfetch.WithMaxTotalContentLength(150000), // Limit total content to 150KB
	)

	// Create LLM agent with web fetch tool.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "web-fetch-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with the ability to fetch and analyze web content."),
		llmagent.WithInstruction("Use the web_fetch tool to retrieve and extract content from web pages. "+
			"You can fetch multiple URLs at once (up to 20). "+
			"The tool converts HTML to markdown for better readability and supports various text formats including JSON, XML, plain text, etc. "+
			"When analyzing web content, provide clear summaries and extract key information relevant to the user's question."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{fetchTool}),
	)

	// Create runner.
	appName := "web-fetch-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("web-fetch-session-%d", time.Now().Unix())

	fmt.Printf("✅ Web fetch chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *webFetchChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	// Print welcome message with examples.
	fmt.Println("💡 Try asking questions like:")
	fmt.Println("   - Summarize the content from https://example.com")
	fmt.Println("   - Fetch and compare https://site1.com and https://site2.com")
	fmt.Println("   - What's on the homepage of https://news.ycombinator.com")
	fmt.Println("   - Extract the main points from https://blog.example.com/article")
	fmt.Println("   - Get the API documentation from https://api.example.com/docs")
	fmt.Println()
	fmt.Println("ℹ️  Note: The tool supports HTML, JSON, XML, and plain text formats")
	fmt.Println()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle exit command.
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		// Process the user message.
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange.
func (c *webFetchChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with web fetch tool visualization.
func (c *webFetchChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {

		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect and display tool calls.
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🌐 Web fetch initiated:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Fetching web content...\n")
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					// Truncate long tool responses for display
					content := strings.TrimSpace(choice.Message.Content)
					if len(content) > 200 {
						content = content[:200] + "..."
					}
					fmt.Printf("✅ Fetch result (ID: %s): %s\n",
						choice.Message.ToolID,
						content)
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]

			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}

		// Check if this is the final event.
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// intPtr returns a pointer to the given int.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float64.
func floatPtr(f float64) *float64 {
	return &f
}
