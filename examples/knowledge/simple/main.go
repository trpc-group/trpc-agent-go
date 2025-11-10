//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates knowledge integration with the LLM agent.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	// Embedder.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
	ollamaembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	// Source.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
	dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"

	// Vector store.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	vectorelasticsearch "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"

	// Import PDF reader to register it (optional - comment out if PDF support is not needed).
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

// command line flags.
var (
	modelName     = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming     = flag.Bool("streaming", true, "Enable streaming mode for responses")
	embedderType  = flag.String("embedder", "openai", "Embedder type: openai, gemini, ollama")
	vectorStore   = flag.String("vectorstore", "inmemory", "Vector store type: inmemory, pgvector, tcvector, elasticsearch")
	esVersion     = flag.String("es-version", "v9", "Elasticsearch version: v7, v8, v9 (only used when vectorstore=elasticsearch)")
	agenticFilter = flag.Bool("agentic_filter", true, "Enable agentic filter for knowledge search")
	recreate      = flag.Bool("recreate", false, "Recreate the vector store on startup, all data in vector store will be deleted.")
	sourceSync    = flag.Bool("source_sync", false, "Enable source sync for incremental sync, all data in vector store will be sync with source. And orphan documents will be deleted.")
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
	pgvectorUser     = getEnvOrDefault("PGVECTOR_USER", "root")
	pgvectorPassword = getEnvOrDefault("PGVECTOR_PASSWORD", "")
	pgvectorDatabase = getEnvOrDefault("PGVECTOR_DATABASE", "vectordb")

	// TCVector.
	tcvectorURL      = getEnvOrDefault("TCVECTOR_URL", "")
	tcvectorUsername = getEnvOrDefault("TCVECTOR_USERNAME", "")
	tcvectorPassword = getEnvOrDefault("TCVECTOR_PASSWORD", "")

	// Elasticsearch.
	elasticsearchHosts     = getEnvOrDefault("ELASTICSEARCH_HOSTS", "http://localhost:9200")
	elasticsearchUsername  = getEnvOrDefault("ELASTICSEARCH_USERNAME", "")
	elasticsearchPassword  = getEnvOrDefault("ELASTICSEARCH_PASSWORD", "")
	elasticsearchAPIKey    = getEnvOrDefault("ELASTICSEARCH_API_KEY", "")
	elasticsearchIndexName = getEnvOrDefault("ELASTICSEARCH_INDEX_NAME", "trpc_agent_go")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🧠 Knowledge-Enhanced Chat Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Vector Store: %s\n", *vectorStore)
	if *vectorStore == "elasticsearch" {
		fmt.Printf(" (Version: %s)", *esVersion)
	}
	if *agenticFilter {
		fmt.Printf("Available tools: knowledge_search, knowledge_search_with_agentic_filter\n")
	} else {
		fmt.Printf("Available tools: knowledge_search\n")
	}
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
	sources      []source.Source
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

	// get all metadata from sources, it contains keys and values, llm will choose keys values for the filter
	sourcesMetadata := source.GetAllMetadata(c.sources)

	// You can also bind knowledge base on agent directly.
	// It's equal with set knowledge tool in agent.
	// But you can only set one knowledgebase for one agent.

	// llmAgent := llmagent.New(
	// 	agentName,
	// 	llmagent.WithModel(modelInstance),
	// 	llmagent.WithDescription("A helpful AI assistant with knowledge base access."),
	// 	llmagent.WithInstruction("Use the knowledge_search tool to find relevant information from the knowledge base. Be helpful and conversational."),
	// 	llmagent.WithGenerationConfig(genConfig),
	// 	llmagent.WithKnowledge(c.kb),
	// 	// This will automatically add the knowledge_search tool.
	// 	llmagent.WithEnableKnowledgeAgenticFilter(*agenticFilter),
	// 	llmagent.WithKnowledgeAgenticFilterInfo(sourcesMetadata),
	// )

	agentName := "knowledge-assistant"
	knowledgeTool := knowledgetool.NewAgenticFilterSearchTool(
		c.kb,
		sourcesMetadata,
		knowledgetool.WithToolName("knowledge_search"),
		// filter by topic = programming and content_type = llm
		knowledgetool.WithConditionedFilter(
			searchfilter.Or(
				searchfilter.Equal("topic", "programming"),
				searchfilter.Equal("content_type", "llm"),
			),
		),
		knowledgetool.WithToolDescription(
			"Use the knowledge_search tool to find relevant information from the knowledge base. Be helpful and conversational."),
	)

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant."),
		llmagent.WithInstruction("Use the knowledge_search tool to find relevant information from the knowledge base. Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{knowledgeTool}),
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
		docBuilder := func(tcDoc tcvectordb.Document) (*document.Document, []float64, error) {
			doc := &document.Document{
				ID: tcDoc.Id,
			}
			if field, ok := tcDoc.Fields["name"]; ok {
				doc.Name = field.String()
			}
			if field, ok := tcDoc.Fields["content"]; ok {
				doc.Content = field.String()
			}
			if field, ok := tcDoc.Fields["created_at"]; ok {
				u := min(field.Uint64(), uint64(math.MaxInt64))
				//nolint:gosec // u is not overflowed and the conversion is safe.
				doc.CreatedAt = time.Unix(int64(u), 0)
			}
			if field, ok := tcDoc.Fields["updated_at"]; ok {
				u := min(field.Uint64(), uint64(math.MaxInt64))
				//nolint:gosec // u is not overflowed and the conversion is safe.
				doc.UpdatedAt = time.Unix(int64(u), 0)
			}
			if field, ok := tcDoc.Fields["metadata"]; ok {
				if metadata, ok := field.Val.(map[string]any); ok {
					doc.Metadata = metadata
				}
			}

			embedding := make([]float64, len(tcDoc.Vector))
			for i, v := range tcDoc.Vector {
				embedding[i] = float64(v)
			}
			return doc, embedding, nil
		}
		vs, err := vectortcvector.New(
			vectortcvector.WithURL(tcvectorURL),
			vectortcvector.WithUsername(tcvectorUsername),
			vectortcvector.WithPassword(tcvectorPassword),
			vectortcvector.WithCollection("tcvector-agent-go"),
			// used for build document from tcvector query result
			vectortcvector.WithDocBuilder(docBuilder),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create tcvector store: %w", err)
		}
		return vs, nil
	case "elasticsearch":
		// Parse hosts string into slice.
		hosts := strings.Split(elasticsearchHosts, ",")
		for i, host := range hosts {
			hosts[i] = strings.TrimSpace(host)
		}

		docBuilder := func(hitSource json.RawMessage) (*document.Document, []float64, error) {
			var source struct {
				ID        string         `json:"id"`
				Name      string         `json:"name"`
				Content   string         `json:"content"`
				CreatedAt time.Time      `json:"created_at"`
				UpdatedAt time.Time      `json:"updated_at"`
				Embedding []float64      `json:"embedding"`
				Metadata  map[string]any `json:"metadata"`
			}
			if err := json.Unmarshal(hitSource, &source); err != nil {
				return nil, nil, err
			}
			// Create document.
			doc := &document.Document{
				ID:        source.ID,
				Name:      source.Name,
				Content:   source.Content,
				CreatedAt: source.CreatedAt,
				UpdatedAt: source.UpdatedAt,
				Metadata:  source.Metadata,
			}
			return doc, source.Embedding, nil
		}

		vs, err := vectorelasticsearch.New(
			vectorelasticsearch.WithAddresses(hosts),
			vectorelasticsearch.WithUsername(elasticsearchUsername),
			vectorelasticsearch.WithPassword(elasticsearchPassword),
			vectorelasticsearch.WithAPIKey(elasticsearchAPIKey),
			vectorelasticsearch.WithIndexName(elasticsearchIndexName),
			vectorelasticsearch.WithMaxRetries(3),
			vectorelasticsearch.WithVersion(*esVersion),
			// 用于文档检索时的自定义文档构建方法。若不提供，则使用默认构建方法。
			vectorelasticsearch.WithDocBuilder(docBuilder),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create elasticsearch store: %w", err)
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
	case "ollama":
		return ollamaembedder.New(), nil
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
				"../exampledata/file/llm.md",
			},
			filesource.WithName("Large Language Model"),
			filesource.WithMetadataValue("category", "documentation"),
			filesource.WithMetadataValue("topic", "machine_learning"),
			filesource.WithMetadataValue("source_type", "local_file"),
			filesource.WithMetadataValue("content_type", "llm"),
		),
		filesource.New(
			[]string{
				"../exampledata/file/golang.md",
			},
			filesource.WithName("Golang"),
			filesource.WithMetadataValue("category", "documentation"),
			filesource.WithMetadataValue("topic", "programming"),
			filesource.WithMetadataValue("source_type", "local_file"),
			filesource.WithMetadataValue("content_type", "golang"),
		),
		dirsource.New(
			[]string{
				"../exampledata/dir",
			},
			dirsource.WithName("Data Directory"),
			dirsource.WithMetadataValue("category", "dataset"),
			dirsource.WithMetadataValue("topic", "machine_learning"),
			dirsource.WithMetadataValue("source_type", "local_directory"),
			dirsource.WithMetadataValue("content_type", "transformer"),
		),
		// URL source for web content.
		urlsource.New(
			[]string{
				"https://en.wikipedia.org/wiki/Byte-pair_encoding",
			},
			urlsource.WithName("Byte-pair encoding"),
			urlsource.WithMetadataValue("category", "encyclopedia"),
			urlsource.WithMetadataValue("topic", "natural_language_processing"),
			urlsource.WithMetadataValue("source_type", "web_url"),
			urlsource.WithMetadataValue("content_type", "wiki"),
		),
		// Auto source that can handle mixed inputs.
		autosource.New(
			[]string{
				"Cloud computing is the delivery of computing services over the internet, including servers, storage, databases, networking, software, and analytics. It provides on-demand access to shared computing resources.",
				"https://en.wikipedia.org/wiki/N-gram",
				"./README.md",
			},
			autosource.WithName("Mixed Content Source"),
			autosource.WithMetadataValue("category", "mixed"),
			autosource.WithMetadataValue("topic", "technology"),
			autosource.WithMetadataValue("source_type", "auto_detect"),
			autosource.WithMetadataValue("content_type", "mixed"),
		),
	}
	return sources
}

// setupKnowledgeBase creates a built-in knowledge base with sample documents.
func (c *knowledgeChat) setupKnowledgeBase(ctx context.Context) error {
	// Create sources.
	c.sources = c.createSources()

	// Create vector store.
	vs, err := c.setupVectorDB()
	if err != nil {
		return err
	}

	// Create embedder.
	emb, err := c.setupEmbedder(ctx)
	if err != nil {
		return fmt.Errorf("failed to create embedder: %w", err)
	}

	// Create built-in knowledge base with all components.
	c.kb = knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
		knowledge.WithSources(c.sources),
		knowledge.WithEnableSourceSync(*sourceSync),
	)

	// Optionally load the knowledge base.
	if err := c.kb.Load(
		ctx,
		knowledge.WithShowProgress(false),  // The default is true.
		knowledge.WithProgressStepSize(10), // The default is 10.
		knowledge.WithShowStats(false),     // The default is true.
		knowledge.WithSourceConcurrency(4), // The default is min(4, len(sources)).
		knowledge.WithDocConcurrency(64),   // The default is runtime.NumCPU().
		knowledge.WithRecreate(*recreate),
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
	fmt.Println("🔧 Vector Store Configuration:")
	switch *vectorStore {
	case "elasticsearch":
		fmt.Printf("   Elasticsearch: %s (Index: %s)\n", elasticsearchHosts, elasticsearchIndexName)
		if elasticsearchUsername != "" {
			fmt.Printf("   Username: %s\n", elasticsearchUsername)
		}
	case "pgvector":
		fmt.Printf("   PGVector: %s:%s/%s\n", pgvectorHost, pgvectorPort, pgvectorDatabase)
	case "tcvector":
		if tcvectorURL != "" {
			fmt.Printf("   TCVector: %s\n", tcvectorURL)
		}
	}
	fmt.Println()
	fmt.Println("🔍 Try asking questions like:")
	fmt.Println("   - What is a Large Language Model?")
	fmt.Println("   - Tell me about Golang programming language")
	fmt.Println("   - How does Byte-pair encoding work?")
	fmt.Println("   - What is cloud computing?")
	fmt.Println("   - Show me documentation about machine learning")
	fmt.Println("   - Find content from wiki sources")
	fmt.Println("")
	fmt.Println("🎯 You can also try agentic filtered searches:")
	fmt.Println("   - Query something about programming and golang related stuff")
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

		fmt.Println() // Add spacing between turns.
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

	// Process response based on streaming mode.
	if *streaming {
		return c.processStreamingResponse(eventChan)
	} else {
		return c.processNonStreamingResponse(eventChan)
	}
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
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 Tool calls initiated:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
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
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]

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
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// processNonStreamingResponse handles the non-streaming response from the agent.
func (c *knowledgeChat) processNonStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var fullContent string
	var hasToolCalls bool

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
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			if !hasToolCalls {
				fmt.Printf("\n🔧 Tool calls initiated:\n")
				hasToolCalls = true
			}
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
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

		// Process final content from non-streaming response.
		if event.IsFinalResponse() {
			// In non-streaming mode, the final content should be in the Message.Content
			if len(event.Response.Choices) > 0 {
				choice := event.Response.Choices[0]
				if choice.Message.Content != "" {
					fullContent = choice.Message.Content
					fmt.Print(fullContent)
				}
			}
			fmt.Printf("\n")
			break
		}
	}

	return nil
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
