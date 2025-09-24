//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates interactive token tailoring using the Runner with interactive command line interface.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

var (
	flagModel            = flag.String("model", "deepseek-chat", "Model name, e.g., deepseek-chat or gpt-4o")
	flagMaxPromptTokens  = flag.Int("max-prompt-tokens", 512, "Max prompt tokens budget for tailoring")
	flagStrategy         = flag.String("strategy", "middle", "Tailoring strategy: middle|head|tail")
	flagPreserveSystem   = flag.Bool("preserve-system", true, "Preserve the first system message when applicable")
	flagPreserveLastTurn = flag.Bool("preserve-last-turn", true, "Preserve the last turn (1~2 messages)")
)

// Interactive demo with /bulk to generate many messages and showcase
// counter/strategy usage without runner/session dependencies.
func main() {
	flag.Parse()

	counter := model.NewSimpleTokenCounter(*flagMaxPromptTokens)
	strategy := buildStrategy(counter, strings.ToLower(*flagStrategy), *flagPreserveSystem, *flagPreserveLastTurn)
	modelInstance := openai.New(*flagModel,
		openai.WithTokenTailoring(counter, strategy, *flagMaxPromptTokens),
	)

	fmt.Printf("Token Tailoring Demo\n")
	fmt.Printf("Model=%s Strategy=%s MaxPromptTokens=%d PreserveSystem=%t PreserveLastTurn=%t\n",
		*flagModel, strings.ToLower(*flagStrategy), *flagMaxPromptTokens, *flagPreserveSystem, *flagPreserveLastTurn)
	fmt.Println("Commands:")
	fmt.Println("  /bulk N         - append N synthetic user messages")
	fmt.Println("  /history        - show current message count")
	fmt.Println("  /exit           - quit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	messages := []model.Message{model.NewSystemMessage("You are a helpful assistant.")}
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		handled := handleCommand(&messages, line)
		if handled {
			if line == "/exit" {
				return
			}
			continue
		}
		processTurn(context.Background(), modelInstance, &messages, line)
	}
}

func buildStrategy(counter model.TokenCounter, strategyName string, preserveSystem, preserveLastTurn bool) model.TailoringStrategy {
	switch strategyName {
	case "head":
		return model.NewHeadOutStrategy(counter, preserveSystem, preserveLastTurn)
	case "tail":
		return model.NewTailOutStrategy(counter, preserveSystem, preserveLastTurn)
	default:
		return model.NewMiddleOutStrategy(counter)
	}
}

func handleCommand(messages *[]model.Message, line string) bool {
	switch {
	case strings.HasPrefix(line, "/exit"):
		fmt.Println("👋 Goodbye!")
		return true
	case strings.HasPrefix(line, "/history"):
		fmt.Printf("Messages in buffer: %d\n", len(*messages))
		return true
	case strings.HasPrefix(line, "/bulk"):
		parts := strings.Fields(line)
		n := 10
		if len(parts) >= 2 {
			if v, err := strconv.Atoi(parts[1]); err == nil && v > 0 {
				n = v
			}
		}
		for i := 0; i < n; i++ {
			*messages = append(*messages, model.NewUserMessage(long(fmt.Sprintf("synthetic %d", i+1))))
		}
		fmt.Printf("Added %d messages. Total=%d\n", n, len(*messages))
		return true
	default:
		return false
	}
}

func processTurn(ctx context.Context, m *openai.Model, messages *[]model.Message, userLine string) {
	*messages = append(*messages, model.NewUserMessage(userLine))
	before := len(*messages)

	req := &model.Request{
		Messages:         cloneMessages(*messages),
		GenerationConfig: model.GenerationConfig{Stream: false},
	}

	ch, err := m.GenerateContent(ctx, req)
	if err != nil {
		log.Warn("generate content failed", err)
	}

	final := waitResponse(ch)
	fmt.Printf("\n[tailor] maxPromptTokens=%d before=%d after=%d\n",
		*flagMaxPromptTokens, before, len(req.Messages))
	if final != "" {
		fmt.Printf("🤖 Assistant: %s\n\n", strings.TrimSpace(final))
		*messages = append(*messages, model.NewAssistantMessage(final))
	}
}

func waitResponse(ch <-chan *model.Response) string {
	var final string
	timeout := time.After(8 * time.Second)
	for {
		select {
		case resp, ok := <-ch:
			if !ok {
				return final
			}
			if resp == nil {
				continue
			}
			if resp.Error != nil {
				fmt.Printf("\n❌ Error: %s\n", resp.Error.Message)
				return final
			}
			if resp.Done {
				if len(resp.Choices) > 0 {
					final = resp.Choices[0].Message.Content
				}
				return final
			}
		case <-timeout:
			fmt.Println("\n⏱️  Timed out waiting for response. Proceeding.")
			return final
		}
	}
}

func cloneMessages(in []model.Message) []model.Message {
	out := make([]model.Message, len(in))
	copy(out, in)
	return out
}

func long(s string) string { return s + ": " + repeat("lorem ipsum ", 40) }

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
