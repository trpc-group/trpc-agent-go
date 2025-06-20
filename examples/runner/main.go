// Package main demonstrates how to use the Runner with LLMAgent and
// OpenAI-like model with environment variables.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/runner"
)

func main() {
	// Read configuration from environment variables.
	baseURL := getEnv("MODEL_BASE_URL", "https://api.openai.com/v1")
	modelName := getEnv("MODEL_NAME", "gpt-4o-mini")
	apiKey := getEnv("OPENAI_API_KEY", "")

	// Validate required environment variables.
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	fmt.Printf("Creating Runner with configuration:\n")
	fmt.Printf("- Base URL: %s\n", baseURL)
	fmt.Printf("- Model Name: %s\n", modelName)
	fmt.Printf("- API Key: %s***\n", maskAPIKey(apiKey))
	fmt.Println()

	// 1. Create OpenAI-like model.
	modelInstance := openai.New(modelName, openai.Options{
		APIKey:            apiKey,
		BaseURL:           baseURL,
		ChannelBufferSize: 512, // Custom buffer size for high-throughput scenarios.
	})

	// 2. Create LLMAgent.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1500),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming for runner.
	}

	agentName := "assistant-agent"
	llmAgent := llmagent.New(
		agentName,
		llmagent.Options{
			Model:             modelInstance,
			Description:       "A helpful AI assistant for demonstrations using Runner",
			Instruction:       "Be helpful, concise, and informative in your responses",
			SystemPrompt:      "You are a helpful assistant designed to demonstrate Runner capabilities with streaming",
			GenerationConfig:  genConfig,
			ChannelBufferSize: 100,
		},
	)

	// 3. Create Runner (session service not currently used by Runner).
	appName := "runner-demo-app"
	runnerInstance := runner.New(
		appName,
		llmAgent,
		runner.Options{},
	)

	fmt.Printf("Created Runner: %s with agent: %s\n", appName, agentName)
	fmt.Println()

	// 5. Use runner to run the agent with streaming.
	ctx := context.Background()
	userID := "demo-user-001"
	sessionID := "demo-session-001"
	userMessage := model.NewUserMessage("Hello! Can you tell me an interesting fact about Go programming language concurrency features?")

	fmt.Println("=== Runner Streaming Execution ===")
	fmt.Printf("User: %s\n", userMessage.Content)
	fmt.Printf("Starting streaming response...\n\n")

	// Run the agent through the runner.
	eventChan, err := runnerInstance.Run(ctx, userID, sessionID, userMessage, agent.RunOptions{})
	if err != nil {
		log.Fatalf("Failed to run agent through Runner: %v", err)
	}

	// Process streaming events.
	eventCount := 0
	var fullContent string
	var lastFinishReason *string

	fmt.Print("Assistant: ")

	for event := range eventChan {
		eventCount++

		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\nError: %s (Type: %s)\n", event.Error.Message, event.Error.Type)
			continue
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]

			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}

			// Handle complete message content (for non-streaming chunks).
			if choice.Message.Content != "" && choice.Delta.Content == "" {
				fmt.Print(choice.Message.Content)
				fullContent += choice.Message.Content
			}

			// Store finish reason.
			if choice.FinishReason != nil {
				lastFinishReason = choice.FinishReason
			}
		}

		// Check if this is the final event.
		if event.Done {
			fmt.Printf("\n\n=== Streaming Complete ===\n")
			if lastFinishReason != nil {
				fmt.Printf("Finish reason: %s\n", *lastFinishReason)
			}

			if event.Usage != nil {
				fmt.Printf("Token usage - Prompt: %d, Completion: %d, Total: %d\n",
					event.Usage.PromptTokens,
					event.Usage.CompletionTokens,
					event.Usage.TotalTokens)
			}

			fmt.Printf("Total events processed: %d\n", eventCount)
			fmt.Printf("Response length: %d characters\n", len(fullContent))
			break
		}
	}

	fmt.Println("\n=== Runner Demo Complete ===")

	if eventCount == 0 {
		fmt.Println("No events were generated. This might indicate:")
		fmt.Println("- Model configuration issues")
		fmt.Println("- Network connectivity problems")
		fmt.Println("- Check the logs for more details")
	}

	// Demonstrate a follow-up conversation.
	fmt.Println("\n=== Follow-up Conversation ===")
	followUpMessage := model.NewUserMessage("Can you give me a code example of using channels?")
	fmt.Printf("User: %s\n", followUpMessage.Content)

	followUpChan, err := runnerInstance.Run(ctx, userID, sessionID, followUpMessage, agent.RunOptions{})
	if err != nil {
		log.Printf("Failed to run follow-up: %v", err)
		return
	}

	fmt.Print("Assistant: ")
	for event := range followUpChan {
		if event.Error != nil {
			fmt.Printf("\nError: %s\n", event.Error.Message)
			continue
		}

		if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
			fmt.Print(event.Choices[0].Delta.Content)
		}

		if event.Done {
			fmt.Println("")
			break
		}
	}

	fmt.Println("=== Demo Complete ===")
}

// getEnv gets an environment variable with a default value.
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// maskAPIKey masks the API key for logging purposes.
func maskAPIKey(apiKey string) string {
	if len(apiKey) <= 6 {
		return "***"
	}
	return apiKey[:3]
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
