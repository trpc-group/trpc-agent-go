//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates structured output with LLMAgent using a minimal
// interactive runner-style CLI.
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

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

func main() {
	flag.Parse()

	fmt.Printf("🚀 Structured Output (JSON Schema)\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Println(strings.Repeat("=", 50))

	if err := run(); err != nil {
		log.Fatalf("run failed: %v", err)
	}
}

// placeRecommendation represents a simple structured output for real usage.
type placeRecommendation struct {
	Name       string  `json:"name"`
	Address    string  `json:"address"`
	City       string  `json:"city"`
	Category   string  `json:"category"`
	Rating     float64 `json:"rating"`
	PriceLevel string  `json:"price_level"`
	Notes      string  `json:"notes"`
}

func run() error {
	ctx := context.Background()

	// OpenAI-compatible model.
	modelInstance := openai.New(*modelName)

	// Minimal generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(800),
		Temperature: floatPtr(0.3),
		Stream:      *streaming,
	}

	// Build agent with structured output using a typed struct; schema auto-generated.
	agentName := "recommender"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Recommend places with structured output."),
		llmagent.WithInstruction("When asked for a place, return exactly one recommendation."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithStructuredOutputJSON(new(placeRecommendation), true, "A single place recommendation."),
	)

	// Runner with in-memory session service.
	r := runner.NewRunner(
		"structured-output-demo",
		llmAgent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	userID := "user"
	sessionID := fmt.Sprintf("so-session-%d", time.Now().Unix())
	fmt.Printf("✅ Ready! Session: %s\n\n", sessionID)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if strings.EqualFold(text, "exit") {
			fmt.Println("👋 Bye!")
			return nil
		}

		msg := model.NewUserMessage(text)
		evCh, err := r.Run(ctx, userID, sessionID, msg)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			continue
		}

		// Minimal event loop: print content as it arrives; show typed payload when available.
		var latestJSON string
		for ev := range evCh {
			if ev.Error != nil {
				fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
				break
			}

			// If we got a typed structured output payload, display it succinctly.
			if ev.StructuredOutput != nil {
				if pr, ok := ev.StructuredOutput.(*placeRecommendation); ok {
					b, _ := json.MarshalIndent(pr, "", "  ")
					fmt.Printf("\n✅ Typed structured output received:\n%s\n", string(b))
				}
			}

			// Print content as normal.
			if len(ev.Choices) > 0 {
				if *streaming {
					delta := ev.Choices[0].Delta.Content
					if delta != "" {
						fmt.Print(delta)
					}
				} else {
					content := ev.Choices[0].Message.Content
					if content != "" {
						fmt.Println(content)
						latestJSON = content
					}
				}
			}

			if ev.Object == model.ObjectTypeRunnerCompletion {
				fmt.Println()
				break
			}
		}

		// Show raw JSON if not streamed (depends on provider behavior).
		if latestJSON != "" {
			fmt.Printf("\n🔎 Raw JSON (last message):\n%s\n\n", latestJSON)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
