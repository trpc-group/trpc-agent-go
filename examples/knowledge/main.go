//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	"os"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	// Embedder.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

	// Source.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
	dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"

	// Vector store.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// command line flags.
var (
	modelName    = flag.String("model", "claude-4-sonnet-20250514", "Name of the model to use")
	streaming    = flag.Bool("streaming", true, "Enable streaming mode for responses")
	embedderType = flag.String("embedder", "openai", "Embedder type: openai, gemini")
	vectorStore  = flag.String("vectorstore", "inmemory", "Vector store type: inmemory, pgvector, tcvector")
)

// Default values for optional configurations.
const (
	defaultEmbeddingModel = "text-embedding-3-small"
)

// Environment variables for vector stores and embedder.
var (
	// OpenAI embedding model.
	openaiEmbeddingModel = getEnvOrDefault("OPENAI_EMBEDDING_MODEL", defaultEmbeddingModel)

	// PGVector.
	pgvectorHost     = getEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")
	pgvectorPort     = getEnvOrDefault("PGVECTOR_PORT", "5432")
	pgvectorUser     = getEnvOrDefault("PGVECTOR_USER", "postgres")
	pgvectorPassword = getEnvOrDefault("PGVECTOR_PASSWORD", "")
	pgvectorDatabase = getEnvOrDefault("PGVECTOR_DATABASE", "vectordb")

	// TCVector.
	tcvectorURL      = getEnvOrDefault("TCVECTOR_URL", "")
	tcvectorUsername = getEnvOrDefault("TCVECTOR_USERNAME", "")
	tcvectorPassword = getEnvOrDefault("TCVECTOR_PASSWORD", "")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🧠 Knowledge-Enhanced Chat Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Available tools: knowledge_search\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &knowledgeChat{
		modelName:    *modelName,
		embedderType: *embedderType,
		vectorStore:  *vectorStore,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// knowledgeChat manages the conversation with knowledge integration.
type knowledgeChat struct {
	modelName    string
	embedderType string
	vectorStore  string
	runner       runner.Runner
	userID       string
	sessionID    string
	kb           *knowledge.BuiltinKnowledge
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
	modelInstance := openaimodel.New(c.modelName)

	// Create knowledge base with sample documents.
	if err := c.setupKnowledgeBase(ctx); err != nil {
		return fmt.Errorf("failed to setup knowledge base: %w", err)
	}

	// Create LLM agent with knowledge.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      *streaming,
	}

	agentName := "knowledge-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with knowledge base access."),
		llmagent.WithInstruction("Use the knowledge_search tool to find relevant information from the knowledge base. Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
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

// setupVectorDB creates the appropriate vector store based on the selected type.
func (c *knowledgeChat) setupVectorDB() (vectorstore.VectorStore, error) {
	switch strings.ToLower(*vectorStore) {
	case "inmemory":
		return vectorinmemory.New(), nil
	case "pgvector":
		port, err := strconv.Atoi(pgvectorPort)
		if err != nil {
			return nil, fmt.Errorf("failed to parse pgvector port: %w", err)
		}
		vs, err := vectorpgvector.New(
			vectorpgvector.WithHost(pgvectorHost),
			vectorpgvector.WithPort(port),
			vectorpgvector.WithUser(pgvectorUser),
			vectorpgvector.WithPassword(pgvectorPassword),
			vectorpgvector.WithDatabase(pgvectorDatabase),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create pgvector store: %w", err)
		}
		return vs, nil
	case "tcvector":
		vs, err := vectortcvector.New(
			vectortcvector.WithURL(tcvectorURL),
			vectortcvector.WithUsername(tcvectorUsername),
			vectortcvector.WithPassword(tcvectorPassword),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create tcvector store: %w", err)
		}
		return vs, nil
	default:
		return nil, fmt.Errorf("unsupported vector store type: %s", *vectorStore)
	}
}

// setupEmbedder creates embedder based on the configured embedderType.
func (c *knowledgeChat) setupEmbedder(ctx context.Context) (embedder.Embedder, error) {
	switch strings.ToLower(c.embedderType) {
	case "gemini":
		return geminiembedder.New(ctx)
	default: // openai
		return openaiembedder.New(
			openaiembedder.WithModel(openaiEmbeddingModel),
		), nil
	}
}

func (c *knowledgeChat) createSources() []source.Source {
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
	return sources
}

// setupKnowledgeBase creates a built-in knowledge base with sample documents.
func (c *knowledgeChat) setupKnowledgeBase(ctx context.Context) error {
	// Create vector store.
	vectorStore, err := c.setupVectorDB()
	if err != nil {
		return err
	}

	// Create embedder.
	emb, err := c.setupEmbedder(ctx)
	if err != nil {
		return fmt.Errorf("failed to create embedder: %w", err)
	}

	// Create sources.
	sources := c.createSources()

	// Create built-in knowledge base with all components.
	c.kb = knowledge.New(
		knowledge.WithVectorStore(vectorStore),
		knowledge.WithEmbedder(emb),
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
	fmt.Println("   /exit     - End the conversation")
	fmt.Println()
	fmt.Println("🔍 Try asking questions like:")
	fmt.Println("   - What is a Large Language Model?")
	fmt.Println("   - Explain the Transformer architecture.")
	fmt.Println("   - What is a Mixture-of-Experts (MoE) model?")
	fmt.Println("   - How does Byte-pair encoding work?")
	fmt.Println("   - What is an N-gram model?")
	fmt.Println("   - What is cloud computing?")
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

// Helper functions.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

// getEnvOrDefault returns the environment variable value or a default value if not set.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
