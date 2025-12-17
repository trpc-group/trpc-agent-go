//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates interactive chat using Gemini web fetch tool.
// The tool uses Gemini's URL Context feature for server-side web fetching and analysis.
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
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch/geminifetch"
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	geminiModel := flag.String("gemini-model", "gemini-2.5-flash", "Gemini model for web fetching")
	flag.Parse()

	fmt.Printf("🚀 Gemini Web Fetch Chat Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Gemini Fetch Model: %s\n", *geminiModel)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: gemini_web_fetch\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &geminiWebFetchChat{
		modelName:   *modelName,
		geminiModel: *geminiModel,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// geminiWebFetchChat manages the conversation with Gemini web fetch capability.
type geminiWebFetchChat struct {
	modelName   string
	geminiModel string
	runner      runner.Runner
	userID      string
	sessionID   string
}

// run starts the interactive chat session.
func (c *geminiWebFetchChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and Gemini web fetch tool.
func (c *geminiWebFetchChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create Gemini web fetch tool.
	fetchTool, err := geminifetch.NewTool(c.geminiModel)
	if err != nil {
		return fmt.Errorf("failed to create gemini fetch tool: %w", err)
	}

	// Create LLM agent with Gemini web fetch tool.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "gemini-web-fetch-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with Gemini's server-side web fetching capability."),
		llmagent.WithInstruction("Use the gemini_web_fetch tool to retrieve and analyze web content. "+
			"Simply include URLs naturally in your prompt and Gemini will automatically fetch and analyze them on the server side. "+
			"You can include up to 20 URLs in a single request. "+
			"The tool leverages Gemini's URL Context feature for intelligent content extraction and analysis. "+
			"When analyzing web content, provide clear summaries and extract key information relevant to the user's question."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{fetchTool}),
	)

	// Create runner.
	appName := "gemini-web-fetch-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("gemini-web-fetch-session-%d", time.Now().Unix())

	fmt.Printf("✅ Gemini web fetch chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *geminiWebFetchChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	// Print welcome message with examples.
	fmt.Println("💡 Try asking questions like:")
	fmt.Println("   - Summarize https://example.com")
	fmt.Println("   - Compare https://site1.com and https://site2.com")
	fmt.Println("   - What's the main content of https://news.ycombinator.com")
	fmt.Println("   - Analyze the article at https://blog.example.com/post")
	fmt.Println("   - Extract key points from https://ai.google.dev/gemini-api/docs/url-context")
	fmt.Println()
	fmt.Println("ℹ️  Note: URLs are automatically detected and fetched by Gemini's server")
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
func (c *geminiWebFetchChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with Gemini web fetch tool visualization.
func (c *geminiWebFetchChat) processStreamingResponse(eventChan <-chan *event.Event) error {
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
			fmt.Printf("🌐 Gemini web fetch initiated:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Prompt: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Gemini fetching and analyzing content...\n")
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
