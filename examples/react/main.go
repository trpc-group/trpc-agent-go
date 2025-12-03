//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates React planning with LLM agents using structured
// planning instructions, tool calling, and response processing.
package main

import (
	"bufio"
	"context"
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
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("🧠 React Planning Agent Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: search, calculator, weather\n")
	fmt.Printf("The agent will use React planning to structure its responses\n")
	fmt.Println(strings.Repeat("=", 60))

	// Create and run the chat.
	chat := &reactPlanningChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// reactPlanningChat manages the conversation with React planning.
type reactPlanningChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session.
func (c *reactPlanningChat) run() error {
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

// setup creates the runner with LLM agent and React planner.
func (c *reactPlanningChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create tools for demonstration.
	searchTool := function.NewFunctionTool(
		c.search,
		function.WithName("search"),
		function.WithDescription("Search for information on a given topic"),
	)
	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform mathematical calculations"),
	)
	weatherTool := function.NewFunctionTool(
		c.getWeather,
		function.WithName("get_weather"),
		function.WithDescription("Get current weather information for a location"),
	)

	// Create React planner.
	reactPlanner := react.New()

	// Create LLM agent with React planner and tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(3000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "react-research-agent"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A research agent that uses React planning to structure its thinking and actions"),
		llmagent.WithInstruction("You are a helpful research assistant. "+
			"Use the React planning approach to break down complex questions into manageable steps."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{searchTool, calculatorTool, weatherTool}),
		llmagent.WithPlanner(reactPlanner),
	)

	// Create runner.
	appName := "react-planning-demo"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("react-session-%d", time.Now().Unix())

	fmt.Printf("✅ React planning agent ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *reactPlanningChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Try asking complex questions that require planning, like:")
	fmt.Println("   • 'What's the population of Tokyo and how does it compare to New York?'")
	fmt.Println("   • 'If I invest $1000 at 5% interest, what will it be worth in 10 years?'")
	fmt.Println("   • 'What's the weather like in Paris and should I pack an umbrella?'")
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
func (c *reactPlanningChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response with React planning awareness.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with React planning visualization.
func (c *reactPlanningChat) processStreamingResponse(eventChan <-chan *event.Event) error {
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

		// Detect and display tool calls.
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
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
			continue
		}

		// Process text content with React planning awareness.
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
			fmt.Printf("   ✅ Tool completed\n")
		}
	}

	fmt.Println() // End the response
	return nil
}

// Tool implementations for demonstration.

// search simulates a search tool.
func (c *reactPlanningChat) search(_ context.Context, args searchArgs) (searchResult, error) {
	results := map[string]string{
		"tokyo": "Tokyo has a population of approximately 14 million people in the city proper and " +
			"38 million in the greater metropolitan area.",
		"new york": "New York City has a population of approximately 8.3 million people, " +
			"with about 20 million in the metropolitan area.",
		"paris weather": "Paris currently has partly cloudy skies with a temperature of 15°C (59°F). " +
			"Light rain is expected later today.",
		"compound interest": "Compound interest is calculated using the formula A = P(1 + r/n)^(nt), " +
			"where A is the amount, P is principal, r is annual interest rate, " +
			"n is number of times interest compounds per year, and t is time in years.",
	}

	query := strings.ToLower(args.Query)
	for key, result := range results {
		if strings.Contains(query, key) || strings.Contains(key, query) {
			return searchResult{
				Query:   args.Query,
				Results: []string{result},
				Count:   1,
			}, nil
		}
	}

	return searchResult{
		Query:   args.Query,
		Results: []string{fmt.Sprintf("Found general information about: %s", args.Query)},
		Count:   1,
	}, nil
}

// calculate performs mathematical calculations.
func (c *reactPlanningChat) calculate(_ context.Context, args calcArgs) (calcResult, error) {
	var result float64

	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B != 0 {
			result = args.A / args.B
		}
	case "power", "^":
		result = math.Pow(args.A, args.B)
	}

	return calcResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// getWeather simulates weather information retrieval.
func (c *reactPlanningChat) getWeather(_ context.Context, args weatherArgs) (weatherResult, error) {
	weatherData := map[string]weatherResult{
		"paris": {
			Location:       "Paris, France",
			Temperature:    15,
			Condition:      "Partly cloudy",
			Humidity:       65,
			Recommendation: "Light jacket recommended, umbrella advised for later",
		},
		"tokyo": {
			Location:       "Tokyo, Japan",
			Temperature:    22,
			Condition:      "Sunny",
			Humidity:       55,
			Recommendation: "Perfect weather for outdoor activities",
		},
		"new york": {
			Location:       "New York, USA",
			Temperature:    18,
			Condition:      "Overcast",
			Humidity:       70,
			Recommendation: "Light layers recommended",
		},
	}

	location := strings.ToLower(args.Location)
	if weather, exists := weatherData[location]; exists {
		return weather, nil
	}

	return weatherResult{
		Location:       args.Location,
		Temperature:    20,
		Condition:      "Unknown",
		Humidity:       60,
		Recommendation: "Check local weather sources for accurate information",
	}, nil
}

// Tool argument and result types.

type searchArgs struct {
	Query string `json:"query" description:"The search query"`
}

type searchResult struct {
	Query   string   `json:"query"`
	Results []string `json:"results"`
	Count   int      `json:"count"`
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

// Helper functions.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
