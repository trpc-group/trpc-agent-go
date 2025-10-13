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

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/model/tiktoken"
)

var (
	flagModel                = flag.String("model", "deepseek-chat", "Model name, e.g., deepseek-chat or gpt-4o")
	flagEnableTokenTailoring = flag.Bool("enable-token-tailoring", true, "Enable automatic token tailoring based on model context window")
	flagMaxInputTokens       = flag.Int("max-input-tokens", 0, "Max input tokens for token tailoring (0 = auto-calculate from context window)")
	flagCounter              = flag.String("counter", "simple", "Token counter: simple|tiktoken")
	flagStrategy             = flag.String("strategy", "middle", "Tailoring strategy: middle|head|tail")
	flagStreaming            = flag.Bool("streaming", true, "Stream assistant responses")
)

// Interactive demo with /bulk to generate many messages and showcase
// counter/strategy usage without runner/session dependencies.
func main() {
	flag.Parse()

	// Build model with dual-mode token tailoring support.
	var opts []openai.Option
	opts = append(opts, openai.WithEnableTokenTailoring(*flagEnableTokenTailoring))

	if *flagMaxInputTokens > 0 {
		opts = append(opts, openai.WithMaxInputTokens(*flagMaxInputTokens))
	}

	if *flagCounter != "simple" {
		counter := buildCounter(strings.ToLower(*flagCounter), *flagModel, *flagMaxInputTokens)
		opts = append(opts, openai.WithTokenCounter(counter))
	}

	if *flagStrategy != "middle" {
		counter := buildCounter(strings.ToLower(*flagCounter), *flagModel, *flagMaxInputTokens)
		strategy := buildStrategy(counter, strings.ToLower(*flagStrategy))
		opts = append(opts, openai.WithTailoringStrategy(strategy))
	}

	modelInstance := openai.New(*flagModel, opts...)

	fmt.Printf("✂️  Token Tailoring Demo\n")
	fmt.Printf("🧩 model: %s\n", *flagModel)
	fmt.Printf("🔧 enable-token-tailoring: %t\n", *flagEnableTokenTailoring)
	if *flagMaxInputTokens > 0 {
		fmt.Printf("🔢 max-input-tokens: %d\n", *flagMaxInputTokens)
	} else {
		fmt.Printf("🔢 max-input-tokens: auto (from context window)\n")
	}
	fmt.Printf("🧮 counter: %s\n", strings.ToLower(*flagCounter))
	fmt.Printf("🎛️ strategy: %s\n", strings.ToLower(*flagStrategy))
	fmt.Printf("📡 streaming: %t\n", *flagStreaming)
	fmt.Println("==================================================")
	fmt.Println("💡 Commands:")
	fmt.Println("  /bulk N     - append N synthetic user messages")
	fmt.Println("  /history    - show current message count")
	fmt.Println("  /exit       - quit")
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

func buildStrategy(counter model.TokenCounter, strategyName string) model.TailoringStrategy {
	switch strategyName {
	case "head":
		return model.NewHeadOutStrategy(counter)
	case "tail":
		return model.NewTailOutStrategy(counter)
	default:
		return model.NewMiddleOutStrategy(counter)
	}
}

func buildCounter(name string, modelName string, maxTokens int) model.TokenCounter {
	switch name {
	case "tiktoken":
		if c, err := tiktoken.New(modelName, maxTokens); err == nil {
			return c
		} else {
			log.Warn("tiktoken counter init failed, falling back to simple", err)
		}
	}
	return model.NewSimpleTokenCounter(maxTokens)
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
		GenerationConfig: model.GenerationConfig{Stream: *flagStreaming},
	}

	ch, err := m.GenerateContent(ctx, req)
	if err != nil {
		log.Warn("generate content failed", err)
	}

	final := renderResponse(ch, *flagStreaming)
	if *flagMaxInputTokens > 0 {
		fmt.Printf("\n[tailor] maxInputTokens=%d before=%d after=%d\n",
			*flagMaxInputTokens, before, len(req.Messages))
	} else {
		fmt.Printf("\n[tailor] maxInputTokens=auto before=%d after=%d\n",
			before, len(req.Messages))
	}
	// Show a concise summary of the tailored messages (index, role, truncated content)
	fmt.Printf("[tailor] messages (after tailoring):\n%s", summarizeMessages(req.Messages, 12))
	if !*flagStreaming && final != "" {
		fmt.Printf("🤖 Assistant: %s\n\n", strings.TrimSpace(final))
		*messages = append(*messages, model.NewAssistantMessage(final))
	}
}

// renderResponse prints streaming or non-streaming responses similar to runner example.
func renderResponse(ch <-chan *model.Response, streaming bool) string {
	var final string
	printedPrefix := false
	for resp := range ch {
		if resp == nil {
			continue
		}
		if resp.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", resp.Error.Message)
			break
		}
		if streaming {
			if len(resp.Choices) > 0 {
				delta := resp.Choices[0].Delta.Content
				if delta != "" {
					if !printedPrefix {
						fmt.Print("🤖 Assistant: ")
						printedPrefix = true
					}
					fmt.Print(delta)
					final += delta
				}
			}
			if resp.Done {
				if printedPrefix {
					fmt.Println()
				}
				break
			}
			continue
		}
		// Non-streaming mode
		if resp.Done {
			if len(resp.Choices) > 0 {
				final = resp.Choices[0].Message.Content
			}
			break
		}
	}
	return final
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

// summarizeMessages returns a concise multi-line preview of messages with truncation.
func summarizeMessages(msgs []model.Message, maxItems int) string {
	var b strings.Builder
	for i, m := range msgs {
		if i >= maxItems {
			fmt.Fprintf(&b, "... (%d more)\n", len(msgs)-i)
			break
		}
		role := string(m.Role)
		content := firstNonEmpty(
			strings.TrimSpace(m.Content),
			strings.TrimSpace(m.ReasoningContent),
			firstTextPart(m),
		)
		content = truncate(content, 100)
		// replace newlines to keep one-line per message
		content = strings.ReplaceAll(content, "\n", " ")
		fmt.Fprintf(&b, "[%d] %s: %s\n", i, role, content)
	}
	return b.String()
}

func firstTextPart(m model.Message) string {
	for _, p := range m.ContentParts {
		if p.Text != nil {
			return strings.TrimSpace(*p.Text)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
