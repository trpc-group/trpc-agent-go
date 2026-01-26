//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates baseline case with knowledge tool search.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/toolsearch/toollibrary/small"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
	inputFile = flag.String("input", "", "Input file with messages (one per line)")
)

func main() {
	flag.Parse()

	chat := &baselineChat{
		modelName: *modelName,
		streaming: *streaming,
		inputFile: *inputFile,
	}

	fmt.Printf("🚀 Tool Search Test: Knowledge Search\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Tools: %d (all tools provided to LLM)\n", len(small.GetTools()))

	if *inputFile != "" {
		fmt.Printf("Input file: %s\n", *inputFile)
	} else {
		fmt.Printf("Type 'exit' to end the conversation\n")
	}
	fmt.Println(strings.Repeat("=", 60))

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

type baselineChat struct {
	modelName string
	streaming bool
	inputFile string
	runner    runner.Runner
	userID    string
	sessionID string

	// Token usage tracking
	sessionUsage *SessionTokenUsage
	turnCount    int
}

type SessionTokenUsage struct {
	OtherChatModelPromptTokens     int
	OtherChatModelCompletionTokens int
	OtherChatModelTokens           int

	ToolSearchPromptTokens     int
	ToolSearchCompletionTokens int
	ToolSearchTokens           int

	TurnCount int

	UsageHistory           []TurnUsage
	ToolSearchUsageHistory []TurnUsage
	ToolSearchTurnCount    int
}

type TurnUsage struct {
	TurnNumber        int
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	Model             string
	InvocationID      string
	Timestamp         time.Time
	UserMessage       string
	AssistantResponse string
	SelectedTools     []string
}

func (c *baselineChat) run() error {
	ctx := context.Background()

	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	defer c.runner.Close()
	return c.startChat(ctx)
}

func (c *baselineChat) setup(_ context.Context) error {
	modelInstance := openai.New(c.modelName)

	genConfig := model.GenerationConfig{
		Stream: c.streaming,
	}

	maxTools := 3
	var SearchToolTurnNumber int
	var SearchToolTurnNumberMutex sync.Mutex

	modelCallbacks := model.NewCallbacks()
	toolKnowledge, err := toolsearch.NewToolKnowledge(openaiembedder.New(openaiembedder.WithModel(openaiembedder.ModelTextEmbedding3Small)),
		toolsearch.WithVectorStore(vectorinmemory.New()))
	if err != nil {
		return fmt.Errorf("failed to create tool knowledge: %w", err)
	}
	if tc, err := toolsearch.New(modelInstance, toolsearch.WithMaxTools(maxTools), toolsearch.WithToolKnowledge(toolKnowledge)); err != nil {
		return fmt.Errorf("failed to create tool selector: %w", err)
	} else {
		modelCallbacks.RegisterBeforeModel(tc.Callback()).RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (res *model.BeforeModelResult, err error) {
			if usage, ok := toolsearch.ToolSearchUsageFromContext(ctx); ok && usage != nil {

				SearchToolTurnNumberMutex.Lock()
				defer SearchToolTurnNumberMutex.Unlock()
				c.addToolSearchTurnUsage(TurnUsage{
					TurnNumber:       SearchToolTurnNumber,
					PromptTokens:     usage.PromptTokens,
					CompletionTokens: usage.CompletionTokens,
					TotalTokens:      usage.TotalTokens})
				ctx = toolsearch.SetToolSearchUsage(ctx, &model.Usage{})
				SearchToolTurnNumber++
			}
			return nil, nil
		})
	}

	agentName := "baseline-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with access to tools including calculator, time, text processing, currency converter, unit converter, password generator, hash generator, base64 converter, email validator, and random number generator"),
		llmagent.WithTools(small.GetTools()), // Provide ALL tools to LLM
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithModelCallbacks(modelCallbacks),
	)

	sessionService := inmemory.NewSessionService()

	appName := "tool-search-baseline"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	c.userID = "user"
	c.sessionID = fmt.Sprintf("baseline-session-%d", time.Now().Unix())
	c.sessionUsage = &SessionTokenUsage{
		UsageHistory: make([]TurnUsage, 0),
	}

	fmt.Printf("✅ Knowledge Search chat ready! Session: %s\n", c.sessionID)
	fmt.Printf("⚠️  Note: only %d of 10 tools are provided to LLM without any search\n\n", maxTools)

	return nil
}

func (c *baselineChat) startChat(ctx context.Context) error {
	// File mode: read messages from file
	if c.inputFile != "" {
		return c.processFile(ctx)
	}

	// Interactive mode: read from stdin
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /stats    - Show current session token usage statistics")
	fmt.Println("   /new      - Start a new session (reset token tracking)")
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
			c.showFinalStats()
			fmt.Println("👋 Goodbye!")
			return nil
		case "/stats":
			c.showStats()
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

func (c *baselineChat) processFile(ctx context.Context) error {
	// Read messages from file
	messages, err := readMessagesFromFile(c.inputFile)
	if err != nil {
		return fmt.Errorf("failed to read messages from file: %w", err)
	}

	fmt.Printf("Processing %d messages from file...\n", len(messages))
	fmt.Println()

	// Process each message
	for i, msg := range messages {
		fmt.Printf("[%d/%d] %s\n", i+1, len(messages), msg)

		if err := c.processMessage(ctx, msg); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println("---")
		fmt.Println()
	}

	// Show final statistics
	c.showFinalStats()

	return nil
}

func readMessagesFromFile(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var messages []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			messages = append(messages, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

func (c *baselineChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)
	c.turnCount++

	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	return c.processResponse(eventChan, userMessage)
}

func (c *baselineChat) processResponse(eventChan <-chan *event.Event, userMessage string) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent   string
		turnUsage     *TurnUsage
		selectedTools []string
	)

	for event := range eventChan {
		// Track token usage
		if event.Response != nil && event.Response.Usage != nil {
			if turnUsage == nil {
				turnUsage = &TurnUsage{
					TurnNumber:   c.turnCount,
					Model:        event.Response.Model,
					InvocationID: event.InvocationID,
					Timestamp:    event.Response.Timestamp,
					UserMessage:  userMessage,
				}
			}

			turnUsage.PromptTokens = event.Response.Usage.PromptTokens
			turnUsage.CompletionTokens = event.Response.Usage.CompletionTokens
			turnUsage.TotalTokens = event.Response.Usage.TotalTokens
		}

		// Track tool calls
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				selectedTools = append(selectedTools, toolCall.Function.Name)
			}
		}

		// Display content
		if len(event.Response.Choices) > 0 {
			if event.Response.Choices[0].Delta.Content != "" {
				fmt.Print(event.Response.Choices[0].Delta.Content)
				fullContent += event.Response.Choices[0].Delta.Content
			} else if event.Response.Choices[0].Message.Content != "" {
				fmt.Print(event.Response.Choices[0].Message.Content)
				fullContent += event.Response.Choices[0].Message.Content
			}
		}

		if event.Done {
			if turnUsage != nil {
				turnUsage.AssistantResponse = fullContent
				turnUsage.SelectedTools = selectedTools
				c.addOtherChatModelTurnUsage(*turnUsage)
			}

			if turnUsage != nil {
				fmt.Printf("\n📊 Turn %d Token Usage:\n", c.turnCount)
				fmt.Printf("   Prompt: %d, Completion: %d, Total: %d\n",
					turnUsage.PromptTokens,
					turnUsage.CompletionTokens,
					turnUsage.TotalTokens)
				if len(selectedTools) > 0 {
					fmt.Printf("   Tools used: %s\n", strings.Join(selectedTools, ", "))
				}
			}

			break
		}
	}

	return nil
}

func (c *baselineChat) addOtherChatModelTurnUsage(usage TurnUsage) {
	c.sessionUsage.OtherChatModelPromptTokens += usage.PromptTokens
	c.sessionUsage.OtherChatModelCompletionTokens += usage.CompletionTokens
	c.sessionUsage.OtherChatModelTokens += usage.TotalTokens
	c.sessionUsage.TurnCount++
	c.sessionUsage.UsageHistory = append(c.sessionUsage.UsageHistory, usage)
}

func (c *baselineChat) addToolSearchTurnUsage(usage TurnUsage) {
	c.sessionUsage.ToolSearchPromptTokens += usage.PromptTokens
	c.sessionUsage.ToolSearchCompletionTokens += usage.CompletionTokens
	c.sessionUsage.ToolSearchTokens += usage.TotalTokens
	c.sessionUsage.ToolSearchTurnCount++
	c.sessionUsage.ToolSearchUsageHistory = append(c.sessionUsage.ToolSearchUsageHistory, usage)
}

func (c *baselineChat) showStats() {
	fmt.Printf("\n📊 Session Token Usage Statistics:\n")
	fmt.Printf("   Total Turns: %d\n", c.sessionUsage.TurnCount)
	fmt.Printf("   Other Chat Model Total Prompt Tokens: %d\n", c.sessionUsage.OtherChatModelPromptTokens)
	fmt.Printf("   Other Chat Model Total Completion Tokens: %d\n", c.sessionUsage.OtherChatModelCompletionTokens)
	fmt.Printf("   Other Chat Model Total Tokens: %d\n", c.sessionUsage.OtherChatModelTokens)
	fmt.Printf("   Tool Search Total Prompt Tokens: %d\n", c.sessionUsage.ToolSearchPromptTokens)
	fmt.Printf("   Tool Search Total Completion Tokens: %d\n", c.sessionUsage.ToolSearchCompletionTokens)
	fmt.Printf("   Tool Search Total Tokens: %d\n", c.sessionUsage.ToolSearchTokens)
	totalPromptTokens := c.sessionUsage.OtherChatModelPromptTokens + c.sessionUsage.ToolSearchPromptTokens
	totalCompletionTokens := c.sessionUsage.OtherChatModelCompletionTokens + c.sessionUsage.ToolSearchCompletionTokens
	totalTokens := c.sessionUsage.OtherChatModelTokens + c.sessionUsage.ToolSearchTokens
	fmt.Printf("   Total Prompt Tokens: %d\n", totalPromptTokens)
	fmt.Printf("   Total Completion Tokens: %d\n", totalCompletionTokens)
	fmt.Printf("   Total Tokens: %d\n", totalTokens)

	if c.sessionUsage.TurnCount > 0 {
		avgPrompt := float64(c.sessionUsage.OtherChatModelPromptTokens) / float64(c.sessionUsage.TurnCount)
		avgCompletion := float64(c.sessionUsage.OtherChatModelCompletionTokens) / float64(c.sessionUsage.TurnCount)
		avgTotal := float64(c.sessionUsage.OtherChatModelTokens) / float64(c.sessionUsage.TurnCount)

		fmt.Printf("   Other Chat Model Average Prompt Tokens per Turn: %.1f\n", avgPrompt)
		fmt.Printf("   Other Chat Model Average Completion Tokens per Turn: %.1f\n", avgCompletion)
		fmt.Printf("   Other Chat Model Average Total Tokens per Turn: %.1f\n", avgTotal)
	}

	if c.sessionUsage.ToolSearchTurnCount > 0 {
		avgToolSearchPrompt := float64(c.sessionUsage.ToolSearchPromptTokens) / float64(c.sessionUsage.ToolSearchTurnCount)
		avgToolSearchCompletion := float64(c.sessionUsage.ToolSearchCompletionTokens) / float64(c.sessionUsage.ToolSearchTurnCount)
		avgToolSearchTotal := float64(c.sessionUsage.ToolSearchTokens) / float64(c.sessionUsage.ToolSearchTurnCount)

		fmt.Printf("   Tool Search Average Prompt Tokens per Turn: %.1f\n", avgToolSearchPrompt)
		fmt.Printf("   Tool Search Average Completion Tokens per Turn: %.1f\n", avgToolSearchCompletion)
		fmt.Printf("   Tool Search Average Total Tokens per Turn: %.1f\n", avgToolSearchTotal)
	}

	if c.sessionUsage.TurnCount > 0 {
		avgTotalPrompt := float64(totalPromptTokens) / float64(c.sessionUsage.TurnCount)
		avgTotalCompletion := float64(totalCompletionTokens) / float64(c.sessionUsage.TurnCount)
		avgTotalTokens := float64(totalTokens) / float64(c.sessionUsage.TurnCount)

		fmt.Printf("   Total Average Prompt Tokens per Turn: %.1f\n", avgTotalPrompt)
		fmt.Printf("   Total Average Completion Tokens per Turn: %.1f\n", avgTotalCompletion)
		fmt.Printf("   Total Average Tokens per Turn: %.1f\n", avgTotalTokens)
	}

	// Print detailed usage history
	if len(c.sessionUsage.UsageHistory) > 0 {
		fmt.Printf("\n📋 Turn-by-Turn Usage History:\n")
		for _, usage := range c.sessionUsage.UsageHistory {
			fmt.Printf("\n   Turn %d:\n", usage.TurnNumber)
			fmt.Printf("      Other Chat Model PromptTokens: %d\n", usage.PromptTokens)
			fmt.Printf("      Other Chat Model CompletionTokens: %d\n", usage.CompletionTokens)
			fmt.Printf("      Other Chat Model TotalTokens: %d\n", usage.TotalTokens)
			if len(usage.SelectedTools) > 0 {
				fmt.Printf("      SelectedTools: %s\n", strings.Join(usage.SelectedTools, ", "))
			}
		}
	}

	// Print tool search usage history
	if len(c.sessionUsage.ToolSearchUsageHistory) > 0 {
		fmt.Printf("\n🔍 Tool Search Turn-by-Turn Usage History:\n")
		for _, usage := range c.sessionUsage.ToolSearchUsageHistory {
			fmt.Printf("\n   Turn %d:\n", usage.TurnNumber)
			fmt.Printf("      Tool Search PromptTokens: %d\n", usage.PromptTokens)
			fmt.Printf("      Tool Search CompletionTokens: %d\n", usage.CompletionTokens)
			fmt.Printf("      Tool Search TotalTokens: %d\n", usage.TotalTokens)
			if len(usage.SelectedTools) > 0 {
				fmt.Printf("      SelectedTools: %s\n", strings.Join(usage.SelectedTools, ", "))
			}
		}
	}

	fmt.Println()
}

func (c *baselineChat) showFinalStats() {
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Printf("🎯 Final Session Statistics (Without Tool Search):\n")
	c.showStats()
}

func (c *baselineChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("baseline-session-%d", time.Now().Unix())
	c.sessionUsage = &SessionTokenUsage{
		UsageHistory: make([]TurnUsage, 0),
	}
	c.turnCount = 0

	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   Token tracking has been reset.\n")
	fmt.Println()
}
