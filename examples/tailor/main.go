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

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/ollama/ollama/api"
	openaisdk "github.com/openai/openai-go"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/model/tiktoken"
)

var (
	flagProvider             = flag.String("provider", "openai", "Name of the provider to use, openai/anthropic/ollama")
	flagModel                = flag.String("model", "", "Model name (auto-detected if empty)")
	flagEnableTokenTailoring = flag.Bool("enable-token-tailoring", true, "Enable automatic token tailoring based on model context window")
	flagMaxInputTokens       = flag.Int("max-input-tokens", 0, "Max input tokens for token tailoring (0 = auto-calculate from context window)")
	flagCounter              = flag.String("counter", "simple", "Token counter: simple|tiktoken")
	flagStrategy             = flag.String("strategy", "middle", "Tailoring strategy: middle|head|tail")
	flagStreaming            = flag.Bool("streaming", true, "Stream assistant responses")
	flagDebug                = flag.Bool("debug", false, "Enable debug logging")
)

// Interactive demo with /bulk to generate many messages and showcase
// counter/strategy usage without runner/session dependencies.
func main() {
	flag.Parse()
	if *flagDebug {
		log.SetLevel(log.LevelDebug)
	}

	// Auto-detect model name if not specified.
	provider := strings.ToLower(*flagProvider)
	modelName := *flagModel
	if modelName == "" {
		modelName = getDefaultModel(provider)
	}

	// Build model using provider package.
	modelInstance, err := buildModel(provider, modelName)
	if err != nil {
		log.Fatalf("Failed to build model: %v", err)
	}

	fmt.Printf("✂️  Token Tailoring Demo\n")
	fmt.Printf("🔌 provider: %s\n", provider)
	fmt.Printf("🧩 model: %s\n", modelName)
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
	fmt.Println("  /load FILE  - load messages from JSON file (e.g., /load input.json)")
	fmt.Println("  /history    - show current message count")
	fmt.Println("  /show       - display current messages (head + tail)")
	fmt.Println("  /exit       - quit")
	fmt.Println()

	counter := buildCounter(strings.ToLower(*flagCounter), modelName)
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
		processTurn(context.Background(), modelInstance, counter, &messages, line)
	}
}

func getDefaultModel(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-3-5-sonnet"
	case "ollama":
		return "llama3.2:latest"
	default:
		return "deepseek-chat"
	}
}

// buildModel creates a model instance using the provider package with unified configuration.
func buildModel(providerName, modelName string) (model.Model, error) {
	counter := buildCounter(strings.ToLower(*flagCounter), modelName)
	strategy := buildStrategy(counter, strings.ToLower(*flagStrategy))

	// Build common provider options.
	var opts []provider.Option
	opts = append(opts, provider.WithEnableTokenTailoring(*flagEnableTokenTailoring))
	if *flagMaxInputTokens > 0 {
		opts = append(opts, provider.WithMaxInputTokens(*flagMaxInputTokens))
	}
	opts = append(opts, provider.WithTokenCounter(counter))
	opts = append(opts, provider.WithTailoringStrategy(strategy))

	// Set provider-specific callbacks for token statistics display.
	switch providerName {
	case "openai":
		opts = append(opts, provider.WithCallbacks(provider.Callbacks{
			OpenAIChatRequest: func(ctx context.Context, req *openaisdk.ChatCompletionNewParams) {
				// Convert OpenAI messages back to model.Message for token counting.
				messages := convertFromOpenAIMessages(req.Messages)
				tokensAfter, _ := counter.CountTokensRange(ctx, messages, 0, len(messages))

				// Display tailoring statistics.
				if *flagMaxInputTokens > 0 {
					fmt.Printf("\n✂️  [Tailoring] maxInputTokens=%d 📨 messages=%d 🎯 tokens=%d\n",
						*flagMaxInputTokens, len(messages), tokensAfter)
				} else {
					fmt.Printf("\n✂️  [Tailoring] maxInputTokens=auto 📨 messages=%d 🎯 tokens=%d\n",
						len(messages), tokensAfter)
				}

				// Show head and tail messages to visualize what was kept/removed.
				fmt.Printf("📝 Messages (after tailoring, showing head + tail):\n%s",
					summarizeMessagesHeadTail(messages, 5, 5))
			},
		}))
	case "anthropic":
		opts = append(opts, provider.WithCallbacks(provider.Callbacks{
			AnthropicChatRequest: func(ctx context.Context, req *anthropicsdk.MessageNewParams) {
				// Convert Anthropic messages back to model.Message for token counting.
				messages := convertFromAnthropicMessages(req.Messages, req.System)
				tokensAfter, _ := counter.CountTokensRange(ctx, messages, 0, len(messages))

				// Display tailoring statistics.
				if *flagMaxInputTokens > 0 {
					fmt.Printf("\n✂️  [Tailoring] maxInputTokens=%d 📨 messages=%d 🎯 tokens=%d\n",
						*flagMaxInputTokens, len(messages), tokensAfter)
				} else {
					fmt.Printf("\n✂️  [Tailoring] maxInputTokens=auto 📨 messages=%d 🎯 tokens=%d\n",
						len(messages), tokensAfter)
				}

				// Show head and tail messages to visualize what was kept/removed.
				fmt.Printf("📝 Messages (after tailoring, showing head + tail):\n%s",
					summarizeMessagesHeadTail(messages, 5, 5))
			},
		}))
	case "ollama":
		opts = append(opts, provider.WithCallbacks(provider.Callbacks{
			OllamaChatRequest: func(ctx context.Context, req *api.ChatRequest) {
				// Convert Ollama messages back to model.Message for token counting.
				messages := convertFromOllamaMessages(req.Messages)
				tokensAfter, _ := counter.CountTokensRange(ctx, messages, 0, len(messages))

				// Display tailoring statistics.
				if *flagMaxInputTokens > 0 {
					fmt.Printf("\n✂️  [Tailoring] maxInputTokens=%d 📨 messages=%d 🎯 tokens=%d\n",
						*flagMaxInputTokens, len(messages), tokensAfter)
				} else {
					fmt.Printf("\n✂️  [Tailoring] maxInputTokens=auto 📨 messages=%d 🎯 tokens=%d\n",
						len(messages), tokensAfter)
				}

				// Show head and tail messages to visualize what was kept/removed.
				fmt.Printf("📝 Messages (after tailoring, showing head + tail):\n%s",
					summarizeMessagesHeadTail(messages, 5, 5))
			},
		}))
	default:
		return nil, fmt.Errorf("invalid provider: %s (must be 'openai' or 'anthropic')", providerName)
	}

	return provider.Model(providerName, modelName, opts...)
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

func buildCounter(name string, modelName string) model.TokenCounter {
	switch name {
	case "tiktoken":
		c, err := tiktoken.New(modelName)
		if err == nil {
			return c
		}
		log.Warn("tiktoken counter init failed, falling back to simple", err)
		fallthrough
	default:
		return model.NewSimpleTokenCounter()
	}
}

func handleCommand(messages *[]model.Message, line string) bool {
	switch {
	case strings.HasPrefix(line, "/exit"):
		fmt.Println("👋 Goodbye!")
		return true
	case strings.HasPrefix(line, "/history"):
		fmt.Printf("📊 Messages in buffer: %d\n", len(*messages))
		return true
	case strings.HasPrefix(line, "/show"):
		fmt.Printf("📋 Current messages (total: %d):\n", len(*messages))
		fmt.Print(summarizeMessagesHeadTail(*messages, 10, 10))
		return true
	case strings.HasPrefix(line, "/load"):
		parts := strings.Fields(line)
		if len(parts) < 2 {
			fmt.Println("❌ Usage: /load <filename>")
			return true
		}
		filename := parts[1]
		loaded, err := loadMessagesFromJSON(filename)
		if err != nil {
			fmt.Printf("❌ Failed to load %s: %v\n", filename, err)
			return true
		}
		// Replace messages with loaded ones (keep system message if first is not system).
		if len(loaded) > 0 && loaded[0].Role == model.RoleSystem {
			*messages = loaded
		} else {
			// Prepend system message if not present.
			*messages = append([]model.Message{model.NewSystemMessage("You are a helpful assistant.")}, loaded...)
		}
		fmt.Printf("✅ Loaded %d messages from %s. Total=%d\n", len(loaded), filename, len(*messages))
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
		fmt.Printf("✅ Added %d messages. Total=%d\n", n, len(*messages))
		return true
	default:
		return false
	}
}

func processTurn(ctx context.Context, m model.Model, counter model.TokenCounter, messages *[]model.Message, userLine string) {
	*messages = append(*messages, model.NewUserMessage(userLine))
	req := &model.Request{
		Messages:         *messages,
		GenerationConfig: model.GenerationConfig{Stream: *flagStreaming},
	}

	ch, err := m.GenerateContent(ctx, req)
	if err != nil {
		log.Warn("generate content failed", err)
		return
	}

	final := renderResponse(ch, *flagStreaming)

	if !*flagStreaming && final != "" {
		fmt.Printf("🤖 Assistant: %s\n\n", strings.TrimSpace(final))
	}
	if final != "" {
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
