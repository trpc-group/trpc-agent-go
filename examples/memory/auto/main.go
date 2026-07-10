//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates automatic memory extraction using the Runner.
// Unlike manual memory tools, auto memory extracts user information from
// conversations automatically in the background without explicit tool calls.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	knowledgefile "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	knowledgeinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	util "trpc.group/trpc-go/trpc-agent-go/examples/memory"
)

var (
	modelName = flag.String(
		"model",
		"deepseek-v4-flash",
		"Model for chat responses",
	)
	extModel = flag.String(
		"ext-model",
		"",
		"Model for memory extraction (defaults to chat model)",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming mode for responses",
	)
	debug = flag.Bool(
		"debug",
		false,
		"Enable debug mode to print messages sent to model",
	)
	memType = flag.String(
		"memory",
		"inmemory",
		"Memory service type: inmemory, sqlite, sqlitevec, redis, "+
			"postgres, pgvector, mysql, mysqlvec",
	)
	enableKnowledge = flag.Bool(
		"knowledge",
		false,
		"Enable a small local knowledge base with knowledge_search",
	)
	disableAutoMemoryOnExternalContext = flag.Bool(
		"disable-auto-memory-on-external-context",
		false,
		"Stop future auto-memory extraction after knowledge_search is used",
	)
)

func main() {
	flag.Parse()

	fmt.Println("🧠 Auto Memory Demo")
	fmt.Printf("Chat Model: %s\n", *modelName)
	extractorModel := *extModel
	if extractorModel == "" {
		extractorModel = *modelName
	}
	fmt.Printf("Extractor Model: %s\n", extractorModel)
	memoryType := util.MemoryType(*memType)
	fmt.Printf("Memory Service: %s\n", memoryType)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Knowledge: %t\n", *enableKnowledge)
	fmt.Printf("Disable Auto Memory On External Context: %t\n", *disableAutoMemoryOnExternalContext)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println()
	fmt.Println("💡 Auto memory mode extracts user information automatically.")
	fmt.Println("   No explicit memory tools are needed - the system learns")
	fmt.Println("   about you from natural conversation.")
	fmt.Println()

	chat := &autoMemoryChat{
		modelName:                          *modelName,
		extractorModel:                     extractorModel,
		memoryType:                         memoryType,
		appName:                            appName,
		streaming:                          *streaming,
		debug:                              *debug,
		enableKnowledge:                    *enableKnowledge,
		disableAutoMemoryOnExternalContext: *disableAutoMemoryOnExternalContext,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// autoMemoryChat manages the conversation with auto memory capabilities.
type autoMemoryChat struct {
	modelName                          string
	extractorModel                     string
	memoryType                         util.MemoryType
	appName                            string
	streaming                          bool
	debug                              bool
	enableKnowledge                    bool
	disableAutoMemoryOnExternalContext bool
	runner                             runner.Runner
	memoryService                      memory.Service
	sessionService                     session.Service
	userID                             string
	sessionID                          string
}

// run starts the interactive chat session.
func (c *autoMemoryChat) run() error {
	ctx := context.Background()

	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	defer c.memoryService.Close()

	return c.startChat(ctx)
}

const (
	appName   = "memory-chat"
	agentName = "memory-assistant"
)

func buildLocalKnowledgeTools(ctx context.Context) ([]tool.Tool, error) {
	dataPath, err := localKnowledgeDataPath()
	if err != nil {
		return nil, err
	}
	fileSrc := knowledgefile.New(
		[]string{dataPath},
		knowledgefile.WithName("local docs"),
	)
	kb := knowledge.New(
		knowledge.WithVectorStore(knowledgeinmemory.New()),
		knowledge.WithEmbedder(deterministicEmbedder{}),
		knowledge.WithSources([]source.Source{fileSrc}),
	)
	if err := kb.Load(
		ctx,
		knowledge.WithShowProgress(false),
		knowledge.WithShowStats(false),
	); err != nil {
		return nil, err
	}
	return []tool.Tool{
		knowledgetool.NewKnowledgeSearchTool(
			kb,
			knowledgetool.WithMaxResults(3),
		),
	}, nil
}

func localKnowledgeDataPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(cwd, "../../knowledge/exampledata/file/other.md"),
		filepath.Join(cwd, "../knowledge/exampledata/file/other.md"),
		filepath.Join(cwd, "examples/knowledge/exampledata/file/other.md"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("local knowledge data file not found from %s", cwd)
}

type deterministicEmbedder struct{}

func (deterministicEmbedder) GetEmbedding(
	_ context.Context,
	text string,
) ([]float64, error) {
	return deterministicEmbedding(text), nil
}

func (deterministicEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	embedding, err := deterministicEmbedder{}.GetEmbedding(ctx, text)
	return embedding, nil, err
}

func (deterministicEmbedder) GetDimensions() int {
	return 32
}

func deterministicEmbedding(text string) []float64 {
	const dims = 32
	vector := make([]float64, dims)
	for _, token := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(token))
		vector[int(h.Sum32())%dims]++
	}
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return vector
	}
	norm = math.Sqrt(norm)
	for i := range vector {
		vector[i] /= norm
	}
	return vector
}

// setup creates the runner with LLM agent and auto memory extraction.
func (c *autoMemoryChat) setup(ctx context.Context) error {
	// Create models.
	chatModel := openai.New(c.modelName)
	extractModel := openai.New(c.extractorModel)

	// Create memory extractor with optional extraction checkers.
	// The extractor uses LLM to analyze conversations and extract memories.
	// Checkers control when extraction should be triggered.
	memExtractor := extractor.NewExtractor(
		extractModel,
		// Optional: configure extraction checkers.
		// By default, extraction happens on every turn.
		// Use checkers to control extraction frequency:
		//
		// Example 1: Extract when messages > 5 OR every 3 minutes (OR logic).
		// extractor.WithCheckersAny(
		//     extractor.CheckMessageThreshold(5),
		//     extractor.CheckTimeInterval(3*time.Minute),
		// ),
		//
		// Example 2: Extract when messages > 10 AND every 5 minutes (AND logic).
		// extractor.WithChecker(extractor.CheckMessageThreshold(10)),
		// extractor.WithChecker(extractor.CheckTimeInterval(5*time.Minute)),
	)

	// Create memory service with auto extraction enabled.
	// When extractor is set, write tools (add/update/delete) are hidden, but
	// search and clear tools remain available. Load tool is also hidden in auto mode.
	var err error
	c.memoryService, err = util.NewMemoryServiceByType(c.memoryType, util.MemoryServiceConfig{
		Extractor:                          memExtractor,
		AsyncMemoryNum:                     3,
		MemoryQueueSize:                    100,
		MemoryJobTimeout:                   30 * time.Second,
		DisableAutoMemoryOnExternalContext: c.disableAutoMemoryOnExternalContext,
	})
	if err != nil {
		return fmt.Errorf("failed to create memory service: %w", err)
	}

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("auto-memory-session-%d", time.Now().Unix())

	// Create LLM agent with memory tools.
	// Only search tool is available since extractor is set.
	genConfig := model.GenerationConfig{
		MaxTokens: intPtr(2000),
		Stream:    c.streaming,
	}

	// Create model callbacks for debug mode.
	var modelCallbacks *model.Callbacks
	if c.debug {
		modelCallbacks = model.NewCallbacks()
		modelCallbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			fmt.Println("🔍 Debug: Messages sent to model:")
			for i, msg := range args.Request.Messages {
				fmt.Printf("   [%d] %s: %s\n", i+1, msg.Role, msg.Content)
			}
			fmt.Println()
			return nil, nil
		})
	}

	tools := c.memoryService.Tools()
	if c.enableKnowledge {
		knowledgeTools, err := buildLocalKnowledgeTools(ctx)
		if err != nil {
			_ = c.memoryService.Close()
			return fmt.Errorf("failed to build local knowledge tools: %w", err)
		}
		tools = append(tools, knowledgeTools...)
	}

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(chatModel),
		llmagent.WithDescription("A helpful AI assistant with automatic memory. "+
			"I learn about you from our conversations automatically."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithModelCallbacks(modelCallbacks),
		// Memory preloading injects memories into the system prompt before
		// each request.
		// Use WithPreloadMemory(N) for adaptive preload: load all memories
		// when count <= N, otherwise inject the top-N search results and
		// fall back to directly loading up to N memories if search cannot
		// provide usable results.
		// Use WithPreloadMemory(-1) to load all memories.
		// Default is 0 (disabled, use memory_search/memory_load tools instead).
		llmagent.WithPreloadMemory(-1),
	)

	// Create runner with memory service.
	// The runner will automatically trigger memory extraction after responses.
	c.sessionService = sessioninmemory.NewSessionService()
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(c.sessionService),
		runner.WithMemoryService(c.memoryService),
	)

	util.PrintMemoryInfo(c.memoryType, false)
	fmt.Printf("✅ Auto memory chat ready! Session: %s\n\n", c.sessionID)
	return nil
}

// startChat runs the interactive conversation loop.
func (c *autoMemoryChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /memory   - Show what the system remembers about you")
	fmt.Println("   /state    - Show current memory guard session state")
	fmt.Println("   /new      - Start a new session")
	fmt.Println("   /exit     - End the conversation")
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

		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "/memory":
			c.showMemories(ctx)
			continue
		case "/state":
			c.showState(ctx)
			continue
		case "/new":
			c.startNewSession()
			continue
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

// showMemories displays the current memories for the user.
func (c *autoMemoryChat) showMemories(ctx context.Context) {
	const memoryLimit = 100
	userKey := memory.UserKey{
		AppName: c.appName,
		UserID:  c.userID,
	}

	entries, err := c.memoryService.ReadMemories(ctx, userKey, memoryLimit)
	if err != nil {
		fmt.Printf("❌ Failed to read memories: %v\n", err)
		return
	}

	if len(entries) == 0 {
		fmt.Println("📭 No memories stored yet.")
		fmt.Println("   (Extraction runs asynchronously; wait a bit and")
		fmt.Println("   try /memory again.)")
		fmt.Println()
		return
	}

	fmt.Printf("📚 Stored memories (%d):\n", len(entries))
	for i, entry := range entries {
		if entry.Memory != nil {
			fmt.Printf("   %d. [%s] %s\n", i+1, entry.ID, entry.Memory.Memory)
		}
	}
	fmt.Println()
}

// showState displays memory guard state for the current session.
func (c *autoMemoryChat) showState(ctx context.Context) {
	if c.sessionService == nil {
		fmt.Println("memory:mode = <session service unavailable>")
		return
	}
	sess, err := c.sessionService.GetSession(ctx, session.Key{
		AppName:   c.appName,
		UserID:    c.userID,
		SessionID: c.sessionID,
	})
	if err != nil {
		fmt.Printf("❌ Failed to read session state: %v\n", err)
		return
	}
	if sess == nil {
		fmt.Println("memory:mode = <session not found>")
		return
	}
	mode, ok := sess.GetState(memory.SessionStateKeyMemoryMode)
	if !ok || len(mode) == 0 {
		fmt.Println("memory:mode = <unset>")
		return
	}
	fmt.Printf("memory:mode = %s\n", string(mode))
}

// processMessage handles a single message exchange.
func (c *autoMemoryChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	return c.processResponse(eventChan)
}

// processResponse handles both streaming and non-streaming responses.
func (c *autoMemoryChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent      string
		assistantStarted bool
		finalSeen        bool
	)

	for evt := range eventChan {
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}

		if finalSeen {
			continue
		}

		// Handle tool calls (only load/search in auto mode).
		if c.hasToolCalls(evt) {
			c.handleToolCalls(evt, assistantStarted)
			assistantStarted = true
			continue
		}

		// Handle tool responses.
		if c.hasToolResponses(evt) {
			c.handleToolResponses(evt)
			continue
		}

		// Handle content.
		if content := c.extractContent(evt); content != "" {
			if !assistantStarted {
				assistantStarted = true
			}
			fmt.Print(content)
			fullContent += content
		}

		if evt.IsFinalResponse() {
			fmt.Printf("\n")
			finalSeen = true
		}
	}

	return nil
}

// hasToolCalls checks if the event contains tool calls.
func (c *autoMemoryChat) hasToolCalls(evt *event.Event) bool {
	return len(evt.Response.Choices) > 0 &&
		len(evt.Response.Choices[0].Message.ToolCalls) > 0
}

// hasToolResponses checks if the event contains tool responses.
func (c *autoMemoryChat) hasToolResponses(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			return true
		}
	}
	return false
}

// handleToolCalls displays tool call information.
func (c *autoMemoryChat) handleToolCalls(evt *event.Event, assistantStarted bool) {
	if assistantStarted {
		fmt.Printf("\n")
	}
	fmt.Printf("🔧 Tool calls:\n")
	for _, toolCall := range evt.Response.Choices[0].Message.ToolCalls {
		fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
		}
	}
	fmt.Printf("\n🔄 Executing...\n")
}

// handleToolResponses displays tool response information.
func (c *autoMemoryChat) handleToolResponses(evt *event.Event) {
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ Tool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
		}
	}
}

// extractContent extracts content from the event based on streaming mode.
func (c *autoMemoryChat) extractContent(evt *event.Event) string {
	if len(evt.Response.Choices) == 0 {
		return ""
	}
	choice := evt.Response.Choices[0]
	if c.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

// startNewSession creates a new session ID.
func (c *autoMemoryChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("auto-memory-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Memories persist across sessions)\n")
	fmt.Println()
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
