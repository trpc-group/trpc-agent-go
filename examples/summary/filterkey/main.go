//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates session summarization with custom filterKey support.
// This example shows how to use AppendEventHook to set custom filterKeys for
// categorizing conversations, enabling separate summaries per category.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model name for LLM summarization")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
	maxWords  = flag.Int("max-words", 0, "Max summary words (0=unlimited)")
	debug     = flag.Bool("debug", false, "Enable debug mode to print request messages")
)

const defaultFilterKey = "default"

func main() {
	flag.Parse()

	chat := &filterKeyChat{
		modelName:        *modelName,
		currentFilterKey: defaultFilterKey,
	}
	if err := chat.run(); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

// filterKeyChat manages the conversation and filterKey summarization demo.
type filterKeyChat struct {
	modelName      string
	runner         runner.Runner
	sessionService session.Service
	app            string
	userID         string
	sessionID      string

	// currentFilterKey is the active filterKey for new messages.
	currentFilterKey string
	filterKeyMu      sync.RWMutex
}

func (c *filterKeyChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up.
	defer c.runner.Close()

	return c.startChat(ctx)
}

// setup constructs the model, summarizer manager, session service, and runner.
func (c *filterKeyChat) setup(_ context.Context) error {
	// Model used for both chat and summarization.
	llm := openai.New(c.modelName)

	// Summarizer with custom filterKey support.
	sum := summary.NewSummarizer(llm, summary.WithMaxSummaryWords(*maxWords))

	// In-memory session service with summarizer and AppendEventHook.
	// The hook sets filterKey based on the current user-defined key.
	sessService := inmemory.NewSessionService(
		inmemory.WithSummarizer(sum),
		inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
			c.setEventFilterKey(ctx.Event)
			return next()
		}),
	)
	c.sessionService = sessService

	// Create tools for the agent.
	tools := []tool.Tool{
		function.NewFunctionTool(c.calculate, function.WithName("calculate"),
			function.WithDescription("Performs basic arithmetic operations.")),
		function.NewFunctionTool(c.getCurrentTime, function.WithName("get_current_time"),
			function.WithDescription("Gets the current time for different timezones.")),
	}

	// Agent and runner with tools.
	agentOpts := []llmagent.Option{
		llmagent.WithModel(llm),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools."),
		llmagent.WithInstruction("Use the calculator tool for math; use the time tool " +
			"for time queries. After the tool returns, give the final answer in one sentence."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    *streaming,
			MaxTokens: intPtr(4000),
		}),
		llmagent.WithTools(tools),
		llmagent.WithAddSessionSummary(true),
	}

	// Add debug callback if enabled.
	if *debug {
		debugCallbacks := model.NewCallbacks().RegisterBeforeModel(
			func(_ context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
				fmt.Println("🐛 [DEBUG] Request messages:")
				for i, msg := range args.Request.Messages {
					content := msg.Content
					if len(content) > 200 {
						content = content[:200] + "..."
					}
					fmt.Printf("   [%d] %s: %s\n", i, msg.Role, content)
				}
				fmt.Println()
				return nil, nil
			})
		agentOpts = append(agentOpts, llmagent.WithModelCallbacks(debugCallbacks))
	}

	ag := llmagent.New("filterkey-demo-agent", agentOpts...)
	c.app = "filterkey-demo-app"
	c.runner = runner.NewRunner(c.app, ag, runner.WithSessionService(sessService))

	// IDs.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("filterkey-session-%d", time.Now().Unix())

	fmt.Printf("📝 Filter-Key Summarization Demo\n")
	fmt.Printf("Model: %s | Streaming: %v | MaxWords: %d | Debug: %v\n",
		c.modelName, *streaming, *maxWords, *debug)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Session: %s\n\n", c.sessionID)

	return nil
}

// setEventFilterKey sets the filterKey based on the current user-defined key.
func (c *filterKeyChat) setEventFilterKey(evt *event.Event) {
	if evt == nil {
		return
	}
	c.filterKeyMu.RLock()
	key := c.currentFilterKey
	c.filterKeyMu.RUnlock()

	// Use app-prefixed keys so they match the invocation's filter prefix.
	evt.FilterKey = c.app + "/" + key
}

// setCurrentFilterKey updates the current filterKey.
func (c *filterKeyChat) setCurrentFilterKey(key string) {
	c.filterKeyMu.Lock()
	c.currentFilterKey = key
	c.filterKeyMu.Unlock()
}

// getCurrentFilterKey returns the current filterKey.
func (c *filterKeyChat) getCurrentFilterKey() string {
	c.filterKeyMu.RLock()
	defer c.filterKeyMu.RUnlock()
	return c.currentFilterKey
}

// startChat runs the interactive conversation loop.
func (c *filterKeyChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	c.printHelp()

	for {
		key := c.getCurrentFilterKey()
		fmt.Printf("👤 [%s] You: ", key)
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}
		if strings.EqualFold(userInput, "/exit") {
			fmt.Println("👋 Bye.")
			return nil
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *filterKeyChat) printHelp() {
	fmt.Println("💡 This demo shows how to use filterKey to categorize conversations.")
	fmt.Println("   Each filterKey gets its own separate summary.")
	fmt.Println()
	fmt.Println("📌 Commands:")
	fmt.Println("   /key <name>    - Switch to a filterKey (any name you want)")
	fmt.Println("   /show [key]    - Show summary for a filterKey (default: current)")
	fmt.Println("   /list          - List all summaries")
	fmt.Println("   /help          - Show this help")
	fmt.Println("   /exit          - End the conversation")
	fmt.Println()
}

// processMessage handles one message: run the agent, print the answer.
func (c *filterKeyChat) processMessage(ctx context.Context, userMessage string) error {
	// Handle commands.
	if strings.HasPrefix(userMessage, "/key") {
		return c.handleKeyCommand(userMessage)
	}
	if strings.HasPrefix(userMessage, "/show") {
		return c.handleShowCommand(ctx, userMessage)
	}
	if strings.EqualFold(userMessage, "/list") {
		return c.handleListSummaries(ctx)
	}
	if strings.EqualFold(userMessage, "/help") {
		c.printHelp()
		return nil
	}

	// Normal chat turn.
	return c.handleChatTurn(ctx, userMessage)
}

// handleKeyCommand switches the current filterKey.
func (c *filterKeyChat) handleKeyCommand(userMessage string) error {
	key := strings.TrimSpace(strings.TrimPrefix(userMessage, "/key"))
	if key == "" {
		fmt.Printf("📌 Current filterKey: %s\n", c.getCurrentFilterKey())
		return nil
	}

	c.setCurrentFilterKey(key)
	fmt.Printf("📌 Switched to filterKey: %s\n", key)
	fmt.Println("   All messages will now be categorized under this key.")
	return nil
}

// handleShowCommand shows the summary for a specific filterKey.
func (c *filterKeyChat) handleShowCommand(ctx context.Context, userMessage string) error {
	key := strings.TrimSpace(strings.TrimPrefix(userMessage, "/show"))
	if key == "" {
		key = c.getCurrentFilterKey()
	}

	sess, err := c.sessionService.GetSession(ctx, session.Key{
		AppName: c.app, UserID: c.userID, SessionID: c.sessionID,
	})
	if err != nil || sess == nil {
		fmt.Printf("⚠️ Load session failed: %v\n", err)
		return nil
	}

	filterKey := c.app + "/" + key
	c.displaySummary(sess, filterKey, key)
	return nil
}

// handleChatTurn handles normal chat messages.
func (c *filterKeyChat) handleChatTurn(ctx context.Context, userMessage string) error {
	msg := model.NewUserMessage(userMessage)
	evtCh, err := c.runner.Run(ctx, c.userID, c.sessionID, msg)
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}

	c.consumeResponse(evtCh, *streaming)
	return nil
}

// displaySummary displays a summary for the given filter key.
func (c *filterKeyChat) displaySummary(sess *session.Session, filterKey, displayName string) {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()

	if sess.Summaries == nil {
		fmt.Printf("📝 Summary[%s]: <empty>\n\n", displayName)
		return
	}

	sum, ok := sess.Summaries[filterKey]
	if !ok || sum == nil || sum.Summary == "" {
		fmt.Printf("📝 Summary[%s]: <empty>\n\n", displayName)
		return
	}

	fmt.Printf("📝 Summary[%s]:\n%s\n\n", displayName, sum.Summary)
}

// handleListSummaries prints all filterKeys and their summaries in the session.
func (c *filterKeyChat) handleListSummaries(ctx context.Context) error {
	sess, err := c.sessionService.GetSession(ctx, session.Key{
		AppName: c.app, UserID: c.userID, SessionID: c.sessionID,
	})
	if err != nil || sess == nil {
		fmt.Printf("⚠️ Load session failed: %v\n", err)
		return nil
	}

	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()

	if len(sess.Summaries) == 0 {
		fmt.Println("📝 No summaries yet. Chat more to generate summaries!")
		return nil
	}

	fmt.Println("📝 All Summaries:")
	fmt.Println(strings.Repeat("-", 50))
	for key, sum := range sess.Summaries {
		// Extract display name from filterKey (e.g., "app/math" -> "math").
		displayName := key
		if after, ok := strings.CutPrefix(key, c.app+"/"); ok {
			displayName = after
		}
		if key == "" || key == session.SummaryFilterKeyAllContents {
			displayName = "(full session)"
		}

		text := "<empty>"
		if sum != nil && sum.Summary != "" {
			text = sum.Summary
		}
		fmt.Printf("[%s]\n%s\n\n", displayName, text)
	}
	return nil
}

// consumeResponse reads the event stream and displays the assistant response.
func (c *filterKeyChat) consumeResponse(evtCh <-chan *event.Event, streaming bool) string {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent     strings.Builder
		seenToolCallIDs = make(map[string]struct{})
	)

	for evt := range evtCh {
		// Handle errors.
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}

		// Tool call events.
		if c.handleToolCalls(evt, streaming, seenToolCallIDs) {
			continue
		}

		// Tool response events.
		if c.handleToolResponses(evt, seenToolCallIDs) {
			continue
		}

		// Handle content.
		if content := c.extractContent(evt, streaming); content != "" {
			fmt.Print(content)
			fullContent.WriteString(content)
		}

		// Final response.
		if evt.IsFinalResponse() {
			fmt.Println()
			break
		}
	}

	return fullContent.String()
}

// handleToolCalls logs tool calls when the LLM requests them.
func (c *filterKeyChat) handleToolCalls(evt *event.Event, streaming bool, seen map[string]struct{}) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}

	choice := evt.Response.Choices[0]
	var toolCalls []model.ToolCall
	if streaming {
		toolCalls = choice.Delta.ToolCalls
	} else {
		toolCalls = choice.Message.ToolCalls
	}

	if len(toolCalls) == 0 {
		return false
	}

	for _, call := range toolCalls {
		if call.ID != "" {
			if _, ok := seen[call.ID]; ok {
				continue
			}
			seen[call.ID] = struct{}{}
		}
		fmt.Printf("\n🔧 Tool: %s(%s)\n", call.Function.Name,
			strings.TrimSpace(string(call.Function.Arguments)))
	}
	return true
}

// handleToolResponses logs tool outputs.
func (c *filterKeyChat) handleToolResponses(evt *event.Event, seen map[string]struct{}) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}

	hasToolResponse := false
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			if _, ok := seen[choice.Message.ToolID]; ok {
				continue
			}
			seen[choice.Message.ToolID] = struct{}{}
			fmt.Printf("   → %s\n", strings.TrimSpace(choice.Message.Content))
			hasToolResponse = true
		}
	}
	return hasToolResponse
}

// extractContent extracts content from the event based on streaming mode.
func (c *filterKeyChat) extractContent(evt *event.Event, streaming bool) string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}

	choice := evt.Response.Choices[0]
	if streaming {
		if choice.Delta.Role == model.RoleTool {
			return ""
		}
		return choice.Delta.Content
	}

	if choice.Message.Role == model.RoleTool {
		return ""
	}
	return choice.Message.Content
}
