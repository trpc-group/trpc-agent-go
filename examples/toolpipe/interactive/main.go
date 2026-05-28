//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the toolpipe extension — a shell-like result
// filter for agent tools.
//
// In this example, the agent has a DuckDuckGo search tool augmented with
// a result_filter parameter. The model can naturally write:
//
//	duckduckgo_search({"query": "Go programming language", "result_filter": "grep -i concurrency | head 5"})
//
// The framework will:
//  1. Strip result_filter from args before calling the real tool
//  2. Execute duckduckgo_search with clean args
//  3. Apply "grep -i concurrency | head 5" to the result
//  4. Return only the filtered projection to the model
//
// This reduces context pollution when tool results are large.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	oai "github.com/openai/openai-go"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension/toolpipe"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch"
)

func main() {
	modelName := flag.String("model", "gpt-4o", "Name of the model to use")
	flag.Parse()

	fmt.Println("🚀 ToolPipe Demo — Shell-like Result Filtering")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()
	fmt.Println("This demo augments duckduckgo_search and web_fetch with a")
	fmt.Println("result_filter parameter. The model can write shell-like")
	fmt.Println("expressions to filter large tool results before they")
	fmt.Println("enter the context.")
	fmt.Println()
	fmt.Println("Supported filter ops: grep, head, tail, jq")
	fmt.Println("Examples:")
	fmt.Println("  grep -i keyword              — filter lines matching pattern")
	fmt.Println("  head -10                     — keep first 10 lines")
	fmt.Println("  jq -r '.results[0].content'  — extract JSON field as raw text")
	fmt.Println("  jq -r '...' | grep '^#'     — extract then filter")
	fmt.Println()
	fmt.Println("Type 'exit' to quit.")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	if err := run(*modelName); err != nil {
		log.Fatalf("Fatal: %v", err)
	}
}

func run(modelName string) error {
	ctx := context.Background()

	// Create model with debug callback to inspect streaming.
	modelInstance := openai.New(modelName,
		openai.WithChatStreamCompleteCallback(
			func(_ context.Context, req *oai.ChatCompletionNewParams, acc *oai.ChatCompletionAccumulator, streamErr error) {
				if streamErr != nil {
					fmt.Fprintf(os.Stderr, "\n[DEBUG] stream error: %v\n", streamErr)
					return
				}
				if acc == nil {
					fmt.Fprintf(os.Stderr, "\n[DEBUG] stream complete: accumulator is nil\n")
					return
				}
				choices := acc.Choices
				for i, c := range choices {
					role := c.Message.Role
					content := c.Message.Content
					nToolCalls := len(c.Message.ToolCalls)
					finishReason := c.FinishReason
					fmt.Fprintf(os.Stderr, "\n[DEBUG] choice[%d]: role=%s content_len=%d tool_calls=%d finish_reason=%s\n",
						i, role, len(content), nToolCalls, finishReason)
					if len(content) > 0 && len(content) < 200 {
						fmt.Fprintf(os.Stderr, "[DEBUG] content: %s\n", content)
					}
				}
				// Also dump request messages count.
				if req != nil {
					raw, _ := json.Marshal(req.Messages)
					fmt.Fprintf(os.Stderr, "[DEBUG] request messages size: %d bytes\n", len(raw))
				}
			},
		),
	)

	// Create DuckDuckGo search tool.
	searchTool := duckduckgo.NewTool()

	// Create web fetch tool — fetches URLs and converts HTML to markdown.
	fetchTool := httpfetch.NewTool(
		httpfetch.WithMaxContentLength(80000), // 80KB per URL
	)

	// Create the ToolPipe extension — augments both tools.
	pipe := toolpipe.New(
		toolpipe.WithToolNames("duckduckgo_search", "web_fetch"),
		toolpipe.WithAllowedOps(
			toolpipe.OpGrep,
			toolpipe.OpHead,
			toolpipe.OpTail,
			toolpipe.OpJQ,
		),
		toolpipe.WithMaxOutputBytes(32<<10), // 32KB max filtered output
	)

	// Create agent with the toolpipe extension.
	agent := llmagent.New(
		"search-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A research assistant with filtered search capabilities"),
		llmagent.WithInstruction("You are a helpful research assistant with web search and web fetch capabilities."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(16000),
			Stream:    true,
		}),
		llmagent.WithTools([]tool.Tool{searchTool, fetchTool}),
		llmagent.WithExtensions(pipe),
	)

	// Create runner.
	r := runner.NewRunner("toolpipe-demo", agent)
	defer r.Close()

	sessionID := fmt.Sprintf("session-%d", time.Now().Unix())

	// Interactive loop.
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.ToLower(input) == "exit" {
			fmt.Println("👋 Bye!")
			return nil
		}

		msg := model.NewUserMessage(input)
		eventChan, err := r.Run(ctx, "user", sessionID, msg)
		if err != nil {
			fmt.Printf("❌ Error: %v\n\n", err)
			continue
		}

		if err := printStream(eventChan); err != nil {
			fmt.Printf("❌ Stream error: %v\n\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func printStream(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	assistantStarted := false

	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}

		if len(ev.Response.Choices) > 0 {
			choice := ev.Response.Choices[0]

			// Show tool calls.
			if len(choice.Message.ToolCalls) > 0 {
				for _, tc := range choice.Message.ToolCalls {
					fmt.Printf("\n🔧 %s(%s)\n", tc.Function.Name, string(tc.Function.Arguments))
				}
				fmt.Print("⏳ Executing...")
				continue
			}

			// Show tool results (role=tool).
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				content := choice.Message.Content
				fmt.Printf("\n📋 Result: %s\n", content)
				continue
			}

			// Stream assistant text.
			if choice.Delta.Content != "" {
				if !assistantStarted {
					fmt.Print("\n🤖 Assistant: ")
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
			}
		}

		if ev.IsFinalResponse() {
			fmt.Println()
			break
		}
	}
	return nil
}

func intPtr(i int) *int { return &i }
