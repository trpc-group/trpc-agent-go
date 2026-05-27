//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates context compaction with a real model call.
//
// It runs two turns in the same session. The first turn asks the model to call
// a tool that returns a large log. The second turn asks about the previous tool
// result. Use -debug to inspect the exact request projected for each
// model call and verify whether historical tool results were compacted.
//
// Usage:
//
//	# Run from the examples module so this package uses examples/go.mod.
//	cd examples
//
//	# The OpenAI-compatible provider reads credentials from environment
//	# variables. MODEL_NAME is optional when -model is passed explicitly.
//	export OPENAI_API_KEY="..."
//	export MODEL_NAME="gpt-5.2"
//
//	# Debug output is enabled by default. It prints the request after session
//	# history projection and context compaction, immediately before the model
//	# adapter receives it.
//	go run ./context_compaction -model=gpt-5.2
//
//	# Try -skip-recent-events=3 to protect the previous tool chain from Pass 1,
//	# or -force-clean-large-log to clean historical demo tool results by name.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName   = "context-compaction-demo"
	agentName = "context-compaction-agent"
	userID    = "demo-user"
)

var (
	modelName = flag.String(
		"model",
		os.Getenv("MODEL_NAME"),
		"Name of the OpenAI-compatible model to use (default: MODEL_NAME env var)",
	)
	streaming = flag.Bool(
		"streaming",
		false,
		"Enable streaming model responses",
	)
	debug = flag.Bool(
		"debug",
		true,
		"Print the projected request before every model call",
	)
	previewBytes = flag.Int(
		"preview-bytes",
		240,
		"Maximum content bytes to print for each request message",
	)
	logLines = flag.Int(
		"log-lines",
		240,
		"Number of synthetic log lines returned by the large_log tool",
	)
	toolResultMaxTokens = flag.Int(
		"tool-result-max-tokens",
		80,
		"Pass 1 token threshold for replacing historical tool results",
	)
	keepRecentRequests = flag.Int(
		"keep-recent-requests",
		0,
		"Number of latest completed requests protected from Pass 1",
	)
	skipRecentEvents = flag.Int(
		"skip-recent-events",
		0,
		"Number of tail events treated as recent by Pass 1",
	)
	oversizedToolResultMaxTokens = flag.Int(
		"oversized-tool-result-max-tokens",
		0,
		"Pass 2 token threshold for head+tail truncation; 0 disables Pass 2",
	)
	forceCleanLargeLog = flag.Bool(
		"force-clean-large-log",
		false,
		"Force historical large_log tool results to be cleaned when context compaction is enabled",
	)
)

type logRequest struct {
	Lines int `json:"lines"`
}

type logResult struct {
	Summary string `json:"summary"`
	Log     string `json:"log"`
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	flag.Parse()
	if strings.TrimSpace(*modelName) == "" {
		return fmt.Errorf("model is required; pass -model or set MODEL_NAME")
	}

	demo := newDemo()
	defer demo.runner.Close()

	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Debug: %t\n", *debug)
	fmt.Printf("Pass 1 max tokens: %d\n", *toolResultMaxTokens)
	fmt.Printf("Keep recent requests: %d\n", *keepRecentRequests)
	fmt.Printf("Skip recent events: %d\n", *skipRecentEvents)
	fmt.Printf("Pass 2 max tokens: %d\n", *oversizedToolResultMaxTokens)
	fmt.Printf("Force clean large_log: %t\n", *forceCleanLargeLog)
	fmt.Println(strings.Repeat("=", 80))

	firstPrompt := fmt.Sprintf(
		"Call the large_log tool with lines=%d, then summarize the first and last log lines.",
		*logLines,
	)
	if err := demo.runTurn(ctx, firstPrompt); err != nil {
		return err
	}

	secondPrompt := "What did the previous large_log tool return? Answer from the session history."
	if err := demo.runTurn(ctx, secondPrompt); err != nil {
		return err
	}
	return nil
}

type demoApp struct {
	runner    runner.Runner
	sessionID string
}

func newDemo() *demoApp {
	modelCallbacks := model.NewCallbacks()
	if *debug {
		// The callback sees the final request produced by the agent flow, after
		// history projection and context compaction. This is the most direct way
		// to verify what will be sent to the provider.
		modelCallbacks.RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			printProjectedRequest(args.Request)
			return &model.BeforeModelResult{Context: ctx}, nil
		})
	}

	modelInstance := openai.New(*modelName)
	genConfig := model.GenerationConfig{
		Stream: *streaming,
	}
	tools := []tool.Tool{
		function.NewFunctionTool(
			makeLargeLog,
			function.WithName("large_log"),
			function.WithDescription(
				"Return a large synthetic diagnostic log. Use this when the user asks to collect or inspect large logs.",
			),
		),
	}

	forceCleanToolNames := []string(nil)
	if *forceCleanLargeLog {
		forceCleanToolNames = []string{"large_log"}
	}

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Demonstrates prompt-side context compaction for large tool results."),
		llmagent.WithInstruction(
			"When the user asks to collect logs, call the large_log tool once before answering. "+
				"For follow-up questions, answer from the conversation history unless the user explicitly asks to collect new logs.",
		),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithModelCallbacks(modelCallbacks),
		// Context compaction is a prompt-projection feature. It rewrites tool
		// result content in the request sent to the model, while the full session
		// event remains stored in the session service.
		llmagent.WithEnableContextCompaction(true),
		// Pass 1 protects the current request and this many latest completed
		// requests. This demo defaults to 0 so the second turn clearly shows the
		// previous tool result becoming historical.
		llmagent.WithContextCompactionKeepRecentRequests(*keepRecentRequests),
		// Pass 1 threshold. Historical tool results above this estimated token
		// count are replaced with a compact placeholder.
		llmagent.WithContextCompactionToolResultMaxTokens(*toolResultMaxTokens),
		// Pass 2 threshold. When positive, any oversized tool result, including
		// one in the current request, is head+tail truncated.
		llmagent.WithContextCompactionOversizedToolResultMaxTokens(*oversizedToolResultMaxTokens),
		llmagent.WithToolResultCompactionConfig(&llmagent.ToolResultCompactionConfig{
			// ForceCleanToolNames is useful for noisy tools whose raw output is
			// rarely useful after the tool loop completes, such as shell/log
			// dumping tools. This demo exposes it through -force-clean-large-log.
			ForceCleanToolNames: forceCleanToolNames,
			// KeepToolNames has higher priority than ForceCleanToolNames and
			// skips context compaction for tools whose exact payload should stay
			// visible to the model.
			KeepToolNames: []string{
				"session_load",
				"session_search",
			},
			// SkipRecentFunc customizes what counts as "recent" for Pass 1. It
			// does not disable Pass 2; an oversized recent tool result can still
			// be head+tail truncated when Pass 2 is enabled.
			SkipRecentFunc: skipRecentFunc,
		}),
	)

	return &demoApp{
		runner: runner.NewRunner(
			appName,
			llmAgent,
			runner.WithSessionService(sessioninmemory.NewSessionService()),
		),
		sessionID: fmt.Sprintf("context-compaction-%d", time.Now().Unix()),
	}
}

func skipRecentFunc(events []event.Event) int {
	if *skipRecentEvents <= 0 {
		return 0
	}
	if *skipRecentEvents > len(events) {
		return len(events)
	}
	return *skipRecentEvents
}

func makeLargeLog(_ context.Context, req logRequest) (logResult, error) {
	lines := req.Lines
	if lines <= 0 {
		lines = *logLines
	}
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(
			&b,
			"line %04d service=payment shard=%02d level=INFO request_id=req-%04d message=\"synthetic diagnostic payload for context compaction\"\n",
			i,
			i%16,
			i,
		)
	}
	return logResult{
		Summary: fmt.Sprintf("generated %d synthetic log lines", lines),
		Log:     b.String(),
	}, nil
}

func (d *demoApp) runTurn(ctx context.Context, text string) error {
	fmt.Printf("\nUser: %s\n", text)
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	events, err := d.runner.Run(
		turnCtx,
		userID,
		d.sessionID,
		model.NewUserMessage(text),
		agent.WithRequestID(uuid.NewString()),
	)
	if err != nil {
		return err
	}
	for evt := range events {
		printEvent(evt)
		if err := eventError(evt); err != nil {
			cancel()
			drainEvents(events)
			return err
		}
	}
	return nil
}

func drainEvents(events <-chan *event.Event) {
	for range events {
	}
}

func eventError(evt *event.Event) error {
	if evt == nil || evt.Response == nil || evt.Response.Error == nil {
		return nil
	}
	return fmt.Errorf("response error: %w", evt.Response.Error)
}

func printEvent(evt *event.Event) {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		if err := eventError(evt); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		return
	}
	msg := evt.Response.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			fmt.Printf("Assistant requested tool: %s args=%s\n", tc.Function.Name, string(tc.Function.Arguments))
		}
		return
	}
	if msg.Role == model.RoleTool {
		fmt.Printf("Tool result: tool=%s bytes=%d\n", msg.ToolName, len(msg.Content))
		return
	}
	if evt.IsFinalResponse() && strings.TrimSpace(msg.Content) != "" {
		fmt.Printf("Assistant: %s\n", strings.TrimSpace(msg.Content))
	}
}

func printProjectedRequest(req *model.Request) {
	if req == nil {
		fmt.Println("\n--- Model Request: <nil> ---")
		return
	}
	fmt.Println("\n--- Model Request ---")
	fmt.Printf("messages=%d tools=%s\n", len(req.Messages), formatToolNames(req.Tools))
	for i, msg := range req.Messages {
		fmt.Printf(
			"[%02d] role=%s tool_name=%q tool_id=%q content_bytes=%d",
			i,
			msg.Role,
			msg.ToolName,
			msg.ToolID,
			len(msg.Content),
		)
		if len(msg.ToolCalls) > 0 {
			names := make([]string, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				names = append(names, tc.Function.Name)
			}
			fmt.Printf(" tool_calls=%v", names)
		}
		if msg.Content != "" {
			fmt.Printf(" preview=%q", preview(msg.Content, *previewBytes))
		}
		fmt.Println()
	}
	fmt.Println("--- End Model Request ---")
}

func formatToolNames(tools map[string]tool.Tool) string {
	if len(tools) == 0 {
		return "[]"
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Sprintf("%v", names)
}

func preview(content string, maxBytes int) string {
	content = strings.ReplaceAll(content, "\n", "\\n")
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	return content[:maxBytes] + "...(truncated preview)"
}
