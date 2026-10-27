//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates multi-turn chat through the OpenAI Responses
// adapter, LLMAgent, and an in-memory Runner session.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	modelName := flag.String("model", "gpt-5", "OpenAI model name")
	streaming := flag.Bool("streaming", true, "Enable streaming responses")
	flag.Parse()

	// WithStore(false) is the adapter default: each request sends the full
	// client-managed history. The SDK reads OPENAI_API_KEY and OPENAI_BASE_URL.
	chatModel := responses.New(*modelName, responses.WithStore(false))
	chatAgent := llmagent.New(
		"responses-chat",
		llmagent.WithModel(chatModel),
		llmagent.WithDescription("A concise chat assistant."),
		llmagent.WithInstruction("Answer the user's questions clearly and concisely."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: *streaming}),
	)
	// The in-memory session lives only until this process exits.
	sessions := sessioninmemory.NewSessionService()
	defer sessions.Close()
	r := runner.NewRunner(
		"responses-chat-example",
		chatAgent,
		runner.WithSessionService(sessions),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("chat-%d", time.Now().UnixNano())
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("OpenAI Responses chat. Type /exit to quit.")
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
		printEvents(ch, *streaming)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("read input: %v", err)
	}
}

func printEvents(ch <-chan *event.Event, streaming bool) {
	fmt.Print("Assistant: ")
	streamedText := false
	for evt := range ch {
		if evt.Error != nil {
			fmt.Printf("\nError: %s\n", evt.Error.Message)
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		if streaming && evt.IsPartial {
			fmt.Print(choice.Delta.Content)
			streamedText = streamedText || choice.Delta.Content != ""
		} else if choice.Message.Role == model.RoleAssistant {
			if !streaming || !streamedText {
				fmt.Print(choice.Message.Content)
			}
			streamedText = false
		}
		if evt.IsFinalResponse() {
			fmt.Println()
		}
	}
}
