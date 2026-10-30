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
	"io"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
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
		printEvents(os.Stdout, ch, *streaming)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("read input: %v", err)
	}
}

func printEvents(w io.Writer, ch <-chan *event.Event, streaming bool) {
	fmt.Fprint(w, "Assistant: ")
	var streamedText strings.Builder
	for evt := range ch {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Fprintf(w, "\nError: %s\n", evt.Error.Message)
			streamedText.Reset()
			continue
		}
		if len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		if streaming && evt.IsPartial {
			fmt.Fprint(w, choice.Delta.Content)
			streamedText.WriteString(choice.Delta.Content)
		} else if choice.Message.Role == model.RoleAssistant {
			if streaming && streamedText.Len() > 0 && streamedText.String() != choice.Message.Content {
				fmt.Fprintf(w, "\nAssistant (completed): %s", choice.Message.Content)
			} else if !streaming || streamedText.Len() == 0 {
				fmt.Fprint(w, choice.Message.Content)
			}
			streamedText.Reset()
		}
		if evt.IsFinalResponse() {
			fmt.Fprintln(w)
		}
	}
}
