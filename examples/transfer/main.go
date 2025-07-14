//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("🔄 Agent Transfer Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available sub-agents: math-agent, weather-agent, research-agent\n")
	fmt.Printf("Use natural language to request tasks - the coordinator will transfer to appropriate agents\n")
	fmt.Println(strings.Repeat("=", 70))

	// Create and run the chat.
	chat := &transferChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// transferChat manages the conversation with agent transfer functionality.
type transferChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session.
func (c *transferChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with main agent and sub-agents.
func (c *transferChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName, openai.Options{
		ChannelBufferSize: 512,
	})

	// Create sub-agents.
	mathAgent := c.createMathAgent(modelInstance)
	weatherAgent := c.createWeatherAgent(modelInstance)
	researchAgent := c.createResearchAgent(modelInstance)

	// Create coordinator agent with sub-agents.
	coordinatorAgent := c.createCoordinatorAgent(modelInstance, []agent.Agent{
		mathAgent,
		weatherAgent,
		researchAgent,
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

// createMathAgent creates a specialized math agent.
func (c *transferChat) createMathAgent(modelInstance model.Model) agent.Agent {
	// Math calculation tool.
	calculateTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculate"),
		function.WithDescription("Perform mathematical calculations"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.3), // Lower temperature for more precise calculations
		Stream:      true,
	}

	return llmagent.New(
		"math-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A specialized mathematical computation agent"),
		llmagent.WithInstruction("You are a math expert. Solve mathematical problems step by step with clear explanations."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(200),
		llmagent.WithTools([]tool.Tool{calculateTool}),
	)
}

// createWeatherAgent creates a specialized weather agent.
func (c *transferChat) createWeatherAgent(modelInstance model.Model) agent.Agent {
	// Weather tool.
	weatherTool := function.NewFunctionTool(
		c.getWeather,
		function.WithName("get_weather"),
		function.WithDescription("Get current weather information for a location"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1500),
		Temperature: floatPtr(0.5),
		Stream:      true,
	}

	return llmagent.New(
		"weather-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A specialized weather information agent"),
		llmagent.WithInstruction("You are a weather expert. Provide detailed weather information and recommendations."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(200),
		llmagent.WithTools([]tool.Tool{weatherTool}),
	)
}

// createResearchAgent creates a specialized research agent.
func (c *transferChat) createResearchAgent(modelInstance model.Model) agent.Agent {
	// Search tool.
	searchTool := function.NewFunctionTool(
		c.search,
		function.WithName("search"),
		function.WithDescription("Search for information on a given topic"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(3000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	return llmagent.New(
		"research-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A specialized research and information gathering agent"),
		llmagent.WithInstruction("You are a research expert. "+
			"Gather comprehensive information and provide well-structured answers."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(200),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)
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

When a user asks a question:
1. Analyze what type of task it is
2. Use transfer_to_agent to delegate to the appropriate specialist
3. If unsure, ask the user for clarification or handle simple queries yourself

Always explain why you're transferring to a specific agent.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(200),
		llmagent.WithSubAgents(subAgents),
	)
}

// startChat runs the interactive conversation loop.
func (c *transferChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Try different types of requests:")
	fmt.Println("   • Math: 'Calculate the compound interest on $5000 at 6% for 8 years'")
	fmt.Println("   • Weather: 'What's the weather like in Tokyo?'")
	fmt.Println("   • Research: 'Tell me about renewable energy trends'")
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
	)

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Handle agent transfers.
		if event.Object == model.ObjectTypeTransfer {
			fmt.Printf("\n🔄 Transfer Event: %s\n", event.Response.Choices[0].Message.Content)
			currentAgent = c.getAgentFromTransfer(event)
			assistantStarted = false
			continue
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			if c.isTransferTool(event.Choices[0].Message.ToolCalls[0]) {
				fmt.Printf("🔄 Initiating transfer...\n")
			} else {
				fmt.Printf("🔧 %s executing tools:\n", c.getAgentIcon(event.Author))
				for _, toolCall := range event.Choices[0].Message.ToolCalls {
					fmt.Printf("   • %s", toolCall.Function.Name)
					if len(toolCall.Function.Arguments) > 0 {
						fmt.Printf(" (%s)", string(toolCall.Function.Arguments))
					}
					fmt.Printf("\n")
				}
			}
			continue
		}

		// Process text content - use delta content only for streaming.
		if len(event.Choices) > 0 {
			var content string
			// Only use delta content to avoid duplication in streaming responses
			if event.Choices[0].Delta.Content != "" {
				content = event.Choices[0].Delta.Content
			}

			if content != "" {
				if !assistantStarted && !toolCallsDetected {
					if event.Author != currentAgent {
						fmt.Printf("\n%s %s: ", c.getAgentIcon(event.Author), c.getAgentDisplayName(event.Author))
						currentAgent = event.Author
					}
					assistantStarted = true
				} else if toolCallsDetected && !assistantStarted {
					if !c.isTransferResponse(event) {
						fmt.Printf("%s %s: ", c.getAgentIcon(event.Author), c.getAgentDisplayName(event.Author))
						assistantStarted = true
					}
				}

				if !c.isTransferResponse(event) {
					fmt.Print(content)
					fullContent += content
				}
			}
		}

		// Handle tool responses.
		if c.isToolEvent(event) && len(event.Choices[0].Message.ToolCalls) > 0 &&
			!c.isTransferTool(event.Choices[0].Message.ToolCalls[0]) {
			fmt.Printf("   ✅ Tool completed\n")
		}
	}

	fmt.Println() // Final newline
	return nil
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
		}
	}
	return "coordinator-agent"
}

func (c *transferChat) isTransferTool(toolCall model.ToolCall) bool {
	return toolCall.Function.Name == "transfer_to_agent"
}

func (c *transferChat) isTransferResponse(event *event.Event) bool {
	return len(event.Choices) > 0 && event.Choices[0].Message.ToolID != ""
}

func (c *transferChat) isToolEvent(event *event.Event) bool {
	return len(event.Choices) > 0 && event.Choices[0].Message.Role == model.RoleTool
}

// Tool implementations for demonstration.

// calculate performs mathematical operations.
func (c *transferChat) calculate(args calcArgs) calcResult {
	var result float64
	switch args.Operation {
	case "add":
		result = args.A + args.B
	case "subtract":
		result = args.A - args.B
	case "multiply":
		result = args.A * args.B
	case "divide":
		if args.B == 0 {
			return calcResult{
				Operation: args.Operation,
				A:         args.A,
				B:         args.B,
				Result:    0,
				Error:     "Division by zero",
			}
		}
		result = args.A / args.B
	case "power":
		result = 1
		for i := 0; i < int(args.B); i++ {
			result *= args.A
		}
	default:
		return calcResult{
			Operation: args.Operation,
			A:         args.A,
			B:         args.B,
			Result:    0,
			Error:     "Unknown operation",
		}
	}

	return calcResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}
}

// getWeather returns weather information for a location.
func (c *transferChat) getWeather(args weatherArgs) weatherResult {
	// Simulate weather data based on location.
	weather := map[string]weatherResult{
		"tokyo": {
			Location:       "Tokyo, Japan",
			Temperature:    22.5,
			Condition:      "Partly Cloudy",
			Humidity:       65,
			Recommendation: "Perfect weather for outdoor activities",
		},
		"london": {
			Location:       "London, UK",
			Temperature:    15.2,
			Condition:      "Rainy",
			Humidity:       85,
			Recommendation: "Bring an umbrella and dress warmly",
		},
		"new york": {
			Location:       "New York, USA",
			Temperature:    18.7,
			Condition:      "Sunny",
			Humidity:       45,
			Recommendation: "Great day for outdoor activities",
		},
	}

	location := strings.ToLower(args.Location)
	if result, exists := weather[location]; exists {
		return result
	}

	// Default response for unknown locations.
	return weatherResult{
		Location:       args.Location,
		Temperature:    20.0,
		Condition:      "Clear",
		Humidity:       50,
		Recommendation: "Weather data not available, but looks pleasant",
	}
}

// search performs information search.
func (c *transferChat) search(args searchArgs) searchResult {
	// Simulate search results based on query.
	query := strings.ToLower(args.Query)

	var results []string
	if strings.Contains(query, "renewable energy") {
		results = []string{
			"Renewable energy capacity increased by 295 GW in 2022",
			"Solar and wind power account for 90% of new renewable capacity",
			"Global investment in renewable energy reached $1.8 trillion",
			"Renewable energy costs have decreased by 85% since 2010",
		}
	} else if strings.Contains(query, "ai") || strings.Contains(query, "artificial intelligence") {
		results = []string{
			"AI market expected to reach $1.8 trillion by 2030",
			"Large language models showing breakthrough capabilities",
			"AI adoption accelerating across healthcare and finance",
			"Concerns about AI safety and regulation increasing",
		}
	} else if strings.Contains(query, "climate") {
		results = []string{
			"Global temperatures have risen 1.1°C since pre-industrial times",
			"Arctic sea ice declining at 13% per decade",
			"Extreme weather events becoming more frequent",
			"Countries committing to net-zero emissions by 2050",
		}
	} else {
		results = []string{
			fmt.Sprintf("Search result 1 for '%s'", args.Query),
			fmt.Sprintf("Search result 2 for '%s'", args.Query),
			fmt.Sprintf("Search result 3 for '%s'", args.Query),
		}
	}

	return searchResult{
		Query:   args.Query,
		Results: results,
		Count:   len(results),
	}
}

// Data structures for tool arguments and results.

type calcArgs struct {
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide, power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calcResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
	Error     string  `json:"error,omitempty"`
}

type weatherArgs struct {
	Location string `json:"location" description:"The location to get weather for"`
}

type weatherResult struct {
	Location       string  `json:"location"`
	Temperature    float64 `json:"temperature"`
	Condition      string  `json:"condition"`
	Humidity       int     `json:"humidity"`
	Recommendation string  `json:"recommendation"`
}

type searchArgs struct {
	Query string `json:"query" description:"The search query"`
}

type searchResult struct {
	Query   string   `json:"query"`
	Results []string `json:"results"`
	Count   int      `json:"count"`
}

// Helper functions.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
