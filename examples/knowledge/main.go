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

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/builder"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/core/knowledge/embedder/openai"
	storageinmemory "trpc.group/trpc-go/trpc-agent-go/core/knowledge/storage/inmemory"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/core/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/core/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/orchestration/session/inmemory"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
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
	kb        knowledge.Knowledge
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
		llmagent.WithSystemPrompt("You have access to a knowledge base with information about various topics. Use the knowledge_search tool to find relevant information when users ask questions. You also have calculator and current_time tools available."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
		llmagent.WithKnowledge(c.kb), // This automatically adds the knowledge_search tool
	)

	// Create session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create runner.
	appName := "knowledge-chat"
	c.runner = runner.New(
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
	// Create in-memory storage and vector store
	storage := storageinmemory.New()
	vectorStore := vectorinmemory.New()

	// Use OpenAI embedder for demonstration (replace with your API key)
	embedder := openaiembedder.New(
		openaiembedder.WithAPIKey("sk-your-openai-key"),
	)

	// Create built-in knowledge base
	c.kb = knowledge.New(
		knowledge.WithStorage(storage),
		knowledge.WithVectorStore(vectorStore),
		knowledge.WithEmbedder(embedder),
	)

	// Add sample documents
	sampleDocuments := []*document.Document{
		builder.FromText(
			"Machine learning is a subset of artificial intelligence that enables computers to learn and make decisions without being explicitly programmed. It uses algorithms to identify patterns in data and make predictions or decisions based on those patterns. Common types include supervised learning, unsupervised learning, and reinforcement learning.",
			builder.WithTextID("machine-learning"),
			builder.WithTextName("Machine Learning Basics"),
		),
		builder.FromText(
			"Python is a high-level, interpreted programming language known for its simplicity and readability. It supports multiple programming paradigms including procedural, object-oriented, and functional programming. Python is widely used in data science, web development, automation, and artificial intelligence.",
			builder.WithTextID("python"),
			builder.WithTextName("Python Programming"),
		),
		builder.FromText(
			"Data science is an interdisciplinary field that uses scientific methods, processes, algorithms, and systems to extract knowledge and insights from structured and unstructured data. It combines statistics, data analysis, machine learning, and related methods to understand and analyze actual phenomena with data.",
			builder.WithTextID("data-science"),
			builder.WithTextName("Data Science Fundamentals"),
		),
		builder.FromText(
			"Web development involves creating websites and web applications. It includes frontend development (HTML, CSS, JavaScript) for user interfaces and backend development (server-side programming, databases) for server logic. Modern web development often uses frameworks like React, Angular, or Vue.js for frontend and Node.js, Python, or Java for backend.",
			builder.WithTextID("web-development"),
			builder.WithTextName("Web Development"),
		),
		builder.FromText(
			"Cloud computing is the delivery of computing services over the internet, including servers, storage, databases, networking, software, and analytics. It provides on-demand access to shared computing resources. Major cloud providers include AWS, Microsoft Azure, and Google Cloud Platform. Benefits include scalability, cost-effectiveness, and flexibility.",
			builder.WithTextID("cloud-computing"),
			builder.WithTextName("Cloud Computing"),
		),
	}

	// Add documents to knowledge base
	for _, doc := range sampleDocuments {
		if err := c.kb.AddDocument(ctx, doc); err != nil {
			return fmt.Errorf("failed to add document %s: %w", doc.ID, err)
		}
	}

	fmt.Printf("📖 Built-in knowledge base created with %d documents\n", len(sampleDocuments))
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
	fmt.Println("   - What is machine learning?")
	fmt.Println("   - Tell me about Python programming")
	fmt.Println("   - Explain data science")
	fmt.Println("   - What is cloud computing?")
	fmt.Println("   - Calculate 15 * 23")
	fmt.Println("   - What time is it?")
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
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.RunOptions{})
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
