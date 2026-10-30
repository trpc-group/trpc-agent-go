//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a local function tool through the OpenAI Responses
// adapter, LLMAgent, and an in-memory Runner session.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	modelName := flag.String("model", "gpt-5", "OpenAI model name")
	streaming := flag.Bool("streaming", true, "Enable streaming responses")
	flag.Parse()

	// WithStore(false) is the adapter default: each request sends the full
	// client-managed history. The SDK reads OPENAI_API_KEY and OPENAI_BASE_URL.
	toolAgent := llmagent.New(
		"responses-tool-agent",
		llmagent.WithModel(responses.New(*modelName, responses.WithStore(false))),
		llmagent.WithDescription("An assistant that can use a local calculator."),
		llmagent.WithInstruction("Use the calculator for arithmetic. Each call performs one operation; use another call when the answer needs multiple operations."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: *streaming}),
		llmagent.WithTools([]tool.Tool{calculatorTool()}),
	)
	// The in-memory session lives only until this process exits.
	sessions := sessioninmemory.NewSessionService()
	defer sessions.Close()
	r := runner.NewRunner(
		"responses-tools-example",
		toolAgent,
		runner.WithSessionService(sessions),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("tools-%d", time.Now().UnixNano())
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("OpenAI Responses with a local calculator. Type /exit to quit.")
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "/exit" {
			return
		}
		if text == "" {
			continue
		}
		ch, err := r.Run(context.Background(), "demo-user", sessionID, model.NewUserMessage(text))
		if err != nil {
			log.Printf("run: %v", err)
			continue
		}
		printEvents(os.Stdout, ch, *streaming)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("read input: %v", err)
	}
}

func calculatorTool() tool.Tool {
	type arguments struct {
		Operation string  `json:"operation" jsonschema:"description=The operation to perform,enum=add,enum=multiply"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	type result struct {
		Value float64 `json:"value"`
	}
	return function.NewFunctionTool(
		func(_ context.Context, input arguments) (result, error) {
			switch strings.ToLower(input.Operation) {
			case "add", "+":
				return result{Value: input.A + input.B}, nil
			case "multiply", "*":
				return result{Value: input.A * input.B}, nil
			default:
				return result{}, fmt.Errorf("unsupported operation %q", input.Operation)
			}
		},
		function.WithName("calculator"),
		function.WithDescription("Perform one addition or multiplication operation."),
	)
}

// printEvents writes assistant text and committed local tool activity.
// Streaming assistant text is taken from deltas. The aggregated assistant
// message is written again with a completion label when it differs from the
// preview. Matching text is not repeated. Tool calls are shown only from a
// completed Message.ToolCalls list, and tool results only from the committed
// tool message. Partial tool calls, partial
// tool-result previews, reasoning text, and native Replay payloads are not
// displayed as completed tool activity.
func printEvents(w io.Writer, ch <-chan *event.Event, streaming bool) {
	assistantOpen := false
	var streamedText strings.Builder
	closeAssistant := func() {
		if !assistantOpen {
			return
		}
		fmt.Fprintln(w)
		assistantOpen = false
	}
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			closeAssistant()
			fmt.Fprintf(w, "Error: %s\n", evt.Error.Message)
			streamedText.Reset()
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		if streaming && evt.IsPartial {
			// A partial tool result carries its preview in Delta.Content.
			// The committed result is printed later from Message.
			if choice.Delta.Content != "" && !evt.IsToolResultResponse() {
				if !assistantOpen {
					fmt.Fprint(w, "Assistant: ")
					assistantOpen = true
				}
				fmt.Fprint(w, choice.Delta.Content)
				streamedText.WriteString(choice.Delta.Content)
			}
		} else if choice.Message.Role == model.RoleAssistant {
			if streaming && streamedText.Len() > 0 && streamedText.String() != choice.Message.Content {
				closeAssistant()
				fmt.Fprintf(w, "Assistant (completed): %s", choice.Message.Content)
				assistantOpen = true
			} else if (!streaming || streamedText.Len() == 0) && choice.Message.Content != "" {
				if !assistantOpen {
					fmt.Fprint(w, "Assistant: ")
					assistantOpen = true
				}
				fmt.Fprint(w, choice.Message.Content)
			}
			streamedText.Reset()
		}
		if hasCommittedToolOutput(evt) {
			closeAssistant()
			writeCommittedToolCalls(w, evt)
			writeCommittedToolResults(w, evt)
			streamedText.Reset()
		}
		if evt.IsFinalResponse() {
			closeAssistant()
		}
	}
}

func hasCommittedToolOutput(evt *event.Event) bool {
	if evt == nil || evt.Response == nil || evt.IsPartial {
		return false
	}
	for _, choice := range evt.Response.Choices {
		if len(choice.Message.ToolCalls) > 0 {
			return true
		}
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			return true
		}
	}
	return false
}

func writeCommittedToolCalls(w io.Writer, evt *event.Event) {
	if evt == nil || evt.Response == nil || evt.IsPartial {
		return
	}
	for _, choice := range evt.Response.Choices {
		for _, call := range choice.Message.ToolCalls {
			fmt.Fprintf(w, "Tool call: %s (ID: %s)\n", call.Function.Name, call.ID)
			if len(call.Function.Arguments) > 0 {
				fmt.Fprintf(w, "  Args: %s\n", string(call.Function.Arguments))
			}
		}
	}
}

func writeCommittedToolResults(w io.Writer, evt *event.Event) {
	if evt == nil || evt.Response == nil || evt.IsPartial {
		return
	}
	for _, choice := range evt.Response.Choices {
		msg := choice.Message
		if msg.Role != model.RoleTool || msg.ToolID == "" {
			continue
		}
		result := strings.TrimSpace(msg.Content)
		if msg.ToolName != "" {
			fmt.Fprintf(w, "Tool result: %s (ID: %s): %s\n", msg.ToolName, msg.ToolID, result)
			continue
		}
		fmt.Fprintf(w, "Tool result (ID: %s): %s\n", msg.ToolID, result)
	}
}
