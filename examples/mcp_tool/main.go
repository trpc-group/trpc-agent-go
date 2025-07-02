// Package main demonstrates streamable http and stdio mcp tools usage.
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

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/core/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/core/tool/mcp"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/runner"
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("🚀 Streamable HTTP and STDIO MCP tools usage\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator, current_time, echo, add, get_weather, get_news\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &multiTurnChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// multiTurnChat manages the conversation.
type multiTurnChat struct {
	modelName  string
	runner     *runner.Runner
	userID     string
	sessionID  string
	mcpToolSet []*mcp.MCPToolSet
}

// run starts the interactive chat session.
func (c *multiTurnChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and tools.
func (c *multiTurnChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName, openai.Options{
		ChannelBufferSize: 512,
	})

	// Create tools.
	calculatorTool := function.NewFunctionTool(c.calculate, function.WithName("calculator"), function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"))
	timeTool := function.NewFunctionTool(c.getCurrentTime, function.WithName("current_time"), function.WithDescription("Get the current time and date for a specific timezone"))

	// Create Stdio MCP tools.
	// Configure STDIO MCP to connect to our local server.
	// This is a simplified STDIO server with two tools: 'echo' and 'add'
	mcpConfig := mcp.MCPConnectionConfig{
		Transport: "stdio",
		Command:   "go",
		Args:      []string{"run", "./stdio_server/main.go"},
		Timeout:   10 * time.Second,
	}

	// Create MCP toolset with enterprise-level configuration.
	stdioToolSet := mcp.NewMCPToolSet(mcpConfig,
		mcp.WithToolFilter(mcp.NewIncludeFilter("echo", "add")),
	)
	fmt.Println("STDIO MCP Toolset created successfully")

	// Discover available tools.
	stdioTools := stdioToolSet.Tools(ctx)
	fmt.Printf("Discovered %d STDIO MCP tools\n", len(stdioTools))

	// Create Streamable MCP tools.
	// Configure Streamable HTTP MCP connection.
	// This server provides special tools: get_weather and get_news (with fake data)
	streamableConfig := mcp.MCPConnectionConfig{
		Transport: "streamable_http",
		ServerURL: "http://localhost:3000/mcp", // Use ServerURL instead of URL
		Timeout:   10 * time.Second,
	}

	// Create MCP toolset with enterprise-level configuration.
	streamableToolSet := mcp.NewMCPToolSet(streamableConfig,
		mcp.WithRetry(mcp.RetryConfig{
			Enabled:       true,
			MaxAttempts:   3,
			InitialDelay:  time.Second,
			BackoffFactor: 2.0,
			MaxDelay:      30 * time.Second,
		}),
		mcp.WithToolFilter(mcp.NewIncludeFilter("get_weather", "get_news")),
	)
	fmt.Println("MCP Toolset created successfully")

	// Discover available tools.
	streamableTools := streamableToolSet.Tools(ctx)
	fmt.Printf("Discovered %d MCP tools\n", len(streamableTools))

	// Combine all tools
	allTools := make([]tool.Tool, 0, 2+len(stdioTools)+len(streamableTools))
	allTools = append(allTools, calculatorTool, timeTool)
	for _, mcpTool := range stdioTools {
		allTools = append(allTools, mcpTool)
	}
	for _, mcpTool := range streamableTools {
		allTools = append(allTools, mcpTool)
	}

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools"),
		llmagent.WithInstruction("Use tools when appropriate for calculations or time queries. Be helpful and conversational."),
		llmagent.WithSystemPrompt("You have access to calculator and current_time tools. Use them when users ask for calculations or time information."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools(allTools),
	)

	// Create runner.
	appName := "multi-turn-chat"
	c.runner = runner.New(
		appName,
		llmAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
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

	// Close the toolset when done.
	for _, toolSet := range c.mcpToolSet {
		toolSet.Close()
	}

	return nil
}

// processMessage handles a single message exchange.
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.RunOptions{})
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with tool call visualization.
func (c *multiTurnChat) processStreamingResponse(eventChan <-chan *event.Event) error {
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
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 CallableTool calls initiated:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Executing tools...\n")
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]

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
		// Don't break on tool response events (Done=true but not final assistant response).
		if event.Done && !c.isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// isToolEvent checks if an event is a tool response (not a final response).
func (c *multiTurnChat) isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}

	// Check if this is a tool response by examining choices.
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool {
			return true
		}
	}

	return false
}

// CallableTool implementations.

// calculate performs basic mathematical operations.
func (c *multiTurnChat) calculate(args calculatorArgs) calculatorResult {
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
		} else {
			result = 0 // Handle division by zero
		}
	default:
		result = 0
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}
}

// getCurrentTime returns current time information.
func (c *multiTurnChat) getCurrentTime(args timeArgs) timeResult {
	now := time.Now()
	var t time.Time
	timezone := args.Timezone

	// Handle timezone conversion.
	switch strings.ToUpper(args.Timezone) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.Add(-5 * time.Hour) // Simplified EST
	case "PST", "PACIFIC":
		t = now.Add(-8 * time.Hour) // Simplified PST
	case "CST", "CENTRAL":
		t = now.Add(-6 * time.Hour) // Simplified CST
	case "":
		t = now
		timezone = "Local"
	default:
		t = now.UTC()
		timezone = "UTC"
	}

	return timeResult{
		Timezone: timezone,
		Time:     t.Format("15:04:05"),
		Date:     t.Format("2006-01-02"),
		Weekday:  t.Weekday().String(),
	}
}

// calculatorArgs represents arguments for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

// calculatorResult represents the result of a calculation.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

// timeArgs represents arguments for the time tool.
type timeArgs struct {
	Timezone string `json:"timezone" description:"Timezone (UTC, EST, PST, CST) or leave empty for local"`
}

// timeResult represents the current time information.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
