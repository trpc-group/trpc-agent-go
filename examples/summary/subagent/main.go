//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates session summarization behaviour when
// the primary agent delegates to a sub-agent via agenttool.
//
// It uses a low token threshold so that summarisation triggers
// quickly, making it easy to observe which events are included in
// the threshold check and when the summary fires.
package main

import (
	"bufio"
	"context"
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
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName        = "summary-subagent-demo"
	parentAgent    = "parent-agent"
	childAgentName = "math-specialist"
)

func main() {
	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "deepseek-v3.2"
	}
	chat := &demo{modelName: modelName}
	if err := chat.run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

type demo struct {
	modelName      string
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

func (d *demo) run() error {
	ctx := context.Background()
	if err := d.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer d.runner.Close()
	return d.startChat(ctx)
}

func (d *demo) setup() error {
	llm := openai.New(d.modelName)

	// Low thresholds to make summary trigger quickly.
	const tokenThreshold = 200
	sum := summary.NewSummarizer(
		llm,
		summary.WithMaxSummaryWords(100),
		summary.WithChecksAny(
			summary.CheckTokenThreshold(tokenThreshold),
			summary.CheckEventThreshold(2),
		),
	)

	d.sessionService = inmemory.NewSessionService(
		inmemory.WithSummarizer(sum),
		inmemory.WithAsyncSummaryNum(1),
		inmemory.WithSummaryQueueSize(64),
		inmemory.WithSummaryJobTimeout(45*time.Second),
	)

	// Child agent with a calculator tool.
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription(
			"Perform basic arithmetic (add, subtract, multiply, divide)."),
	)

	childAgent := llmagent.New(
		childAgentName,
		llmagent.WithModel(llm),
		llmagent.WithDescription(
			"A specialist for mathematical calculations."),
		llmagent.WithInstruction(
			"You are a math specialist. Use the calculator tool "+
				"for any arithmetic. Return the result concisely."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(800),
			Stream:    true,
		}),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
	)

	// Wrap the child agent as a tool for the parent.
	childTool := agenttool.NewTool(
		childAgent,
		agenttool.WithStreamInner(true),
	)

	// Parent agent.
	parentLLM := llmagent.New(
		parentAgent,
		llmagent.WithModel(llm),
		llmagent.WithDescription("A helpful AI assistant."),
		llmagent.WithInstruction(
			"For math questions delegate to the math-specialist "+
				"agent tool. For other questions answer directly."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(1500),
			Stream:    true,
		}),
		llmagent.WithTools([]tool.Tool{childTool}),
		llmagent.WithAddSessionSummary(true),
		// Register a BeforeModel callback to show when the summary
		// has been injected.
		llmagent.WithModelCallbacks(
			model.NewCallbacks().RegisterBeforeModel(
				beforeModel)),
	)

	d.runner = runner.NewRunner(
		appName, parentLLM,
		runner.WithSessionService(d.sessionService),
	)

	d.userID = "user"
	d.sessionID = fmt.Sprintf("sess-%d", time.Now().Unix())

	fmt.Println("Session Summarization + Sub-Agent Demo")
	fmt.Printf("Model:          %s\n", d.modelName)
	fmt.Printf("TokenThreshold: %d\n", tokenThreshold)
	fmt.Printf("EventThreshold: %d\n", 2)
	fmt.Printf("Session:        %s\n", d.sessionID)
	fmt.Println(strings.Repeat("=", 60))
	return nil
}

func (d *demo) startChat(ctx context.Context) error {
	sc := bufio.NewScanner(os.Stdin)
	fmt.Println("Commands:")
	fmt.Println("  /show   - display all summaries")
	fmt.Println("  /events - dump session events")
	fmt.Println("  /exit   - quit")
	fmt.Println()

	for {
		fmt.Print("You: ")
		if !sc.Scan() {
			break
		}
		input := strings.TrimSpace(sc.Text())
		if input == "" {
			continue
		}
		switch strings.ToLower(input) {
		case "/exit":
			fmt.Println("Bye.")
			return nil
		case "/show":
			d.showSummaries(ctx)
			continue
		case "/events":
			d.dumpEvents(ctx)
			continue
		}
		if err := d.chat(ctx, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	return sc.Err()
}

func (d *demo) chat(ctx context.Context, msg string) error {
	evtCh, err := d.runner.Run(
		ctx, d.userID, d.sessionID,
		model.NewUserMessage(msg),
	)
	if err != nil {
		return err
	}
	fmt.Print("Assistant: ")
	for evt := range evtCh {
		if evt.Error != nil {
			fmt.Printf("\nError: %s\n", evt.Error.Message)
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		c := evt.Response.Choices[0]
		if c.Delta.Content != "" {
			fmt.Print(c.Delta.Content)
		}
		if evt.Done {
			fmt.Println()
		}
	}
	return nil
}

func (d *demo) showSummaries(ctx context.Context) {
	sess, err := d.sessionService.GetSession(ctx, session.Key{
		AppName: appName, UserID: d.userID, SessionID: d.sessionID,
	})
	if err != nil || sess == nil {
		fmt.Printf("load session failed: %v\n", err)
		return
	}
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()

	if len(sess.Summaries) == 0 {
		fmt.Println("[no summaries yet]")
		return
	}
	for key, s := range sess.Summaries {
		display := key
		if key == "" {
			display = "(full-session)"
		}
		text := "<empty>"
		if s != nil && s.Summary != "" {
			text = s.Summary
		}
		fmt.Printf("--- Summary [%s] ---\n%s\n\n", display, text)
	}
}

func (d *demo) dumpEvents(ctx context.Context) {
	sess, err := d.sessionService.GetSession(ctx, session.Key{
		AppName: appName, UserID: d.userID, SessionID: d.sessionID,
	})
	if err != nil || sess == nil {
		fmt.Printf("load session failed: %v\n", err)
		return
	}
	fmt.Printf("Total events: %d\n", len(sess.Events))
	for i, e := range sess.Events {
		content := extractPreview(e)
		fmt.Printf("  [%d] author=%-20s filterKey=%-30s content=%s\n",
			i, e.Author, e.FilterKey, content)
	}
}

func extractPreview(e event.Event) string {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return "<no content>"
	}
	c := e.Response.Choices[0].Message.Content
	if c == "" {
		c = e.Response.Choices[0].Delta.Content
	}
	if len(c) > 80 {
		c = c[:80] + "..."
	}
	return c
}

func beforeModel(
	_ context.Context, args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	for _, msg := range args.Request.Messages {
		if msg.Role == model.RoleSystem &&
			strings.Contains(msg.Content,
				"<summary_of_previous_interactions>") {
			fmt.Println("[summary injected into prompt]")
			break
		}
	}
	return nil, nil
}

// calculate performs basic arithmetic.
func calculate(
	_ context.Context, args calcArgs,
) (calcResult, error) {
	var r float64
	switch strings.ToLower(args.Op) {
	case "add", "+":
		r = args.A + args.B
	case "subtract", "-":
		r = args.A - args.B
	case "multiply", "*":
		r = args.A * args.B
	case "divide", "/":
		if args.B != 0 {
			r = args.A / args.B
		}
	}
	return calcResult{Op: args.Op, A: args.A, B: args.B, Result: r}, nil
}

type calcArgs struct {
	Op string  `json:"operation" jsonschema:"description=add subtract multiply divide"`
	A  float64 `json:"a" jsonschema:"description=First number"`
	B  float64 `json:"b" jsonschema:"description=Second number"`
}

type calcResult struct {
	Op     string  `json:"operation"`
	A      float64 `json:"a"`
	B      float64 `json:"b"`
	Result float64 `json:"result"`
}

func intPtr(i int) *int { return &i }
