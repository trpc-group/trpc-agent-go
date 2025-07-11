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

// Package main demonstrates knowledge integration with the LLM agent.
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
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
	dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "claude-4-sonnet-20250514", "Name of the model to use")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🧠 Knowledge-Enhanced Chat Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: knowledge_search, calculator, current_time\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &knowledgeChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// knowledgeChat manages the conversation with knowledge integration.
type knowledgeChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
	kb        *knowledge.BuiltinKnowledge
}

// run starts the interactive chat session.
func (c *knowledgeChat) run() error {
	ctx := context.Background()

	// Setup the runner with knowledge base.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent, knowledge base, and tools.
func (c *knowledgeChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openaimodel.New(c.modelName, openaimodel.Options{
		ChannelBufferSize: 512,
	})

	// Create knowledge base with sample documents.
	if err := c.setupKnowledgeBase(ctx); err != nil {
		return fmt.Errorf("failed to setup knowledge base: %w", err)
	}

	// Create additional tools.
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

	// Create LLM agent with knowledge and tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "knowledge-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with knowledge base access and calculator tools"),
		llmagent.WithInstruction("Use the knowledge_search tool to find relevant information from the knowledge base. Use calculator and current_time tools when appropriate. Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
		llmagent.WithKnowledge(c.kb), // This will automatically add the knowledge_search tool.
	)

	// Create session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create runner.
	appName := "knowledge-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("knowledge-session-%d", time.Now().Unix())

	fmt.Printf("✅ Knowledge chat ready! Session: %s\n", c.sessionID)
	fmt.Printf("📚 Knowledge base loaded with sample documents\n\n")

	return nil
}

// setupKnowledgeBase creates a built-in knowledge base with sample documents.
func (c *knowledgeChat) setupKnowledgeBase(ctx context.Context) error {
	// Create in-memory vector store.
	vectorStore := vectorinmemory.New()

	// Use OpenAI embedder for demonstration (replace with your API key).
	embedder := openaiembedder.New()

	// Create diverse sources showcasing different types.
	sources := []source.Source{
		// File source for local documentation (if files exist).
		filesource.New(
			[]string{
				"./data/llm.md",
			},
			filesource.WithName("Large Language Model"),
			filesource.WithMetadataValue("type", "documentation"),
		),

		dirsource.New(
			[]string{
				"./dir",
			},
			dirsource.WithName("Data Directory"),
		),

		// URL source for web content.
		urlsource.New(
			[]string{
				"https://en.wikipedia.org/wiki/Byte-pair_encoding",
			},
			urlsource.WithName("Byte-pair encoding"),
			urlsource.WithMetadataValue("topic", "Byte-pair encoding"),
			urlsource.WithMetadataValue("source", "official"),
		),

		// Auto source that can handle mixed inputs.
		autosource.New(
			[]string{
				"Cloud computing is the delivery of computing services over the internet, including servers, storage, databases, networking, software, and analytics. It provides on-demand access to shared computing resources.",
				"https://en.wikipedia.org/wiki/N-gram",
				"./README.md",
			},
			autosource.WithName("Mixed Content Source"),
			autosource.WithMetadataValue("topic", "Cloud Computing"),
			autosource.WithMetadataValue("type", "mixed"),
		),
	}

	// Create built-in knowledge base with all components.
	c.kb = knowledge.New(
		knowledge.WithVectorStore(vectorStore),
		knowledge.WithEmbedder(embedder),
		knowledge.WithSources(sources),
	)
	// Load the knowledge base.
	if err := c.kb.Load(
		ctx,
		knowledge.WithShowProgress(false),  // The default is true.
		knowledge.WithProgressStepSize(10), // The default is 10.
		knowledge.WithShowStats(false),     // The default is true.
		knowledge.WithSourceConcurrency(4), // The default is min(4, len(sources)).
		knowledge.WithDocConcurrency(64),   // The default is runtime.NumCPU().
	); err != nil {
		return fmt.Errorf("failed to load knowledge base: %w", err)
	}
	return nil
}

// startChat runs the interactive conversation loop.
func (c *knowledgeChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /history  - Show conversation history")
	fmt.Println("   /new      - Start a new session")
	fmt.Println("   /exit      - End the conversation")
	fmt.Println()
	fmt.Println("🔍 Try asking questions like:")
	fmt.Println("   - What is a Large Language Model?")
	fmt.Println("   - Explain the Transformer architecture.")
	fmt.Println("   - What is a Mixture-of-Experts (MoE) model?")
	fmt.Println("   - How does Byte-pair encoding work?")
	fmt.Println("   - What is an N-gram model?")
	fmt.Println("   - What is cloud computing?")
	fmt.Println("   - Calculate 15 * 23")
	fmt.Println("   - What time is it in PST?")
	fmt.Println("   - What tools are available in this chat demo?")
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle special commands.
		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "/history":
			userInput = "show our conversation history"
		case "/new":
			c.startNewSession()
			continue
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
func (c *knowledgeChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response from the agent.
func (c *knowledgeChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var assistantStarted bool
	var fullContent string

	for event := range eventChan {
		if event == nil {
			continue
		}

		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 Tool calls initiated:\n")
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
					fmt.Printf("✅ Tool response (ID: %s): %s\n",
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
func (c *knowledgeChat) isToolEvent(event *event.Event) bool {
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

// startNewSession creates a new chat session.
func (c *knowledgeChat) startNewSession() {
	c.sessionID = fmt.Sprintf("knowledge-session-%d", time.Now().Unix())
	fmt.Printf("🔄 New session started: %s\n\n", c.sessionID)
}

// Tool implementations.

// calculate performs mathematical calculations.
func (c *knowledgeChat) calculate(args calculatorArgs) calculatorResult {
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

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}
}

// getCurrentTime returns the current time and date.
func (c *knowledgeChat) getCurrentTime(args timeArgs) timeResult {
	now := time.Now()
	loc := now.Location()

	// Handle timezone if specified.
	if args.Timezone != "" {
		switch strings.ToUpper(args.Timezone) {
		case "UTC":
			loc = time.UTC
		case "EST":
			loc = time.FixedZone("EST", -5*3600)
		case "PST":
			loc = time.FixedZone("PST", -8*3600)
		case "CST":
			loc = time.FixedZone("CST", -6*3600)
		}
		now = now.In(loc)
	}

	return timeResult{
		Timezone: loc.String(),
		Time:     now.Format("15:04:05"),
		Date:     now.Format("2006-01-02"),
		Weekday:  now.Format("Monday"),
	}
}

// Tool argument and result types.

type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

type timeArgs struct {
	Timezone string `json:"timezone" description:"Timezone (UTC, EST, PST, CST) or leave empty for local"`
}

type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// Helper functions.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
