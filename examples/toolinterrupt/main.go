//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates interrupting tool execution so an external
// caller can run the tool and send the tool result back to the agent.
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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String(
		"model",
		defaultModelName,
		"Name of the model to use",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming mode for responses",
	)
)

const (
	defaultModelName = "deepseek-chat"

	appName   = "tool-interrupt-demo"
	agentName = "tool-interrupt-agent"
	userID    = "demo-user"

	externalToolName = "external_search"
	externalToolDesc = "Search an external system for information."

	maxTokens   = 1500
	temperature = 0.2

	maxToolLoops = 8
)

const agentInstruction = `You are a helpful assistant.

For every user question:
1) Call external_search with {"query": "<the user question>"}.
2) Wait for the tool result.
3) Answer using ONLY the tool result content.

Do not answer before you receive the tool result.`

func main() {
	flag.Parse()

	fmt.Printf("🚀 Tool Interrupt Demo (External Tool Execution)\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println(strings.Repeat("=", 60))

	d := &toolInterruptDemo{
		modelName: *modelName,
		streaming: *streaming,
	}

	if err := d.run(); err != nil {
		log.Fatalf("demo failed: %v", err)
	}
}

type toolInterruptDemo struct {
	modelName string
	streaming bool

	runner    runner.Runner
	sessionID string

	execFilter tool.FilterFunc
}

func (d *toolInterruptDemo) run() error {
	ctx := context.Background()
	if err := d.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer d.runner.Close()

	printHelp()
	return d.startChat(ctx)
}

func (d *toolInterruptDemo) setup(_ context.Context) error {
	modelInstance := openai.New(d.modelName)
	sessionService := inmemory.NewSessionService()

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      d.streaming,
	}

	toolDef := function.NewFunctionTool(
		externalSearchTool,
		function.WithName(externalToolName),
		function.WithDescription(externalToolDesc),
	)

	ag := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(
			"Demonstrates manual tool execution (interrupt + resume).",
		),
		llmagent.WithInstruction(agentInstruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{toolDef}),
	)

	d.runner = runner.NewRunner(
		appName,
		ag,
		runner.WithSessionService(sessionService),
	)

	d.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	d.execFilter = tool.NewExcludeToolNamesFilter(externalToolName)

	fmt.Printf("✅ Demo ready! Session: %s\n\n", d.sessionID)
	return nil
}

func printHelp() {
	fmt.Println("💡 Commands:")
	fmt.Println("   /exit  - End the conversation")
	fmt.Println()
	fmt.Println("Try:")
	fmt.Println("   • What is trpc-agent-go?")
	fmt.Println("   • Summarize the latest release notes")
	fmt.Println()
}

func (d *toolInterruptDemo) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		if strings.ToLower(userInput) == "/exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if err := d.processTurn(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (d *toolInterruptDemo) processTurn(
	ctx context.Context,
	userInput string,
) error {
	next := model.NewUserMessage(userInput)
	for i := 0; i < maxToolLoops; i++ {
		toolCalls, err := d.runOnce(ctx, next)
		if err != nil {
			return err
		}
		if len(toolCalls) == 0 {
			return nil
		}

		if len(toolCalls) != 1 {
			fmt.Printf(
				"\n⚠️ Demo expects 1 tool call, got %d\n",
				len(toolCalls),
			)
		}

		next, err = d.executeExternally(toolCalls[0])
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("tool loop exceeded %d iterations", maxToolLoops)
}

func (d *toolInterruptDemo) runOnce(
	ctx context.Context,
	message model.Message,
) ([]model.ToolCall, error) {
	eventChan, err := d.runner.Run(
		ctx,
		userID,
		d.sessionID,
		message,
		agent.WithToolExecutionFilter(d.execFilter),
	)
	if err != nil {
		return nil, fmt.Errorf("runner.Run failed: %w", err)
	}
	return d.printAndCollect(eventChan)
}

func (d *toolInterruptDemo) printAndCollect(
	eventChan <-chan *event.Event,
) ([]model.ToolCall, error) {
	toolCallsByID := make(map[string]model.ToolCall)
	var (
		printedAssistant bool
		printedTools     bool
	)

	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}
		if len(ev.Choices) == 0 {
			continue
		}

		choice := ev.Choices[0]
		delta := choice.Delta

		if delta.Content != "" {
			if !printedAssistant {
				fmt.Print("🤖 Assistant: ")
				printedAssistant = true
			}
			fmt.Print(delta.Content)
		} else if choice.Message.Content != "" {
			if !printedAssistant {
				fmt.Print("🤖 Assistant: ")
				printedAssistant = true
			}
			fmt.Print(choice.Message.Content)
		}

		calls := append(
			choice.Message.ToolCalls,
			choice.Delta.ToolCalls...,
		)
		if len(calls) > 0 && !printedTools {
			fmt.Printf("\n🔧 Tool calls (not executed by agent):\n")
			printedTools = true
		}
		for _, tc := range calls {
			if tc.ID == "" {
				continue
			}
			toolCallsByID[tc.ID] = tc
			args := strings.TrimSpace(string(tc.Function.Arguments))
			if args == "" {
				args = "{}"
			}
			fmt.Printf(
				"   • %s (ID: %s) args=%s\n",
				tc.Function.Name,
				tc.ID,
				args,
			)
		}

		if ev.IsFinalResponse() {
			break
		}
	}

	if printedAssistant {
		fmt.Println()
	}

	calls := make([]model.ToolCall, 0, len(toolCallsByID))
	for _, tc := range toolCallsByID {
		calls = append(calls, tc)
	}
	return calls, nil
}

func (d *toolInterruptDemo) executeExternally(
	tc model.ToolCall,
) (model.Message, error) {
	if tc.Function.Name != externalToolName {
		return model.Message{}, fmt.Errorf(
			"unsupported tool: %s",
			tc.Function.Name,
		)
	}

	resultJSON, err := runExternalSearch(tc.Function.Arguments)
	if err != nil {
		return model.Message{}, err
	}

	fmt.Printf("\n--- External tool executed by caller ---\n")
	fmt.Printf("✅ Tool result (ID: %s): %s\n", tc.ID, resultJSON)

	return model.NewToolMessage(tc.ID, tc.Function.Name, resultJSON), nil
}

type externalSearchInput struct {
	Query string `json:"query"`
}

type externalSearchOutput struct {
	Query   string   `json:"query"`
	Results []string `json:"results"`
}

func externalSearchTool(
	_ context.Context,
	in externalSearchInput,
) (externalSearchOutput, error) {
	return externalSearchOutput{
		Query: in.Query,
		Results: []string{
			fmt.Sprintf("result for query: %q", in.Query),
			"demo-only: no real network call was made",
		},
	}, nil
}

func runExternalSearch(args []byte) (string, error) {
	in := externalSearchInput{}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("parse tool args: %w", err)
	}

	out, err := externalSearchTool(context.Background(), in)
	if err != nil {
		return "", fmt.Errorf("execute tool: %w", err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshal tool result: %w", err)
	}
	return string(b), nil
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
