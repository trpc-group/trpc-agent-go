//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates session summarization with custom filterKey support.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
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
)

func main() {
	flag.Parse()

	chat := &filterKeyChat{
		modelName: *modelName,
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
}

func (c *filterKeyChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
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
			function.WithDescription("Performs basic arithmetic operations like addition, subtraction, multiplication, and division. Use this for any mathematical calculations.")),
		function.NewFunctionTool(c.getCurrentTime, function.WithName("get_current_time"),
			function.WithDescription("Gets the current time and date information for different timezones. Use this when asked about current time.")),
	}

	// Agent and runner with tools.
	ag := llmagent.New(
		"filterkey-demo-agent",
		llmagent.WithModel(llm),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools."),
		llmagent.WithInstruction("Use the calculator tool for math; use the time tool for time queries. Make exactly one tool call for a single question, never repeat. After the tool returns, directly give the final answer in one short sentence. 请只调用一次工具，拿到结果后用一句中文或英文直接回答，不要再次调用工具。"),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    *streaming,
			MaxTokens: intPtr(4000),
		}),
		llmagent.WithTools(tools),
		llmagent.WithEnableParallelTools(false),
	)
	c.app = "filterkey-demo-app"
	c.runner = runner.NewRunner(c.app, ag, runner.WithSessionService(sessService))

	// IDs.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("filterkey-session-%d", time.Now().Unix())

	fmt.Printf("📝 Filter-Key Summarization Chat\n")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Service: inmemory\n")
	fmt.Printf("MaxWords: %d\n", *maxWords)
	fmt.Printf("Streaming: %v\n", *streaming)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("✅ Filter-key chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// setEventFilterKey demonstrates how to set custom filterKey based on event
// author. Always prefix with app so it matches the runner's invocation
// filter key; otherwise history gets filtered out and models may keep
// re-triggering tools.
func (c *filterKeyChat) setEventFilterKey(evt *event.Event) {
	if evt == nil {
		return
	}
	// Set filterKey based on author to demonstrate custom filtering.
	// Use app-prefixed keys so they match the invocation's filter prefix.
	prefix := c.app + "/"
	switch evt.Author {
	case "user":
		evt.FilterKey = prefix + "user-messages"
	case "tool":
		evt.FilterKey = prefix + "tool-calls"
	default:
		// Assistant messages and others go to misc
		evt.FilterKey = prefix + "misc"
	}
}

// startChat runs the interactive conversation loop.
func (c *filterKeyChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("💡 Special commands:")
	fmt.Println("   /summary [filterKey] - Force summarize by filter (default: full)")
	fmt.Println("   /show [filterKey]    - Show summary by filter (default: full)")
	fmt.Println("   /list                - List all filterKeys and summaries in session")
	fmt.Println("   /exit                - End the conversation")
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

// processMessage handles one message: run the agent, print the answer.
func (c *filterKeyChat) processMessage(ctx context.Context, userMessage string) error {
	// Handle summary commands
	if strings.HasPrefix(userMessage, "/summary") {
		return c.handleSummaryCommand(ctx, userMessage, true)
	}
	if strings.HasPrefix(userMessage, "/show") {
		return c.handleSummaryCommand(ctx, userMessage, false)
	}
	if strings.EqualFold(userMessage, "/list") {
		return c.handleListSummaries(ctx)
	}

	// Normal chat turn
	return c.handleChatTurn(ctx, userMessage)
}

// handleSummaryCommand handles /summary and /show commands.
func (c *filterKeyChat) handleSummaryCommand(ctx context.Context, userMessage string, force bool) error {
	prefix := "/show"
	if force {
		prefix = "/summary"
	}

	filterKey := strings.TrimSpace(strings.TrimPrefix(userMessage, prefix))
	if filterKey == "" {
		filterKey = session.SummaryFilterKeyAllContents // Default to full session
	}

	sess, err := c.sessionService.GetSession(ctx, session.Key{AppName: c.app, UserID: c.userID, SessionID: c.sessionID})
	if err != nil || sess == nil {
		fmt.Printf("⚠️ load session failed: %v\n", err)
		return nil
	}

	if force {
		if err := c.sessionService.CreateSessionSummary(ctx, sess, filterKey, true); err != nil {
			fmt.Printf("⚠️ force summarize failed: %v\n", err)
			return nil
		}
		// Re-fetch session to ensure we read the latest summaries.
		sess, _ = c.sessionService.GetSession(ctx, session.Key{AppName: c.app, UserID: c.userID, SessionID: c.sessionID})
	}

	ensureAggregated(sess)
	c.displaySummary(sess, filterKey, force)
	return nil
}

// handleChatTurn handles normal chat messages.
func (c *filterKeyChat) handleChatTurn(ctx context.Context, userMessage string) error {
	fmt.Println("💡 FilterKey Demo: Events are automatically categorized by author via AppendEvent hooks:")
	fmt.Printf("   - User messages → filterKey: '%s/user-messages'\n", c.app)
	fmt.Printf("   - Tool calls → filterKey: '%s/tool-calls'\n", c.app)
	fmt.Printf("   - Assistant/other → filterKey: '%s/misc'\n", c.app)
	fmt.Println()

	// Run the agent with the user message
	msg := model.NewUserMessage(userMessage)
	evtCh, err := c.runner.Run(ctx, c.userID, c.sessionID, msg)
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}

	// Process the response
	c.consumeResponse(evtCh, *streaming)
	return nil
}

// displaySummary displays a summary for the given filter key.
func (c *filterKeyChat) displaySummary(sess *session.Session, filterKey string, forced bool) {
	var text string
	var ok bool

	// Try structured summary first
	if text, ok = getSummaryFromSession(sess, filterKey); !ok {
		// Fallback to service helper
		if t, o := c.sessionService.GetSessionSummaryText(context.Background(), sess, session.WithSummaryFilterKey(filterKey)); o && t != "" {
			text = t
			ok = true
		}
	}

	forceText := ""
	if forced {
		forceText = " (forced)"
	}

	if ok && text != "" {
		if filterKey == session.SummaryFilterKeyAllContents {
			fmt.Printf("📝 Summary%s:\n%s\n\n", forceText, text)
		} else {
			fmt.Printf("📝 Summary[%s]%s:\n%s\n\n", filterKey, forceText, text)
		}
	} else {
		if filterKey == session.SummaryFilterKeyAllContents {
			fmt.Printf("📝 Summary%s: <empty>.\n", forceText)
		} else {
			fmt.Printf("📝 Summary[%s]%s: <empty>.\n", filterKey, forceText)
		}
	}
}

// handleListSummaries prints all filterKeys and their summaries in the session.
func (c *filterKeyChat) handleListSummaries(ctx context.Context) error {
	sess, err := c.sessionService.GetSession(ctx, session.Key{
		AppName: c.app, UserID: c.userID, SessionID: c.sessionID,
	})
	if err != nil || sess == nil {
		fmt.Printf("⚠️ load session failed: %v\n", err)
		return nil
	}

	if len(sess.Summaries) == 0 {
		ensureAggregated(sess)
	}

	if len(sess.Summaries) == 0 {
		fmt.Println("📝 Summaries: <empty>.")
		return nil
	}

	fmt.Println("📝 Summaries (filterKey → summary):")
	for k, v := range sess.Summaries {
		s := ""
		if v != nil {
			s = v.Summary
		}
		if s == "" {
			s = "<empty>"
		}
		fmt.Printf("- %s\n  %s\n", k, s)
	}
	fmt.Println()
	return nil
}

// ensureAggregated ensures session has aggregated summaries as fallback.
func ensureAggregated(sess *session.Session) {
	if len(sess.Summaries) > 0 {
		return
	}
	aggregateSummaries(sess, []string{
		"user-messages", "tool-calls", "misc", session.SummaryFilterKeyAllContents,
	})
}

// aggregateSummaries creates simple aggregated summaries when LLM is not available.
func aggregateSummaries(sess *session.Session, keys []string) {
	if sess.Summaries == nil {
		sess.Summaries = make(map[string]*session.Summary)
	}
	contentsByKey := make(map[string][]string)
	for _, key := range keys {
		contentsByKey[key] = []string{}
	}
	for _, evt := range sess.Events {
		content := extractContent(&evt)
		if content == "" {
			continue
		}
		// Map event to filterKey based on author
		var filterKey string
		switch evt.Author {
		case "user":
			filterKey = "user-messages"
		case "tool":
			filterKey = "tool-calls"
		default:
			filterKey = "misc"
		}
		if _, ok := contentsByKey[filterKey]; ok {
			contentsByKey[filterKey] = append(contentsByKey[filterKey], content)
		}
		contentsByKey[session.SummaryFilterKeyAllContents] = append(
			contentsByKey[session.SummaryFilterKeyAllContents], content)
	}
	for key, vals := range contentsByKey {
		if len(vals) == 0 {
			continue
		}
		sess.Summaries[key] = &session.Summary{
			Summary:   fmt.Sprintf("[%s] %d event(s): %s", key, len(vals), strings.Join(vals, "; ")),
			UpdatedAt: time.Now().UTC(),
		}
	}
}

// consumeResponse reads the event stream and displays the assistant response.
func (c *filterKeyChat) consumeResponse(evtCh <-chan *event.Event, streaming bool) string {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent      string
		assistantStarted bool
		toolCallsSeen    bool
		seenToolCallIDs  = make(map[string]struct{})
	)

	for event := range evtCh {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Tool call events (assistant requesting a tool).
		if c.handleToolCalls(event, streaming, seenToolCallIDs) {
			toolCallsSeen = true
			continue
		}

		// Tool response events (tool role).
		if c.handleToolResponses(event, seenToolCallIDs) {
			continue
		}

		// Handle content.
		if content := c.extractContent(event, streaming); content != "" {
			if !assistantStarted {
				assistantStarted = true
			}
			fmt.Print(content)
			fullContent += content
		}

		// Final assistant response.
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	// If a tool call was seen but no final response printed, add a newline for cleanliness.
	if toolCallsSeen {
		fmt.Printf("\n")
	}

	return fullContent
}

// handleToolCalls logs tool calls when the LLM requests them, de-duplicating by ID.
func (c *filterKeyChat) handleToolCalls(event *event.Event, streaming bool, seen map[string]struct{}) bool {
	if event.Response == nil || len(event.Response.Choices) == 0 {
		return false
	}

	choice := event.Response.Choices[0]

	// Streaming tool calls live in Delta; non-streaming in Message.
	var toolCalls []model.ToolCall
	if streaming {
		toolCalls = choice.Delta.ToolCalls
	} else {
		toolCalls = choice.Message.ToolCalls
	}

	if len(toolCalls) == 0 {
		return false
	}

	fmt.Printf("\n🔧 Callable tool calls initiated:\n")
	for _, call := range toolCalls {
		if call.ID != "" {
			if _, ok := seen[call.ID]; ok {
				continue
			}
			seen[call.ID] = struct{}{}
		}
		fmt.Printf("   • %s (ID: %s)\n", call.Function.Name, call.ID)
		if len(call.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", strings.TrimSpace(string(call.Function.Arguments)))
		}
	}
	fmt.Printf("\n🔄 Executing tools...\n")
	return true
}

// handleToolResponses logs tool outputs (tool role messages), de-duplicating by ToolID.
func (c *filterKeyChat) handleToolResponses(event *event.Event, seen map[string]struct{}) bool {
	if event.Response == nil || len(event.Response.Choices) == 0 {
		return false
	}

	hasToolResponse := false
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			if _, ok := seen[choice.Message.ToolID]; ok {
				continue
			}
			seen[choice.Message.ToolID] = struct{}{}
			fmt.Printf("✅ Callable tool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
			hasToolResponse = true
		}
	}
	return hasToolResponse
}

// extractContent extracts content from the event based on streaming mode.
func (c *filterKeyChat) extractContent(event *event.Event, streaming bool) string {
	if event.Response == nil || len(event.Response.Choices) == 0 {
		return ""
	}

	choice := event.Response.Choices[0]
	if streaming {
		// Skip tool responses in streaming mode.
		if choice.Delta.Role == model.RoleTool {
			return ""
		}

		// In streaming mode, content comes from Delta
		content := choice.Delta.Content

		// Handle tool calls in streaming mode
		if len(choice.Delta.ToolCalls) > 0 {
			// Emit a short marker so users know a tool is being called.
			content += "[TOOL_CALL]"
		}

		return content
	}

	// Skip tool responses in non-streaming mode
	if choice.Message.Role == model.RoleTool {
		return ""
	}

	// In non-streaming mode, content comes from Message
	content := choice.Message.Content

	// Handle tool calls in non-streaming mode
	if len(choice.Message.ToolCalls) > 0 {
		content += "[TOOL_CALL]"
	}

	return content
}
