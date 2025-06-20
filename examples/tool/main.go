// Package main demonstrates how to use the OpenAI-like model with environment variables.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
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

// nonStreamingExample demonstrates non-streaming usage.
func nonStreamingExample(ctx context.Context, llm *openai.Model) error {
	temperature := 0.9
	maxTokens := 1000
	getWeatherTool := tool.NewFunctionTool(getWeather, tool.FunctionToolConfig{
		Name:        "get_weather",
		Description: "Get weather at the given location",
	})

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful weather guide. If you don't have real-time weather data, you should call tool user provided."),
			model.NewUserMessage("What is the weather in New York City? "),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      false,
		},
		Tools: map[string]tool.Tool{
			"get_weather": getWeatherTool,
		},
	}

	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	for response := range responseChan {
		if response.Error != nil {
			return fmt.Errorf("API error: %s", response.Error.Message)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			fmt.Printf("Response: %s\n", choice.Message.Content)

			toolCalls := choice.Message.ToolCalls
			if len(toolCalls) == 0 {
				fmt.Println("No tool calls made.")
			} else {
				fmt.Println("Tool calls:")
				for _, toolCall := range toolCalls {
					if toolCall.Function.Name == "get_weather" {
						// Simulate getting weather data
						location := toolCall.Function.Arguments
						weatherData, err := getWeatherTool.Call(context.Background(), location)
						if err != nil {
							return fmt.Errorf("failed to call tool: %w", err)
						}
						bts, err := json.Marshal(weatherData)
						if err != nil {
							return fmt.Errorf("failed to marshal weather data: %w", err)
						}
						// Print the weather data
						fmt.Printf("CallTool at local: Weather in %s: %s\n", location, bts)
						request.Messages = append(request.Messages, model.NewToolCallMessage(string(bts), toolCall.ID))
					}
				}
			}

			responseChan2, err := llm.GenerateContent(ctx, request)
			if err != nil {
				return fmt.Errorf("failed to generate content: %w", err)
			}
			for response2 := range responseChan2 {
				if response2.Error != nil {
					return fmt.Errorf("API error: %s", response2.Error.Message)
				}
				fmt.Printf("Response from LLM: %s\n", response2.Choices[0].Message.Content)
			}

			if choice.FinishReason != nil {
				fmt.Printf("Finish reason: %s\n", *choice.FinishReason)
			}
		}

		if response.Usage != nil {
			fmt.Printf("Token usage - Prompt: %d, Completion: %d, Total: %d\n",
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				response.Usage.TotalTokens)
		}

		if response.Done {
			break
		}
	}

	return nil
}

type getWeatherInput struct {
	Location string `json:"location"`
}
type getWeatherOutput struct {
	Weather string `json:"weather"`
}

func getWeather(i getWeatherInput) getWeatherOutput {
	// In a real implementation, this function would call a weather API
	return getWeatherOutput{Weather: "Sunny, 25°C"}
}
