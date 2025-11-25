//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a minimal multi-turn chat powered by Runner.
// It focuses on core control flow with an in-memory session backend so the
// example stays self-contained and easy to run.
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

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName      = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming      = flag.Bool("streaming", true, "Enable streaming mode for responses")
	enableParallel = flag.Bool("enable-parallel", false, "Enable parallel tool execution (default: false, serial execution)")
	variant        = flag.String("variant", "openai", "Name of the variant to use when calling the OpenAI provider")
)

func main() {
	flag.Parse()

	fmt.Printf("🚀 Runner quickstart: multi-turn chat with tools\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Parallel tools: %t\n", *enableParallel)
	fmt.Printf("Session backend: in-memory (simple demo)\n")
	fmt.Printf("Type '/exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator, current_time\n")
	fmt.Println(strings.Repeat("=", 50))

	chat := &multiTurnChat{
		modelName: *modelName,
		streaming: *streaming,
		variant:   *variant,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

// multiTurnChat manages the conversation loop for the demo.
type multiTurnChat struct {
	modelName string
	streaming bool
	runner    runner.Runner
	userID    string
	sessionID string
	variant   string
}

func (c *multiTurnChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	return c.startChat(ctx)
}

// setup builds the runner with a model, tools, and the in-memory session store.
func (c *multiTurnChat) setup(_ context.Context) error {
	modelInstance := openai.New(c.modelName, openai.WithVariant(openai.Variant(c.variant)))

	sessionService := sessioninmemory.NewSessionService()

	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)
	timeTool := function.NewFunctionTool(
		c.getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current time and date for a specific timezone"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	appName := "runner-quickstart"
	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools."),
		llmagent.WithInstruction("Use tools when helpful for calculations or time queries. Stay conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
		llmagent.WithEnableParallelTools(*enableParallel),
	)

	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	c.userID = "demo-user"
	c.sessionID = fmt.Sprintf("demo-session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)
	return nil
}

func (c *multiTurnChat) startChat(ctx context.Context) error {
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
		if userInput == "/exit" {
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
	return nil
}

func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)
	requestID := uuid.New().String()
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.WithRequestID(requestID))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return c.processResponse(eventChan)
}

func (c *multiTurnChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for evt := range eventChan {
		if err := c.handleEvent(evt, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
			return err
		}
		if evt.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

func (c *multiTurnChat) handleEvent(
	evt *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) error {
	if evt.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
		return nil
	}
	if c.handleToolCalls(evt, toolCallsDetected, assistantStarted) {
		return nil
	}
	if c.handleToolResponses(evt) {
		return nil
	}
	c.handleContent(evt, toolCallsDetected, assistantStarted, fullContent)
	return nil
}

func (c *multiTurnChat) handleToolCalls(
	evt *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if evt.Response != nil && len(evt.Response.Choices) > 0 && len(evt.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}
		fmt.Printf("🔧 Callable tool calls initiated:\n")
		for _, toolCall := range evt.Response.Choices[0].Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Printf("\n🔄 Executing tools...\n")
		return true
	}
	return false
}

func (c *multiTurnChat) handleToolResponses(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	hasToolResponse := false
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ Callable tool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
			hasToolResponse = true
		}
	}
	return hasToolResponse
}

func (c *multiTurnChat) handleContent(
	evt *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	content := c.extractContent(evt.Response.Choices[0])
	if content == "" {
		return
	}
	c.displayContent(content, toolCallsDetected, assistantStarted, fullContent)
}

func (c *multiTurnChat) extractContent(choice model.Choice) string {
	if c.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

func (c *multiTurnChat) displayContent(
	content string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Printf("\n🤖 Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
	*fullContent += content
}
