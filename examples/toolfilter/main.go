//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates per-run tool filtering functionality.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	openaigo "github.com/openai/openai-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	// Parse command line arguments
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	filterMode := flag.String("filter", "", "Filter mode: exclude-demo, include-demo, per-agent, or empty for no filter")
	flag.Parse()

	fmt.Printf("🚀 Multi-Agent Tool Filtering Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	if *filterMode != "" {
		fmt.Printf("Filter Mode: %s\n", *filterMode)
	}
	fmt.Printf("Enter 'exit' to end the conversation\n")
	fmt.Printf("Available agents: math-agent (calculator, text_tool), time-agent (time_tool, text_tool)\n")
	fmt.Println(strings.Repeat("=", 60))

	// Create and run chat system
	chat := &toolFilterDemo{
		modelName:  *modelName,
		filterMode: *filterMode,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Demo failed: %v", err)
	}
}

// toolFilterDemo manages the multi-agent tool filtering demonstration
type toolFilterDemo struct {
	modelName  string
	runner     runner.Runner
	userID     string
	sessionID  string
	filterMode string
}

// run starts the demo
func (c *toolFilterDemo) run() error {
	ctx := context.Background()

	// Setup runner
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat
	return c.startChat(ctx)
}

// setup creates a runner with multiple sub-agents
func (c *toolFilterDemo) setup(_ context.Context) error {
	// Create OpenAI model with request callback to show tools
	modelInstance := openai.New(
		c.modelName,
		openai.WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			// Print tools that will be sent to the model
			if len(req.Tools) > 0 {
				toolNames := make([]string, 0, len(req.Tools))
				for _, t := range req.Tools {
					toolNames = append(toolNames, t.Function.Name)
				}
				fmt.Printf("📋 Tools in OpenAI request: %v\n", toolNames)
			} else {
				fmt.Printf("📋 Tools in OpenAI request: [none]\n")
			}
		}),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	// Create math-agent with calculator and text tools
	mathAgent := llmagent.New(
		"math-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A math specialist agent that can perform calculations and text processing"),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{
			createCalculatorTool(),
			createTextTool(),
		}),
	)

	// Create time-agent with time and text tools
	timeAgent := llmagent.New(
		"time-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A time specialist agent that can provide time information and text processing"),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{
			createTimeTool(),
			createTextTool(),
		}),
	)

	// Create coordinator agent with sub-agents
	coordinatorAgent := llmagent.New(
		"coordinator",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A coordinator that can delegate tasks to specialized agents"),
		llmagent.WithInstruction("You are a coordinator. Delegate math tasks to math-agent and time queries to time-agent."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithSubAgents([]agent.Agent{mathAgent, timeAgent}),
	)

	// Create runner
	appName := "multi-agent-tool-filter-demo"
	c.runner = runner.NewRunner(appName, coordinatorAgent)

	// Set identifiers
	c.userID = "user"
	c.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())

	fmt.Printf("✅ Multi-agent demo ready! Session ID: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop
func (c *toolFilterDemo) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Try these commands:")
	fmt.Println("   [Math] Calculate 2+3")
	fmt.Println("   [Time] What time is it?")
	fmt.Println("   [Both] Convert 'Hello' to uppercase (both agents have text_tool)")
	if c.filterMode != "" {
		fmt.Println("\n   Tool filtering is active:")
		switch c.filterMode {
		case "exclude-demo":
			fmt.Println("   - Demo: tool.NewExcludeToolNamesFilter")
			fmt.Println("   - Excludes: text_tool globally")
			fmt.Println("   - Result: All agents can use calculator/time_tool, but NOT text_tool")
		case "include-demo":
			fmt.Println("   - Demo: tool.NewIncludeToolNamesFilter")
			fmt.Println("   - Includes only: calculator, time_tool")
			fmt.Println("   - Result: All agents can ONLY use calculator and time_tool")
		case "per-agent":
			fmt.Println("   - Demo: Custom FilterFunc with agent.InvocationFromContext")
			fmt.Println("   - math-agent: only calculator")
			fmt.Println("   - time-agent: only time_tool")
			fmt.Println("   - Shows how to implement agent-specific filtering")
		}
	} else {
		fmt.Println("\n   No filtering - all agents can use all their tools")
	}
	fmt.Println()

	for {
		fmt.Print("👤 User: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle exit command
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		// Process user message
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

// processMessage processes a single message exchange
func (c *toolFilterDemo) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Build run options with tool filtering
	var runOpts []agent.RunOption

	// Apply tool filtering based on filter mode
	if c.filterMode != "" {
		switch c.filterMode {
		case "exclude-demo":
			// Demo: Exclude specific tools using NewExcludeToolNamesFilter
			filter := tool.NewExcludeToolNamesFilter("text_tool")
			runOpts = append(runOpts, agent.WithToolFilter(filter))
			fmt.Printf("� Exclude filter: text_tool\n")
			fmt.Printf("   (Framework tools like transfer_to_agent are auto-included)\n")
		case "include-demo":
			// Demo: Include only specific tools using NewIncludeToolNamesFilter
			filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
			runOpts = append(runOpts, agent.WithToolFilter(filter))
			fmt.Printf("✅ Include filter: calculator, time_tool\n")
			fmt.Printf("   (Framework tools like transfer_to_agent are auto-included)\n")
		case "per-agent":
			// Demo: Custom per-agent filtering using FilterFunc
			// This demonstrates how users can implement agent-specific filtering
			filter := c.createPerAgentFilter()
			runOpts = append(runOpts, agent.WithToolFilter(filter))
			fmt.Printf("🎯 Per-agent filter:\n")
			fmt.Printf("   - math-agent: only calculator\n")
			fmt.Printf("   - time-agent: only time_tool\n")
			fmt.Printf("   (Framework tools are auto-included)\n")
		}
	}

	// Run agent through runner
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, runOpts...)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response
	return c.processStreamingResponse(eventChan)
}

// createPerAgentFilter creates a custom filter that applies different rules per agent.
// This demonstrates how users can implement agent-specific filtering logic.
//
// Key insight: The FilterFunc receives context.Context, which contains the invocation
// information. You can use agent.InvocationFromContext(ctx) to get the current agent name
// and apply agent-specific filtering rules.
func (c *toolFilterDemo) createPerAgentFilter() tool.FilterFunc {
	// Define per-agent allowed tools
	agentAllowedTools := map[string]map[string]bool{
		"math-agent": {
			"calculator": true,
			// text_tool is NOT in this list, so it will be filtered out
		},
		"time-agent": {
			"time_tool": true,
			// text_tool is NOT in this list, so it will be filtered out
		},
	}

	return func(ctx context.Context, t tool.Tool) bool {
		declaration := t.Declaration()
		if declaration == nil {
			return false
		}
		toolName := declaration.Name

		// Get the current agent name from invocation context
		// The context contains invocation information during tool filtering
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil {
			// If no invocation context, allow all tools (fallback)
			return true
		}

		agentName := inv.AgentName

		// Check if this tool is allowed for the current agent
		allowedTools, exists := agentAllowedTools[agentName]
		if !exists {
			// If agent not in the map, allow all tools (fallback)
			return true
		}

		// Return true only if the tool is in the agent's allowed list
		return allowedTools[toolName]
	}
}

// processStreamingResponse processes streaming response
func (c *toolFilterDemo) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var fullContent string

	for event := range eventChan {
		// Handle errors
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect and display tool calls
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			fmt.Printf("\n🔧 Tool calls:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   → %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Arguments: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n⚡ Executing...\n\n🤖 Assistant: ")
			continue
		}

		// Process streaming content
		if len(event.Response.Choices) > 0 && event.Response.Choices[0].Delta.Content != "" {
			content := event.Response.Choices[0].Delta.Content
			fmt.Print(content)
			fullContent += content
		}

		// Check if this is the final event
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// Calculator tool
type calculatorRequest struct {
	Expression string `json:"expression" jsonschema:"description=Mathematical expression to calculate,required"`
}

type calculatorResponse struct {
	Result  float64 `json:"result"`
	Message string  `json:"message"`
}

func createCalculatorTool() tool.CallableTool {
	return function.NewFunctionTool(
		calculateExpression,
		function.WithName("calculator"),
		function.WithDescription("Perform mathematical calculations"),
	)
}

func calculateExpression(_ context.Context, req calculatorRequest) (calculatorResponse, error) {
	result, err := evaluateBasicExpression(req.Expression)
	if err != nil {
		return calculatorResponse{Result: 0, Message: fmt.Sprintf("Error: %v", err)}, err
	}
	return calculatorResponse{Result: result, Message: fmt.Sprintf("Result: %g", result)}, nil
}

func evaluateBasicExpression(expr string) (float64, error) {
	expr = strings.ReplaceAll(expr, " ", "")
	if num, err := strconv.ParseFloat(expr, 64); err == nil {
		return num, nil
	}
	// Simple evaluation for demo (supports +, -, *, /)
	if strings.Contains(expr, "+") {
		parts := strings.Split(expr, "+")
		if len(parts) == 2 {
			left, err1 := strconv.ParseFloat(parts[0], 64)
			right, err2 := strconv.ParseFloat(parts[1], 64)
			if err1 == nil && err2 == nil {
				return left + right, nil
			}
		}
	}
	return 0, fmt.Errorf("unsupported expression")
}

// Time tool
type timeRequest struct {
	Operation string `json:"operation" jsonschema:"description=Operation: current, date, or timestamp,required"`
}

type timeResponse struct {
	Result string `json:"result"`
}

func createTimeTool() tool.CallableTool {
	return function.NewFunctionTool(
		getTimeInfo,
		function.WithName("time_tool"),
		function.WithDescription("Get current time information"),
	)
}

func getTimeInfo(_ context.Context, req timeRequest) (timeResponse, error) {
	now := time.Now()
	var result string
	switch req.Operation {
	case "current":
		result = now.Format("2006-01-02 15:04:05")
	case "date":
		result = now.Format("2006-01-02")
	case "timestamp":
		result = fmt.Sprintf("%d", now.Unix())
	default:
		result = now.Format("2006-01-02 15:04:05")
	}
	return timeResponse{Result: result}, nil
}

// Text tool
type textRequest struct {
	Text      string `json:"text" jsonschema:"description=Text to process,required"`
	Operation string `json:"operation" jsonschema:"description=Operation: uppercase or lowercase,required"`
}

type textResponse struct {
	Result string `json:"result"`
}

func createTextTool() tool.CallableTool {
	return function.NewFunctionTool(
		processText,
		function.WithName("text_tool"),
		function.WithDescription("Process text (uppercase, lowercase)"),
	)
}

func processText(_ context.Context, req textRequest) (textResponse, error) {
	var result string
	switch req.Operation {
	case "uppercase":
		result = strings.ToUpper(req.Text)
	case "lowercase":
		result = strings.ToLower(req.Text)
	default:
		result = req.Text
	}
	return textResponse{Result: result}, nil
}

// Helper functions
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

var _ = math.Sqrt // Avoid unused import error
