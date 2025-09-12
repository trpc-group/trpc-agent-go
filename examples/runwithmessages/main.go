//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates driving an Agent using a caller-provided
// []model.Message conversation history, without relying on server-side
// session content. It uses runner.RunWithMessages in an interactive loop.
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
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

func main() {
	flag.Parse()

	fmt.Printf("🚀 RunWithMessages Demo (stateless, caller-supplied history)\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println(strings.Repeat("=", 50))

	// Build an LLM agent with a simple instruction.
	genConfig := model.GenerationConfig{Stream: *streaming}
	agent := llmagent.New(
		"messages-agent",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithInstruction("You are a concise, helpful assistant."),
		llmagent.WithGenerationConfig(genConfig),
	)

	// Create Runner (defaults to in-memory session service).
	r := runner.NewRunner("runwithmessages-demo", agent)

	// Maintain local conversation history here, not in session.
	// Start with an optional system message.
	history := []model.Message{
		model.NewSystemMessage("You are a helpful AI assistant."),
	}

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
		low := strings.ToLower(input)
		switch low {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return
		case "/reset":
			// Reset conversation history and create a new session id for clarity.
			history = []model.Message{model.NewSystemMessage("You are a helpful AI assistant.")}
			prev := sessionID
			sessionID = fmt.Sprintf("runwithmessages-%d", time.Now().Unix())
			fmt.Printf("🆕 History cleared. New session: %s (was %s)\n", sessionID, prev)
			continue
		}

		// Append user message to local history only.
		history = append(history, model.NewUserMessage(input))

		// Run with the full history; the model sees exactly this context.
		ch, err := runner.RunWithMessages(context.Background(), r, userID, sessionID, history)
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
			// Final non-tool response marks end-of-turn.
			if e.Done && !isToolEvent(e) {
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

// isToolEvent returns true if an event is for tool calls/responses.
func isToolEvent(e *event.Event) bool { // reuse minimal checks from other examples
	if e == nil || e.Response == nil {
		// Check for outgoing tool calls in choices (streaming of toolcalls)
		if len(e.Choices) > 0 && len(e.Choices[0].Message.ToolCalls) > 0 {
			return true
		}
		if len(e.Choices) > 0 && e.Choices[0].Message.ToolID != "" {
			return true
		}
		return false
	}
	// Check tool role replies.
	if len(e.Response.Choices) > 0 {
		for _, c := range e.Response.Choices {
			if c.Message.Role == model.RoleTool {
				return true
			}
		}
	}
	// Also check streaming-side message.
	if len(e.Choices) > 0 && len(e.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(e.Choices) > 0 && e.Choices[0].Message.ToolID != "" {
		return true
	}
	return false
}
