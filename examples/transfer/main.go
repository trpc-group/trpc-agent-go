//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates agent transfer functionality with sub-agents.
// This example shows how to create a main agent with multiple specialized
// sub-agents and use the transfer_to_agent tool to delegate tasks.
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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	alog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	debug := flag.Bool("debug", false, "Enable debug logging and verbose event traces")
	endInvocation := flag.Bool("end-invocation", false, "Enable end parent invocation after transfer")
	flag.Parse()

	fmt.Printf("🔄 Agent Transfer Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available sub-agents: math-agent, weather-agent, research-agent, time-agent\n")
	fmt.Printf("Use natural language to request tasks - the coordinator will transfer to appropriate agents\n")
	fmt.Println(strings.Repeat("=", 70))

	// Enable debug logging if requested.
	if *debug {
		alog.SetLevel(alog.LevelDebug)
		fmt.Println("🪵 Debug logging enabled (internal flow logs at DEBUG level)")
	}

	// Create and run the chat.
	chat := &transferChat{
		modelName:                  *modelName,
		debug:                      *debug,
		endInvocationAfterTransfer: *endInvocation,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// transferChat manages the conversation with agent transfer functionality.
type transferChat struct {
	modelName                  string
	runner                     runner.Runner
	userID                     string
	sessionID                  string
	debug                      bool
	endInvocationAfterTransfer bool
}

// run starts the interactive chat session.
func (c *transferChat) run() error {
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

// setup creates the runner with main agent and sub-agents.
func (c *transferChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create sub-agents.
	mathAgent := c.createMathAgent(modelInstance)
	weatherAgent := c.createWeatherAgent(modelInstance)
	researchAgent := c.createResearchAgent(modelInstance)
	timeAgent := c.createTimeAgent(modelInstance)

	// Create coordinator agent with sub-agents.
	coordinatorAgent := c.createCoordinatorAgent(modelInstance, []agent.Agent{
		mathAgent,
		weatherAgent,
		researchAgent,
		timeAgent,
	})

	// Create runner.
	appName := "agent-transfer-demo"
	c.runner = runner.NewRunner(
		appName,
		coordinatorAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("transfer-session-%d", time.Now().Unix())

	fmt.Printf("✅ Agent transfer system ready! Session: %s\n\n", c.sessionID)
	return nil
}

// createCoordinatorAgent creates the main coordinator agent with sub-agents.
func (c *transferChat) createCoordinatorAgent(modelInstance model.Model, subAgents []agent.Agent) agent.Agent {
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.6),
		Stream:      true,
	}

	return llmagent.New(
		"coordinator-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A coordinator agent that delegates tasks to specialized sub-agents"),
		llmagent.WithInstruction(`You are a coordinator agent that helps users by delegating tasks to specialized sub-agents.
Available sub-agents:
- math-agent: For mathematical calculations, equations, and numerical problems
- weather-agent: For weather information, forecasts, and weather-related recommendations
- research-agent: For research, information gathering, and general knowledge questions
- time-agent: For time calculations, duration differences, and temporal analysis

When a user asks a question:
1. Analyze what type of task it is
2. Use transfer_to_agent to delegate to the appropriate specialist
3. If unsure, ask the user for clarification or handle simple queries yourself

Always explain why you're transferring to a specific agent.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithSubAgents(subAgents),
		llmagent.WithEndInvocationAfterTransfer(c.endInvocationAfterTransfer),
	)
}

// startChat runs the interactive conversation loop.
func (c *transferChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Try different types of requests:")
	fmt.Println("   • Math: 'Calculate the power of 2 to 10'")
	fmt.Println("   • Weather: 'What's the weather like in Tokyo?'")
	fmt.Println("   • Research: 'Tell me about renewable energy trends'")
	fmt.Println("   • Time: 'Calculate time difference between 2023-01-01T00:00:00Z and 2023-01-02T12:30:45Z'")
	fmt.Println("   • General: 'Hello, what can you help me with?'")
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
func (c *transferChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response with transfer awareness.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with transfer visualization.
func (c *transferChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🎯 Coordinator: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
		currentAgent      string = "coordinator-agent"
		sawTransferEvent  bool
		debugWarned       bool
	)

	for event := range eventChan {
		if c.debug {
			// Verbose event trace for debugging transfer ordering/author
			var obj = event.Object
			var author = event.Author
			var partial = false
			var done = false
			if event.Response != nil {
				partial = event.Response.IsPartial
				done = event.Response.Done
			}
			fmt.Printf("\n[DBG] event id=%s obj=%s author=%s partial=%t done=%t branch=%s\n", event.ID, obj, author, partial, done, event.Branch)
			if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
				fmt.Printf("[DBG]  tool_calls: ")
				for _, tc := range event.Response.Choices[0].Message.ToolCalls {
					fmt.Printf("%s ", tc.Function.Name)
				}
				fmt.Println()
			}
		}

		if err := c.handleTransferEvent(event, &fullContent, &toolCallsDetected, &assistantStarted, &currentAgent); err != nil {
			return err
		}

		if event.Object == model.ObjectTypeTransfer {
			sawTransferEvent = true
		}

		// Safety check: after transfer, no parent/coordinator events should appear
		if c.debug && sawTransferEvent && event.Author == "coordinator-agent" && event.Object != model.ObjectTypeTransfer {
			fmt.Printf("\n[DBG][WARN] Received coordinator event after transfer — this should not happen\n")
			debugWarned = true
		}
	}

	fmt.Println() // Final newline
	if c.debug && sawTransferEvent && !debugWarned {
		fmt.Println("[DBG] OK: No parent chunks observed after transfer; ordering looks correct")
	}
	return nil
}

// handleTransferEvent processes a single event from the transfer system.
func (c *transferChat) handleTransferEvent(
	event *event.Event,
	fullContent *string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	currentAgent *string,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle agent transfers.
	if c.handleTransfer(event, currentAgent, assistantStarted) {
		return nil
	}

	// Handle tool calls.
	if c.handleToolCalls(event, toolCallsDetected, assistantStarted) {
		return nil
	}

	// Handle content.
	c.handleContent(event, fullContent, toolCallsDetected, assistantStarted, currentAgent)

	// Handle tool responses.
	c.handleToolResponses(event)

	return nil
}

// handleTransfer processes agent transfer events.
func (c *transferChat) handleTransfer(event *event.Event, currentAgent *string, assistantStarted *bool) bool {
	if event.Object == model.ObjectTypeTransfer {
		fmt.Printf("\n🔄 Transfer Event: %s\n", event.Response.Choices[0].Message.Content)
		*currentAgent = c.getAgentFromTransfer(event)
		*assistantStarted = false
		return true
	}
	return false
}

// handleToolCalls detects and displays tool calls.
func (c *transferChat) handleToolCalls(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}

		if c.isTransferTool(event.Response.Choices[0].Message.ToolCalls[0]) {
			fmt.Printf("🔄 Initiating transfer...\n")
		} else {
			c.displayToolCalls(event)
		}
		return true
	}
	return false
}

// displayToolCalls shows tool call information.
func (c *transferChat) displayToolCalls(event *event.Event) {
	fmt.Printf("🔧 %s executing tools:\n", c.getAgentIcon(event.Author))
	for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
		fmt.Printf("   • %s", toolCall.Function.Name)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf(" (%s)", string(toolCall.Function.Arguments))
		}
		fmt.Printf("\n")
	}
}

// handleContent processes streaming content.
func (c *transferChat) handleContent(
	event *event.Event,
	fullContent *string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	currentAgent *string,
) {
	if len(event.Response.Choices) > 0 {
		content := c.extractContent(event.Response.Choices[0])

		if content != "" {
			c.displayContent(event, content, fullContent, toolCallsDetected, assistantStarted, currentAgent)
		}
	}
}

// extractContent extracts content from the choice.
func (c *transferChat) extractContent(choice model.Choice) string {
	// Only use delta content to avoid duplication in streaming responses
	if choice.Delta.Content != "" {
		return choice.Delta.Content
	}
	return ""
}

// displayContent handles content display logic.
func (c *transferChat) displayContent(
	event *event.Event,
	content string,
	fullContent *string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	currentAgent *string,
) {
	if !*assistantStarted && !*toolCallsDetected {
		c.displayAgentHeader(event, currentAgent)
		*assistantStarted = true
	} else if *toolCallsDetected && !*assistantStarted {
		if !c.isTransferResponse(event) {
			fmt.Printf("%s %s: ", c.getAgentIcon(event.Author), c.getAgentDisplayName(event.Author))
			*assistantStarted = true
		}
	}

	if !c.isTransferResponse(event) {
		fmt.Print(content)
		*fullContent += content
	}
}

// displayAgentHeader shows the agent header when starting content.
func (c *transferChat) displayAgentHeader(event *event.Event, currentAgent *string) {
	if event.Author != *currentAgent {
		fmt.Printf("\n%s %s: ", c.getAgentIcon(event.Author), c.getAgentDisplayName(event.Author))
		*currentAgent = event.Author
	}
}

// handleToolResponses processes tool response completion.
func (c *transferChat) handleToolResponses(event *event.Event) {
	if event.IsToolResultResponse() && len(event.Response.Choices[0].Message.ToolCalls) > 0 &&
		!c.isTransferTool(event.Response.Choices[0].Message.ToolCalls[0]) {
		fmt.Printf("   ✅ Tool completed\n")
	}
}

// Helper functions for display formatting.
func (c *transferChat) getAgentIcon(agentName string) string {
	switch agentName {
	case "coordinator-agent":
		return "🎯"
	case "math-agent":
		return "🧮"
	case "weather-agent":
		return "🌤️"
	case "research-agent":
		return "🔍"
	case "time-agent":
		return "⏰"
	default:
		return "🤖"
	}
}

func (c *transferChat) getAgentDisplayName(agentName string) string {
	switch agentName {
	case "coordinator-agent":
		return "Coordinator"
	case "math-agent":
		return "Math Specialist"
	case "weather-agent":
		return "Weather Specialist"
	case "research-agent":
		return "Research Specialist"
	case "time-agent":
		return "Time Specialist"
	default:
		return agentName
	}
}

func (c *transferChat) getAgentFromTransfer(event *event.Event) string {
	// Parse the transfer event to determine target agent.
	if event.Response != nil && len(event.Response.Choices) > 0 {
		content := event.Response.Choices[0].Message.Content
		if strings.Contains(content, "math-agent") {
			return "math-agent"
		} else if strings.Contains(content, "weather-agent") {
			return "weather-agent"
		} else if strings.Contains(content, "research-agent") {
			return "research-agent"
		} else if strings.Contains(content, "time-agent") {
			return "time-agent"
		}
	}
	return "coordinator-agent"
}

func (c *transferChat) isTransferTool(toolCall model.ToolCall) bool {
	return toolCall.Function.Name == transfer.TransferToolName
}

func (c *transferChat) isTransferResponse(event *event.Event) bool {
	return len(event.Response.Choices) > 0 && event.Response.Choices[0].Message.ToolID != ""
}

// Helper functions.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
