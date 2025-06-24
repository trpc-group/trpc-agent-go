// Package main demonstrates how to use the OpenAI-like model with environment variables.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
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

	fmt.Printf("Using configuration:\n")
	fmt.Printf("- Base URL: %s\n", baseURL)
	fmt.Printf("- Model Name: %s\n", modelName)
	fmt.Printf("- API Key: %s***\n", maskAPIKey(apiKey))
	fmt.Println()

	// Create a new OpenAI-like model instance using the new package structure.
	llm := openai.New(modelName, openai.Options{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})

	ctx := context.Background()

	fmt.Println("=== Non-streaming Example ===")
	if err := nonStreamingExample(ctx, llm); err != nil {
		log.Printf("Non-streaming example failed: %v", err)
	}

	fmt.Println("=== Streaming Example ===")
	if err := streamingExample(ctx, llm); err != nil {
		log.Printf("Streaming example failed: %v", err)
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
