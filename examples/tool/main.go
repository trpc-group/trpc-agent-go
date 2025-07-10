// Package main demonstrates how to use the OpenAI-like model with environment variables.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func main() {
	// Read configuration from environment variables.
	baseURL := getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	apiKey := getEnv("OPENAI_API_KEY", "")

	// Read configuration from command line flags.
	modelName := flag.String("model", "gpt-4o-mini", "Name of the model to use")
	flag.Parse()

	// Validate required environment variables.
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	fmt.Printf("Using configuration:\n")
	fmt.Printf("- Model Name: %s\n", *modelName)
	fmt.Printf("- Channel Buffer Size: 512\n")
	fmt.Printf("- OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment\n")
	fmt.Println()

	// Create a new OpenAI-like model instance using the new package structure.
	llm := openai.New(*modelName, openai.Options{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})

	ctx := context.Background()

	fmt.Println("=== Non-streaming Example ===")
	if err := nonStreamingExample(ctx, llm); err != nil {
		log.Printf("Non-streaming example failed: %v", err)
	}

	fmt.Println("=== Streaming Input Example ===")
	if err := streamingInputExample(ctx, llm); err != nil {
		log.Printf("Streaming Input example failed: %v", err)
	}

	fmt.Println("=== Streaming Output Example ===")
	if err := streamingOutputExample(ctx, llm); err != nil {
		log.Printf("Streaming  Outputexample failed: %v", err)
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
