//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates session summarization with LLM.
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
)

var (
	modelName      = flag.String("model", "deepseek-chat", "Model name to use for LLM summarization and chat")
	streaming      = flag.Bool("streaming", true, "Enable streaming mode for responses")
	flagEvents     = flag.Int("events", 1, "Event count threshold to trigger summarization")
	flagTokens     = flag.Int("tokens", 0, "Token-count threshold to trigger summarization (0=disabled)")
	flagTimeSec    = flag.Int("time-sec", 0, "Time threshold in seconds to trigger summarization (0=disabled)")
	flagMaxWords   = flag.Int("max-words", 0, "Max summary words (0=unlimited)")
	flagSkipRecent = flag.Int("skip-recent", 0, "Number of recent events to skip during summarization (0=skip none)")
	flagAddSum     = flag.Bool("add-summary", true, "Prepend latest branch summary as system message for LLM input")
	flagMaxHist    = flag.Int("max-history", 0, "Max history messages when add-summary=false (0=unlimited)")
)

func main() {
	flag.Parse()

	chat := &summaryChat{
		modelName: *modelName,
	}
	if err := chat.run(); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

// summaryChat manages the conversation and summarization demo.
type summaryChat struct {
	modelName      string
	runner         runner.Runner
	sessionService session.Service
	app            string
	userID         string
	sessionID      string
}

func (c *summaryChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	return c.startChat(ctx)
}

// setup constructs the model, summarizer manager, session service, and runner.
func (c *summaryChat) setup(_ context.Context) error {
	// Model used for both chat and summarization.
	llm := openai.New(c.modelName)

	// Summarizer with customizable prompt.
	// You can customize the summary prompt using WithPrompt().
	// Available placeholders:
	//   - {conversation_text}: The conversation content to be summarized
	//   - {max_summary_words}: The maximum word count for the summary (only included when max-words > 0)
	sum := summary.NewSummarizer(llm, summary.WithMaxSummaryWords(*flagMaxWords),
		summary.WithSkipRecent(func(_ []event.Event) int {
			if *flagSkipRecent > 0 {
				return *flagSkipRecent
			}
			return 0
		}),
		summary.WithChecksAny(
			summary.CheckEventThreshold(*flagEvents),
			summary.CheckTokenThreshold(*flagTokens),
			summary.CheckTimeThreshold(time.Duration(*flagTimeSec)*time.Second),
		),
		// For example:
		// summary.WithPrompt("Summarize this conversation focusing on key decisions: {conversation_text}"),
	)
	// In-memory session service with summarizer and async config.
	// Async summary processing is enabled by default with the following configuration:
	// - 2 async workers: handles concurrent summary generation without blocking
	// - 100 queue size: buffers summary jobs during high traffic
	// You can adjust these values based on your workload:
	//   - Low traffic: 1-2 workers, 50-100 queue size
	//   - Medium traffic: 2-4 workers, 100-200 queue size
	//   - High traffic: 4-8 workers, 200-500 queue size
	sessService := inmemory.NewSessionService(
		inmemory.WithSummarizer(sum),
		inmemory.WithAsyncSummaryNum(2),    // 2 async workers for concurrent summary generation
		inmemory.WithSummaryQueueSize(100), // Queue size 100 to buffer summary jobs during high traffic
		// Timeout for each summary job to avoid long-running LLM calls blocking workers.
		inmemory.WithSummaryJobTimeout(30*time.Second),
	)
	c.sessionService = sessService

	// Agent and runner (non-streaming for concise output).
	ag := llmagent.New(
		"summary-demo-agent",
		llmagent.WithModel(llm),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: *streaming, MaxTokens: intPtr(4000)}),
		llmagent.WithAddSessionSummary(*flagAddSum),
		llmagent.WithMaxHistoryRuns(*flagMaxHist),
	)
	c.app = "summary-demo-app"
	c.runner = runner.NewRunner(c.app, ag, runner.WithSessionService(sessService))

	// IDs.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("summary-session-%d", time.Now().Unix())

	fmt.Printf("📝 Session Summarization Chat\n")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Service: inmemory\n")
	fmt.Printf("EventThreshold: %d\n", *flagEvents)
	fmt.Printf("TokenThreshold: %d\n", *flagTokens)
	fmt.Printf("TimeThreshold: %ds\n", *flagTimeSec)
	fmt.Printf("MaxWords: %d\n", *flagMaxWords)
	fmt.Printf("SkipRecent: %d\n", *flagSkipRecent)
	fmt.Printf("Streaming: %v\n", *streaming)
	fmt.Printf("AddSummary: %v\n", *flagAddSum)
	fmt.Printf("MaxHistory: %d\n", *flagMaxHist)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("✅ Summary chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *summaryChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("💡 Special commands:")
	fmt.Println("   /summary  - Force-generate session summary")
	fmt.Println("   /show     - Show current session summary")
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

// processMessage handles one message: run the agent, print the answer, then create and print the summary.
func (c *summaryChat) processMessage(ctx context.Context, userMessage string) error {
	// Commands
	if strings.EqualFold(userMessage, "/summary") {
		sess, err := c.sessionService.GetSession(ctx, session.Key{AppName: c.app, UserID: c.userID, SessionID: c.sessionID})
		if err != nil || sess == nil {
			fmt.Printf("⚠️ load session failed: %v\n", err)
			return nil
		}
		if err := c.sessionService.CreateSessionSummary(ctx, sess, "", true); err != nil {
			fmt.Printf("⚠️ force summarize failed: %v\n", err)
			return nil
		}
		// Re-fetch session to ensure we read the latest summaries.
		sess, _ = c.sessionService.GetSession(ctx, session.Key{AppName: c.app, UserID: c.userID, SessionID: c.sessionID})
		if text, ok := getSummaryFromSession(sess); ok {
			fmt.Printf("📝 Summary (forced):\n%s\n\n", text)
			return nil
		}
		// Fallback to service helper if no structured summary was found.
		if text, ok := c.sessionService.GetSessionSummaryText(ctx, sess); ok && text != "" {
			fmt.Printf("📝 Summary (forced):\n%s\n\n", text)
		} else {
			fmt.Println("📝 Summary: <empty>.")
		}
		return nil
	}
	if strings.EqualFold(userMessage, "/show") {
		sess, err := c.sessionService.GetSession(ctx, session.Key{AppName: c.app, UserID: c.userID, SessionID: c.sessionID})
		if err != nil || sess == nil {
			fmt.Printf("⚠️ load session failed: %v\n", err)
			return nil
		}
		if text, ok := getSummaryFromSession(sess); ok {
			fmt.Printf("📝 Summary:\n%s\n\n", text)
			return nil
		}
		if text, ok := c.sessionService.GetSessionSummaryText(ctx, sess); ok && text != "" {
			fmt.Printf("📝 Summary:\n%s\n\n", text)
		} else {
			fmt.Println("📝 Summary: <empty>.")
		}
		return nil
	}

	// Normal chat turn (no auto summary printout).
	msg := model.NewUserMessage(userMessage)
	evtCh, err := c.runner.Run(ctx, c.userID, c.sessionID, msg)
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}
	c.consumeResponse(evtCh)
	return nil
}

// consumeResponse reads the event stream and returns the final assistant content.
func (c *summaryChat) consumeResponse(evtCh <-chan *event.Event) string {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent      string
		assistantStarted bool
	)

	for event := range evtCh {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Handle content.
		if content := c.extractContent(event); content != "" {
			if !assistantStarted {
				assistantStarted = true
			}
			fmt.Print(content)
			fullContent += content
		}

		// Don't break on Done - wait for all events including finalizeRun.
		if event.Done {
			fmt.Printf("\n")
		}
	}

	return fullContent
}

// extractContent extracts content from the event based on streaming mode.
func (c *summaryChat) extractContent(event *event.Event) string {
	if event.Response == nil || len(event.Response.Choices) == 0 {
		return ""
	}

	choice := event.Response.Choices[0]
	if *streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

// Helper.
func intPtr(i int) *int { return &i }

// getSummaryFromSession returns a structured summary from the session if present.
// It returns the first available summary from any branch.
func getSummaryFromSession(sess *session.Session) (string, bool) {
	if sess == nil || sess.Summaries == nil || len(sess.Summaries) == 0 {
		return "", false
	}
	// Return the first available summary from any branch.
	for _, s := range sess.Summaries {
		if s != nil && s.Summary != "" {
			return s.Summary, true
		}
	}
	return "", false
}
