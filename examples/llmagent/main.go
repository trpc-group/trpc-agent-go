// Package main demonstrates how to use the LLMAgent implementation.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openailike"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
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

	fmt.Printf("Creating LLMAgent with configuration:\n")
	fmt.Printf("- Base URL: %s\n", baseURL)
	fmt.Printf("- Model Name: %s\n", modelName)
	fmt.Printf("- API Key: %s***\n", maskAPIKey(apiKey))
	fmt.Println()

	// Create a model instance.
	model := openailike.New(modelName, openailike.Options{
		APIKey:            apiKey,
		BaseURL:           baseURL,
		ChannelBufferSize: 50, // Larger buffer for agent use.
	})

	// Create an LLMAgent with configuration.
	llmAgent := llmagent.New(llmagent.Options{
		Name:               "demo-llm-agent",
		ChannelBufferSize:  20,
		RequestProcessors:  []flow.RequestProcessor{},  // No custom processors for this demo.
		ResponseProcessors: []flow.ResponseProcessor{}, // No custom processors for this demo.
	})

	fmt.Printf("Created LLMAgent: %s\n", llmAgent.Name())

	// Create an invocation context.
	invocation := &agent.Invocation{
		AgentName:     llmAgent.Name(),
		InvocationID:  "demo-invocation-001",
		EndInvocation: false,
		Model:         model,
	}

	// Run the agent.
	ctx := context.Background()
	eventChan, err := llmAgent.Run(ctx, invocation)
	if err != nil {
		log.Fatalf("Failed to run LLMAgent: %v", err)
	}

	fmt.Println("\n=== LLMAgent Execution ===")
	fmt.Println("Processing events from LLMAgent:")

	// Process events from the agent.
	eventCount := 0
	for event := range eventChan {
		eventCount++

		fmt.Printf("\n--- Event %d ---\n", eventCount)
		fmt.Printf("ID: %s\n", event.ID)
		fmt.Printf("Author: %s\n", event.Author)
		fmt.Printf("InvocationID: %s\n", event.InvocationID)

		if event.Error != nil {
			fmt.Printf("Error: %s (Type: %s)\n", event.Error.Message, event.Error.Type)
		}

		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			if choice.Message.Content != "" {
				fmt.Printf("Message Content: %s\n", choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				fmt.Printf("Delta Content: %s\n", choice.Delta.Content)
			}
			if choice.FinishReason != nil {
				fmt.Printf("Finish Reason: %s\n", *choice.FinishReason)
			}
		}

		if event.Usage != nil {
			fmt.Printf("Token Usage - Prompt: %d, Completion: %d, Total: %d\n",
				event.Usage.PromptTokens,
				event.Usage.CompletionTokens,
				event.Usage.TotalTokens)
		}

		fmt.Printf("Done: %t\n", event.Done)

		if event.Done {
			break
		}
	}

	fmt.Printf("\n=== Execution Complete ===\n")
	fmt.Printf("Total events processed: %d\n", eventCount)

	if eventCount == 0 {
		fmt.Println("No events were generated. This might indicate:")
		fmt.Println("- No request processors provided (agent had nothing to request)")
		fmt.Println("- Model configuration issues")
		fmt.Println("- Consider adding request processors for actual LLM interactions")
	}
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
