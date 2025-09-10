//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates session summarization with LLM using SummarizerManager.
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
	flagModel        = flag.String("model", "deepseek-chat", "Model name to use for LLM summarization and chat")
	flagWindow       = flag.Int("window", 50, "Event window size for summarization input")
	flagEvents       = flag.Int("events", 1, "Event count threshold to trigger summarization")
	flagTokens       = flag.Int("tokens", 0, "Token-count threshold to trigger summarization (0=disabled)")
	flagTimeSec      = flag.Int("timeSec", 0, "Time threshold in seconds to trigger summarization (0=disabled)")
	flagMaxLen       = flag.Int("maxlen", 0, "Max generated summary length (0=unlimited)")
	flagAsyncPersist = flag.Bool("async", false, "Enable async summary persistence on non-force runs")
)

func main() {
	flag.Parse()

	chat := &summaryChat{
		modelName: *flagModel,
		window:    *flagWindow,
	}
	if err := chat.run(); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

// summaryChat manages the conversation and summarization demo.
type summaryChat struct {
	modelName      string
	window         int
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
	return c.startChat(ctx)
}

// setup constructs the model, summarizer manager, session service, and runner.
func (c *summaryChat) setup(_ context.Context) error {
	// Model used for both chat and summarization.
	llm := openai.New(c.modelName)

	// Summarizer and manager.
	var checks []summary.Checker
	if *flagEvents > 0 {
		checks = append(checks, summary.CheckEventThreshold(*flagEvents))
	}
	if *flagTokens > 0 {
		checks = append(checks, summary.CheckTokenThreshold(*flagTokens))
	}
	if *flagTimeSec > 0 {
		checks = append(checks, summary.CheckTimeThreshold(time.Duration(*flagTimeSec)*time.Second))
	}
	sumOpts := []summary.Option{summary.WithWindowSize(c.window)}
	if *flagMaxLen > 0 {
		sumOpts = append(sumOpts, summary.WithMaxSummaryLength(*flagMaxLen))
	}
	if len(checks) > 0 {
		sumOpts = append(sumOpts, summary.WithChecksAny(checks))
	}
	sum := summary.NewSummarizer(llm, sumOpts...)
	mgr := summary.NewManager(sum)

	// In-memory session service with summarizer manager.
	sessService := inmemory.NewSessionService(
		inmemory.WithSummarizerManager(mgr),
		inmemory.WithAsyncSummaryPersist(*flagAsyncPersist),
	)
	c.sessionService = sessService

	// Agent and runner (non-streaming for concise output).
	ag := llmagent.New(
		"summary-demo-agent",
		llmagent.WithModel(llm),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false, MaxTokens: intPtr(800)}),
	)
	c.app = "summary-demo-app"
	c.runner = runner.NewRunner(c.app, ag, runner.WithSessionService(sessService))

	// IDs.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("summary-session-%d", time.Now().Unix())

	fmt.Printf("📝 Session Summarization Chat\n")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Service: inmemory\n")
	fmt.Printf("Window: %d\n", c.window)
	fmt.Printf("EventThreshold: %d\n", *flagEvents)
	fmt.Printf("TokenThreshold: %d\n", *flagTokens)
	fmt.Printf("TimeThreshold: %ds\n", *flagTimeSec)
	fmt.Printf("MaxLen: %d\n", *flagMaxLen)
	fmt.Printf("AsyncPersist: %v\n", *flagAsyncPersist)
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
		if err := c.sessionService.CreateSessionSummary(ctx, sess, true); err != nil {
			fmt.Printf("⚠️ force summarize failed: %v\n", err)
			return nil
		}
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
	final := c.consumeResponse(evtCh)
	fmt.Printf("🤖 Assistant: %s\n", strings.TrimSpace(final))
	return nil
}

// consumeResponse reads the event stream and returns the final assistant content.
func (c *summaryChat) consumeResponse(evtCh <-chan *event.Event) string {
	var final string
	for e := range evtCh {
		if e == nil {
			continue
		}
		if e.Error != nil {
			fmt.Printf("❌ %s\n", e.Error.Message)
			final = ""
			break
		}
		if e.Response != nil && len(e.Response.Choices) > 0 {
			final = e.Response.Choices[0].Message.Content
		}
		if e.Done {
			break
		}
	}
	return final
}

// Helper.
func intPtr(i int) *int { return &i }
