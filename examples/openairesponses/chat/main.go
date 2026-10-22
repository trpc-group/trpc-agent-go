//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
	modelName := flag.String("model", envOr("OPENAI_MODEL", "gpt-5"), "OpenAI model name")
	apiKey := flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key")
	baseURL := flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI API base URL")
	streaming := flag.Bool("streaming", true, "Enable streaming responses")
	flag.Parse()
	if strings.TrimSpace(*apiKey) == "" {
		log.Fatal("set OPENAI_API_KEY or pass -api-key")
	}

	modelOptions := []responses.Option{
		responses.WithAPIKey(*apiKey),
		responses.WithStore(false),
	}
	if *baseURL != "" {
		modelOptions = append(modelOptions, responses.WithBaseURL(*baseURL))
	}
	chatModel := responses.New(*modelName, modelOptions...)
	chatAgent := llmagent.New(
		"responses-chat",
		llmagent.WithModel(chatModel),
		llmagent.WithDescription("A concise chat assistant."),
		llmagent.WithInstruction("Answer the user's questions clearly and concisely."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: *streaming}),
	)
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

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
