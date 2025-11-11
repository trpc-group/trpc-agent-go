//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates driving an Agent using a caller-provided
// []model.Message conversation history, without the caller needing to manually
// seed the server-side session.
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
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

// defaultSeedHistory returns a pre-constructed multi-turn conversation between
// user and assistant. We only pass this full history on the first turn of a
// session (and after reset). After that, we just send the latest user input.
func defaultSeedHistory() []model.Message {
	return []model.Message{
		model.NewSystemMessage("You are a helpful math assistant."),
		model.NewUserMessage("Hi, can you help with calculations?"),
		model.NewAssistantMessage("Sure. I can add, subtract, multiply, divide, and compute power. When needed, I will call the calculate tool."),
	}
}

func main() {
	flag.Parse()

	fmt.Printf("🚀 RunWithMessages Demo (auto-seed & reuse session)\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println(strings.Repeat("=", 50))

	// Build an LLM agent with a simple instruction.
	genConfig := model.GenerationConfig{Stream: *streaming}

	var tools []tool.Tool
	tools = append(tools, function.NewFunctionTool(
		calcFn,
		function.WithName("calculate"),
		function.WithDescription("Perform basic arithmetic: add, subtract, multiply, divide, power"),
	))

	agent := llmagent.New(
		"messages-agent",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithInstruction("You are a concise, helpful assistant. When users ask to compute or do math (add/subtract/multiply/divide/power), call the calculate tool with proper arguments."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
	)

	r := runner.NewRunner("runwithmessages-demo", agent)

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	// Maintain local conversation history. Only on the first user turn we pass
	// the full multi-turn history; on subsequent turns we pass only the latest
	// user message (Runner writes to Session; ContentProcessor reads Session).
	history := defaultSeedHistory()
	seeded := false

	userID := "user"
	sessionID := fmt.Sprintf("runwithmessages-%d", time.Now().Unix())

	// Interactive loop.
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("💡 Type '/reset' to clear history, '/exit' to quit.")
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		switch strings.ToLower(input) {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return
		case "/reset":
			// Reset local history and rotate a new session for clarity.
			history = defaultSeedHistory()
			seeded = false
			prev := sessionID
			sessionID = fmt.Sprintf("runwithmessages-%d", time.Now().Unix())
			fmt.Printf("🆕 History cleared. New session: %s (was %s)\n", sessionID, prev)
			continue
		}

		userMsg := model.NewUserMessage(input)
		// First turn for this session: pass full seed history + latest user input.
		// Later turns: only pass the latest user input.
		var ch <-chan *event.Event
		var err error
		if !seeded {
			seedHistory := append(append([]model.Message{}, history...), userMsg)
			history = append(history, userMsg)
			ch, err = runner.RunWithMessages(context.Background(), r, userID, sessionID, seedHistory)
			if err == nil {
				seeded = true
			}
		} else {
			history = append(history, userMsg)
			ch, err = r.Run(context.Background(), userID, sessionID, userMsg)
		}
		if err != nil {
			fmt.Printf("❌ failed to run: %v\n", err)
			continue
		}

		// Stream/collect assistant output.
		fmt.Print("🤖 Assistant: ")
		var full string
		for e := range ch {
			if e.Error != nil {
				fmt.Printf("\n❌ Error: %s\n", e.Error.Message)
				continue
			}
			// Stream tokens (delta) or print whole message in non-streaming.
			if len(e.Choices) > 0 {
				// Print tool call intents and tool results when present to
				// demonstrate tool-call path clearly.
				if len(e.Choices[0].Message.ToolCalls) > 0 {
					for _, tc := range e.Choices[0].Message.ToolCalls {
						fmt.Printf("\n🔧 Tool call → %s", tc.Function.Name)
						if len(tc.Function.Arguments) > 0 {
							fmt.Printf(" args=%s", string(tc.Function.Arguments))
						}
						fmt.Println()
					}
				}
				if e.Choices[0].Message.ToolID != "" {
					// Tool result in streaming delta or message
					if s := e.Choices[0].Message.Content; strings.TrimSpace(s) != "" {
						fmt.Printf("\n📦 Tool result (%s): %s\n", e.Choices[0].Message.ToolID, s)
					} else {
						fmt.Printf("\n📦 Tool result (%s)\n", e.Choices[0].Message.ToolID)
					}
				}
				if *streaming {
					s := e.Choices[0].Delta.Content
					full += s
					fmt.Print(s)
				} else {
					s := e.Choices[0].Message.Content
					full = s
					fmt.Print(s)
				}
			}
			if e.IsFinalResponse() {
				fmt.Println()
				break
			}
		}

		// Append the assistant reply to local history for the next turn.
		if strings.TrimSpace(full) != "" {
			history = append(history, model.NewAssistantMessage(full))
		}
	}
}

// calcInput is the input of the calculator function.
type calcInput struct {
	Operation string  `json:"operation"` // add, subtract, multiply, divide, power
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

// calcOutput is the output of the calculator function.
type calcOutput struct {
	Result float64 `json:"result"`
	Error  string  `json:"error,omitempty"`
}

// calcFn is the calculator function.
func calcFn(ctx context.Context, in calcInput) (calcOutput, error) {
	switch strings.ToLower(strings.TrimSpace(in.Operation)) {
	case "add":
		return calcOutput{Result: in.A + in.B}, nil
	case "subtract":
		return calcOutput{Result: in.A - in.B}, nil
	case "multiply":
		return calcOutput{Result: in.A * in.B}, nil
	case "divide":
		if in.B == 0 {
			return calcOutput{Error: "division by zero"}, nil
		}
		return calcOutput{Result: in.A / in.B}, nil
	case "power":
		res := 1.0
		for i := 0; i < int(in.B); i++ {
			res *= in.A
		}
		return calcOutput{Result: res}, nil
	default:
		return calcOutput{Error: "unknown operation"}, nil
	}
}
