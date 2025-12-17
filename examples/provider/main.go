//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use Provider with LLM agents.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/hunyuan"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	providerName        = flag.String("provider", "openai", "Name of the provider to use, openai/anthropic/ollama/hunyuan")
	modelName           = flag.String("model", "deepseek-chat", "Name of the model to use")
	isStream            = flag.Bool("stream", true, "Whether to stream the response")
	apiKey              = flag.String("api-key", "", "Override the provider API key")
	baseURL             = flag.String("base-url", "", "Override the provider base URL")
	secretID            = flag.String("secret-id", "", "Set secret id for hunyuan")
	secretKey           = flag.String("secret-key", "", "Set secret key for hunyuan")
	channelBufferSize   = flag.Int("channel-buffer", 0, "Override provider channel buffer size")
	enableTokenTailor   = flag.Bool("token-tailor", false, "Enable provider token tailoring")
	maxTailorInputToken = flag.Int("max-input-tokens", 0, "Maximum input tokens when token tailoring is enabled")
)

func main() {
	// Parse command line flags.
	flag.Parse()
	fmt.Printf("🧠 Provider Agent Demo\n")
	fmt.Printf("Provider: %s\n", *providerName)
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Stream: %t\n", *isStream)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator\n")
	fmt.Printf("The agent will perform mathematical calculations\n")
	fmt.Println(strings.Repeat("=", 60))

	// Create and run the chat.
	chat := &providerChat{
		providerName:      *providerName,
		modelName:         *modelName,
		streaming:         *isStream,
		apiKey:            *apiKey,
		baseURL:           *baseURL,
		channelBufferSize: *channelBufferSize,
		tokenTailoring:    *enableTokenTailor,
		maxInputTokens:    *maxTailorInputToken,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// providerChat manages the conversation with provider.
type providerChat struct {
	providerName      string
	modelName         string
	streaming         bool
	apiKey            string
	baseURL           string
	channelBufferSize int
	tokenTailoring    bool
	maxInputTokens    int
	runner            runner.Runner
	userID            string
	sessionID         string
}

// run starts the interactive chat session.
func (c *providerChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and Provider.
func (c *providerChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance, err := provider.Model(
		c.providerName,
		c.modelName,
		provider.WithAPIKey(c.apiKey),
		provider.WithBaseURL(c.baseURL),
		provider.WithChannelBufferSize(c.channelBufferSize),
		provider.WithEnableTokenTailoring(c.tokenTailoring),
		provider.WithMaxInputTokens(c.maxInputTokens),
		provider.WithHunyuanOption(hunyuan.WithSecretId(*secretID), hunyuan.WithSecretKey(*secretKey)),
	)
	if err != nil {
		return fmt.Errorf("failed to create model: %w", err)
	}

	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform mathematical calculations"),
	)

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(3000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	agentName := "provider-agent"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A Provider agent that can perform mathematical calculations"),
		llmagent.WithInstruction("You are a helpful calculator assistant. "+
			"You can perform mathematical calculations. Operations supported: add, subtract, multiply, divide, power."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
	)

	// Create runner.
	appName := "provider-demo"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("provider-session-%d", time.Now().Unix())

	fmt.Printf("✅ Provider agent ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *providerChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Try asking complex questions that require planning, like:")
	fmt.Println("   • 'If I invest $1000 at a 5% annual rate compounded yearly for 10 years, how much will I have?'")
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
func (c *providerChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response with Provider awareness.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with Provider visualization.
func (c *providerChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🧠 Agent: ")

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
		if !event.Response.IsValidContent() {
			continue
		}

		// Detect and display tool calls.
		if event.Response.IsToolResultResponse() {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 Executing tools:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s", toolCall.Function.Name)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf(" (%s)", string(toolCall.Function.Arguments))
				}
				fmt.Printf("\n")
			}
			for _, toolCall := range event.Response.Choices[0].Delta.ToolCalls {
				fmt.Printf("   • %s", toolCall.Function.Name)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf(" (%s)", string(toolCall.Function.Arguments))
				}
				fmt.Printf("\n")
			}
			continue
		}

		// Process text content with Provider awareness.
		if len(event.Response.Choices) > 0 {
			var content string
			if event.Response.Choices[0].Message.Content != "" {
				content = event.Response.Choices[0].Message.Content
			} else if event.Response.Choices[0].Delta.Content != "" {
				content = event.Response.Choices[0].Delta.Content
			}

			if content != "" {
				if !assistantStarted && !toolCallsDetected {
					assistantStarted = true
				} else if toolCallsDetected && !assistantStarted {
					fmt.Print("🧠 Agent: ")
					assistantStarted = true
				}

				fmt.Print(content)
				fullContent += content
			}
		}

		// Handle tool responses.
		if event.IsToolResultResponse() {
			fmt.Printf("\n   ✅ Tool completed\n")
		}
	}

	fmt.Println() // End the response
	return nil
}

// calculate performs mathematical calculations.
func (c *providerChat) calculate(_ context.Context, args *calcArgs) (*calcResult, error) {
	var result float64

	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return nil, errors.New("division by zero")
		}
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return nil, errors.New("unsupported operation")
	}

	return &calcResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

type calcArgs struct {
	Operation string  `json:"operation" description:"The operation to perform,enum=add,enum=subtract,enum=multiply,enum=divide,enum=power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calcResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

// Helper functions.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
