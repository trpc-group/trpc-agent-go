//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the context continuity fix in trpc-agent-go.
// This example shows how Sequential agents can properly see outputs from Parallel sub-agents
// after the context filtering improvements. The architecture is:
// Sequential(Parallel(Sequential(Parallel(NumberAnalyst,CultureAnalyst), ColorAnalyst), Evaluator), Summarizer)
//
// The demo runs two fixed rounds of conversation to verify:
// 1. ColorAnalyst can see NumberAnalyst & CultureAnalyst outputs from the parallel execution
// 2. Summarizer can see all agent outputs with proper context aggregation
// 3. All agents in Round 2 remember and reference Round 1 results
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	openaiapi "github.com/openai/openai-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	maxTokens   = 400
	temperature = 0.7
)

func main() {
	modelName := "deepseek-chat"

	fmt.Printf("🔗 Context Continuity Fix Demo (Non-interactive)\n")
	fmt.Printf("Model: %s\n", modelName)
	fmt.Printf("Architecture: Sequential(Parallel(Sequential(Parallel(NumberAnalyst,CultureAnalyst), ColorAnalyst), Evaluator), Summarizer)\n")
	fmt.Printf("Purpose: Verify Sequential agents can see Parallel sub-agents' outputs\n")
	fmt.Println(strings.Repeat("=", 70))

	demo := &contextDemo{
		modelName: modelName,
	}

	if err := demo.run(); err != nil {
		log.Fatalf("Demo failed: %v", err)
	}
}

// contextDemo represents the demo configuration for testing context continuity.
type contextDemo struct {
	modelName string        // LLM model name to use
	runner    runner.Runner // Agent runner instance
	userID    string        // User identifier for the session
	sessionID string        // Session identifier for conversation tracking
}

func (d *contextDemo) run() error {
	ctx := context.Background()

	if err := d.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	return d.runFixedConversation(ctx)
}

func (d *contextDemo) setup(_ context.Context) error {
	// Create model with callback to observe all requests
	modelInstance := openai.New(d.modelName,
		openai.WithChatRequestCallback(
			func(ctx context.Context, chatRequest *openaiapi.ChatCompletionNewParams) {
				// Get current agent info
				agentName := "unknown"
				if invocation, ok := agent.InvocationFromContext(ctx); ok {
					agentName = invocation.AgentName
				}

				// Get branch info
				branchInfo := "unknown"
				if invocation, ok := agent.InvocationFromContext(ctx); ok {
					branchInfo = invocation.Branch
				}

				fmt.Printf("\n📋 [%s] Branch: %s\n", agentName, branchInfo)
				fmt.Printf("   Message count: %d\n", len(chatRequest.Messages))

				// Analyze context status
				contextStatus := ""
				if len(chatRequest.Messages) == 2 {
					contextStatus = "📝 Current message only"
				} else if len(chatRequest.Messages) <= 4 {
					contextStatus = "🟡 Partial context"
				} else {
					contextStatus = "✅ Rich context"
				}
				fmt.Printf("   %s (%d messages)\n", contextStatus, len(chatRequest.Messages))

				// Show message content (like main.go)
				if messagesJSON, err := json.MarshalIndent(chatRequest.Messages, "   ", "  "); err == nil {
					fmt.Printf("   📝 Message content:\n%s\n", string(messagesJSON))
				}
				fmt.Println("──────────────────────────────────────────")
			},
		),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      false, // Use non-streaming for complete response observation
	}

	// Build complex nested agent structure to test context continuity:
	// Sequential(Parallel(Sequential(Parallel(NumberAnalyst,CultureAnalyst), ColorAnalyst), Evaluator), Summarizer)

	// Innermost Parallel(NumberAnalyst, CultureAnalyst)
	numberAnalyst := llmagent.New(
		"NumberAnalyst",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Analyzes mathematical meaning of numbers"),
		llmagent.WithInstruction("You are NumberAnalyst. Your task: analyze mathematical meaning of numbers. Important: when relevant, always reference what other agents said. Clearly state what you learned from previous agents. Keep responses concise but mention specific details from previous context."),
		llmagent.WithGenerationConfig(genConfig),
	)

	cultureAnalyst := llmagent.New(
		"CultureAnalyst",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Analyzes cultural meaning of numbers"),
		llmagent.WithInstruction("You are CultureAnalyst. Your task: analyze cultural meaning of numbers. Important: when relevant, always reference what other agents said. Clearly state what you learned from previous agents. Keep responses concise but mention specific details from previous context."),
		llmagent.WithGenerationConfig(genConfig),
	)

	innerParallel := parallelagent.New(
		"InnerParallel",
		parallelagent.WithSubAgents([]agent.Agent{numberAnalyst, cultureAnalyst}),
	)

	// ColorAnalyst agent
	colorAnalyst := llmagent.New(
		"ColorAnalyst",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Analyzes color meanings"),
		llmagent.WithInstruction("You are ColorAnalyst. Your task: analyze color meanings. Important: when relevant, always reference what other agents said. Clearly state what you learned from previous agents. Keep responses concise but mention specific details from previous context."),
		llmagent.WithGenerationConfig(genConfig),
	)

	// Sequential(Parallel(NumberAnalyst,CultureAnalyst), ColorAnalyst)
	innerSequential := chainagent.New(
		"InnerSequential",
		chainagent.WithSubAgents([]agent.Agent{innerParallel, colorAnalyst}),
	)

	// Evaluator agent
	evaluator := llmagent.New(
		"Evaluator",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Performs comprehensive evaluation"),
		llmagent.WithInstruction("You are Evaluator. Your task: perform comprehensive evaluation. Important: when relevant, always reference what other agents said. Clearly state what you learned from previous agents. Keep responses concise but mention specific details from previous context."),
		llmagent.WithGenerationConfig(genConfig),
	)

	// Parallel(Sequential(Parallel(NumberAnalyst,CultureAnalyst), ColorAnalyst), Evaluator)
	middleParallel := parallelagent.New(
		"MiddleParallel",
		parallelagent.WithSubAgents([]agent.Agent{innerSequential, evaluator}),
	)

	// Summarizer agent
	summarizer := llmagent.New(
		"Summarizer",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Summarizes all analysis results"),
		llmagent.WithInstruction("You are Summarizer. Your task: summarize all analysis results. Important: when relevant, always reference what other agents said. Clearly state what you learned from previous agents. Keep responses concise but mention specific details from previous context."),
		llmagent.WithGenerationConfig(genConfig),
	)

	// Outermost Sequential(Parallel(Sequential(Parallel(NumberAnalyst,CultureAnalyst), ColorAnalyst), Evaluator), Summarizer)
	chainAgent := chainagent.New(
		"ComplexNesting",
		chainagent.WithSubAgents([]agent.Agent{middleParallel, summarizer}),
	)

	// Create runner
	appName := "context-continuity-demo"
	d.runner = runner.NewRunner(appName, chainAgent)

	d.userID = "demo-user"
	d.sessionID = fmt.Sprintf("demo-session-%d", time.Now().Unix())

	fmt.Printf("✅ Agents ready! Session: %s\n", d.sessionID)
	fmt.Printf("📝 Flow: Sequential(Parallel(Sequential(Parallel(NumberAnalyst,CultureAnalyst), ColorAnalyst), Evaluator), Summarizer)\n\n")

	return nil
}

func (d *contextDemo) runFixedConversation(ctx context.Context) error {
	// First round
	fmt.Printf("🚀 Round 1: Analyze number 8 and color red\n")
	fmt.Printf("📍 Key observation: Can ColorAnalyst see NumberAnalyst & CultureAnalyst outputs? Can Summarizer see all outputs?\n")
	fmt.Println(strings.Repeat("=", 70))

	message1 := model.NewUserMessage("Analyze the meaning of number 8 and color red")

	if err := d.processMessage(ctx, message1, "Round 1"); err != nil {
		return fmt.Errorf("round 1 failed: %w", err)
	}

	fmt.Printf("\n" + strings.Repeat("=", 70) + "\n")

	// Second round
	fmt.Printf("🚀 Round 2: Analyze number 7 and color blue\n")
	fmt.Printf("📍 Key observation: Do agents remember Round 1 content? Do message counts increase?\n")
	fmt.Println(strings.Repeat("=", 70))

	message2 := model.NewUserMessage("Now analyze number 7 and color blue, comparing with Round 1 analysis results")

	if err := d.processMessage(ctx, message2, "Round 2"); err != nil {
		return fmt.Errorf("round 2 failed: %w", err)
	}

	fmt.Printf("\n🎯 Context continuity fix verification completed!\n")
	fmt.Printf("📊 Expected verification results:\n")
	fmt.Printf("  • ColorAnalyst: Round 1 has 4 messages, Round 2 has 9 messages (includes NumberAnalyst & CultureAnalyst outputs)\n")
	fmt.Printf("  • Summarizer: Round 1 has 6 messages, Round 2 has 12 messages (includes all agent outputs)\n")
	fmt.Printf("  • All agents in Round 2 explicitly reference Round 1 analysis results\n")

	return nil
}

func (d *contextDemo) processMessage(ctx context.Context, message model.Message, roundName string) error {
	fmt.Printf("🔄 Starting %s processing...\n", roundName)
	fmt.Println(strings.Repeat("─", 50))

	eventChan, err := d.runner.Run(ctx, d.userID, d.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agents: %w", err)
	}

	return d.processStreamingResponse(eventChan)
}

func (d *contextDemo) processStreamingResponse(eventChan <-chan *event.Event) error {
	agentIcons := map[string]string{
		"ComplexNesting":  "🔗",
		"MiddleParallel":  "⚡",
		"InnerSequential": "🔄",
		"InnerParallel":   "⚖️",
		"NumberAnalyst":   "🔢",
		"CultureAnalyst":  "🎨",
		"ColorAnalyst":    "🌈",
		"Evaluator":       "📊",
		"Summarizer":      "📝",
	}

	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("\n❌ Error from %s: %s\n", event.Author, event.Error.Message)
			continue
		}

		agentIcon := agentIcons[event.Author]
		if agentIcon == "" {
			agentIcon = "🤖"
		}

		// Process response content
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			if choice.Message.Content != "" {
				fmt.Printf("%s [%s]: %s\n\n", agentIcon, event.Author, choice.Message.Content)
			}
		}

		// Check completion
		if event.Done && event.Response != nil && event.Response.Object == model.ObjectTypeRunnerCompletion {
			fmt.Printf("🎯 Round analysis completed!\n")
			break
		}
	}

	return nil
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
