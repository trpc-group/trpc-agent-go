//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use the dynamic model switching functionality.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func main() {
	// Read configuration from command line flags.
	defaultModelName := flag.String("model", "gpt-4o-mini", "Default model to use")
	flag.Parse()

	fmt.Printf("🚀 Model Switching Example\n")
	fmt.Printf("   📝 Default Model: %s\n", *defaultModelName)
	fmt.Printf("   🔑 OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment\n")

	fmt.Println()

	// Create multiple models for demonstration.
	defaultModel := openai.New(*defaultModelName)
	creativeModel := openai.New("gpt-4o")
	efficientModel := openai.New("gpt-3.5-turbo")

	// Create agent with multiple models.
	agent := llmagent.New("multi-model-agent",
		llmagent.WithModels(defaultModel, creativeModel, efficientModel))

	ctx := context.Background()

	fmt.Println("🔧 === Initial Setup ===")
	if err := initialSetupExample(ctx, agent); err != nil {
		log.Printf("❌ Initial setup failed: %v", err)
	}

	fmt.Println("\n📋 === Model Information ===")
	if err := modelInformationExample(ctx, agent); err != nil {
		log.Printf("❌ Model information failed: %v", err)
	}

	fmt.Println("\n🔄 === Model Switching ===")
	if err := modelSwitchingExample(ctx, agent); err != nil {
		log.Printf("❌ Model switching failed: %v", err)
	}

	fmt.Println("\n💬 === Conversation Examples ===")
	if err := conversationExamples(ctx, agent); err != nil {
		log.Printf("❌ Conversation examples failed: %v", err)
	}

	fmt.Println("\n📊 === Performance Comparison ===")
	if err := performanceComparison(ctx, agent); err != nil {
		log.Printf("❌ Performance comparison failed: %v", err)
	}

	fmt.Println("🎉 === Demo Complete ===")
}

// initialSetupExample demonstrates the initial setup and model registration.
func initialSetupExample(_ context.Context, agent agent.Agent) error {
	fmt.Println("🏗️ Creating agent with multiple models...")

	// Display all available models.
	models := agent.Models()
	fmt.Printf("✅ Agent created with %d models:\n", len(models))

	for i, m := range models {
		info := m.Info()
		fmt.Printf("   %d. %s\n", i+1, info.Name)
	}

	// Show current active model.
	activeModel := agent.ActiveModel()
	fmt.Printf("🎯 Current active model: %s\n", activeModel.Info().Name)

	return nil
}

// modelInformationExample displays detailed information about all models.
func modelInformationExample(_ context.Context, agent agent.Agent) error {
	fmt.Println("📊 Getting detailed model information...")

	models := agent.Models()
	for i, m := range models {
		info := m.Info()
		fmt.Printf("\n🔍 Model %d:\n", i+1)
		fmt.Printf("   📝 Name: %s\n", info.Name)
	}

	return nil
}

// modelSwitchingExample demonstrates switching between different models.
func modelSwitchingExample(_ context.Context, agent agent.Agent) error {
	fmt.Println("🔄 Demonstrating model switching...")

	// Get available model names.
	models := agent.Models()
	modelNames := make([]string, len(models))
	for i, m := range models {
		modelNames[i] = m.Info().Name
	}

	fmt.Printf("📋 Available models: %v\n", modelNames)

	// Switch to each model and verify.
	for _, modelName := range modelNames {
		fmt.Printf("\n🔄 Switching to model: %s\n", modelName)

		// Switch to the model.
		err := agent.SwitchModel(modelName)
		if err != nil {
			fmt.Printf("❌ Failed to switch to %s: %v\n", modelName, err)
			continue
		}

		// Verify the switch.
		activeModel := agent.ActiveModel()
		fmt.Printf("✅ Successfully switched to: %s\n", activeModel.Info().Name)

		// Small delay to show the switching.
		time.Sleep(500 * time.Millisecond)
	}

	// Switch back to default model.
	defaultModelName := models[0].Info().Name
	fmt.Printf("\n🔄 Switching back to default model: %s\n", defaultModelName)
	err := agent.SwitchModel(defaultModelName)
	if err != nil {
		return fmt.Errorf("failed to switch back to default model: %w", err)
	}
	fmt.Printf("✅ Back to default model: %s\n", agent.ActiveModel().Info().Name)

	return nil
}

// conversationExamples shows conversations with different models.
func conversationExamples(ctx context.Context, agent agent.Agent) error {
	fmt.Println("💬 Testing conversations with different models...")

	conversations := []struct {
		modelName   string
		prompt      string
		description string
	}{
		{
			modelName:   "gpt-4o-mini",
			prompt:      "Explain quantum computing in simple terms.",
			description: "Efficient model for straightforward explanations",
		},
		{
			modelName:   "gpt-4o",
			prompt:      "Write a creative story about a time traveler.",
			description: "Creative model for storytelling",
		},
	}

	for _, conv := range conversations {
		fmt.Printf("\n🎭 --- %s (%s) ---\n", conv.modelName, conv.description)

		// Switch to the specified model.
		err := agent.SwitchModel(conv.modelName)
		if err != nil {
			fmt.Printf("❌ Failed to switch to %s: %v\n", conv.modelName, err)
			continue
		}

		// Verify the switch.
		activeModel := agent.ActiveModel()
		fmt.Printf("🎯 Active model: %s\n", activeModel.Info().Name)

		// Create a request for the model.
		temperature := 0.7
		maxTokens := 300
		request := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage("You are a helpful assistant."),
				model.NewUserMessage(conv.prompt),
			},
			GenerationConfig: model.GenerationConfig{
				Temperature: &temperature,
				MaxTokens:   &maxTokens,
				Stream:      false,
			},
		}

		// Generate content using the active model.
		responseChan, err := activeModel.GenerateContent(ctx, request)
		if err != nil {
			fmt.Printf("❌ Failed to generate content: %v\n", err)
			continue
		}

		// Process the response.
		fmt.Printf("💬 Response from %s:\n", conv.modelName)
		for response := range responseChan {
			if response.Error != nil {
				fmt.Printf("❌ API error: %s\n", response.Error.Message)
				break
			}

			if len(response.Choices) > 0 {
				choice := response.Choices[0]
				content := choice.Message.Content
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				fmt.Printf("   %s\n", content)

				if choice.FinishReason != nil {
					fmt.Printf("   🏁 Finish reason: %s\n", *choice.FinishReason)
				}
			}

			if response.Usage != nil {
				fmt.Printf("   💎 Token usage - Prompt: %d, Completion: %d, Total: %d\n",
					response.Usage.PromptTokens,
					response.Usage.CompletionTokens,
					response.Usage.TotalTokens)
			}

			if response.Done {
				break
			}
		}
	}

	return nil
}

// performanceComparison compares responses from different models.
func performanceComparison(ctx context.Context, agent agent.Agent) error {
	fmt.Println("📊 Comparing performance across different models...")

	testPrompt := "Write a haiku about artificial intelligence."
	models := agent.Models()
	results := make(map[string]time.Duration)

	for _, m := range models {
		modelName := m.Info().Name
		fmt.Printf("\n⏱️ Testing %s...\n", modelName)

		// Switch to the model.
		err := agent.SwitchModel(modelName)
		if err != nil {
			fmt.Printf("❌ Failed to switch to %s: %v\n", modelName, err)
			continue
		}

		// Get the active model.
		activeModel := agent.ActiveModel()

		// Create a request for performance testing.
		temperature := 0.8
		maxTokens := 100
		request := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage("You are a helpful assistant."),
				model.NewUserMessage(testPrompt),
			},
			GenerationConfig: model.GenerationConfig{
				Temperature: &temperature,
				MaxTokens:   &maxTokens,
				Stream:      false,
			},
		}

		// Measure actual response time.
		start := time.Now()
		responseChan, err := activeModel.GenerateContent(ctx, request)
		if err != nil {
			fmt.Printf("❌ Failed to generate content: %v\n", err)
			continue
		}

		// Wait for response.
		for response := range responseChan {
			if response.Error != nil {
				fmt.Printf("❌ API error: %s\n", response.Error.Message)
				break
			}

			if response.Done {
				break
			}
		}

		duration := time.Since(start)
		results[modelName] = duration
		fmt.Printf("✅ %s responded in %v\n", modelName, duration)
	}

	// Display performance summary.
	fmt.Println("\n📈 Performance Summary:")
	for modelName, duration := range results {
		fmt.Printf("   🏃 %s: %v\n", modelName, duration)
	}

	return nil
}
