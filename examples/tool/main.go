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
	if err := Example(ctx, llm); err != nil {
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

// Example demonstrates non-streaming usage.
func Example(ctx context.Context, llm *openai.Model) error {
	temperature := 0.9
	maxTokens := 1000
	getWeatherTool := tool.NewFunctionTool(getWeather, tool.FunctionToolConfig{
		Name:        "get_weather",
		Description: "Get weather at the given location",
	})

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful weather guide."),
			model.NewUserMessage("What is the weather in London City?"),
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
						weatherDataCh, err := getWeatherTool.Call(context.Background(), location)
						if err != nil {
							return fmt.Errorf("failed to call tool: %w", err)
						}

						var weatherData []any
						for data := range weatherDataCh {
							weatherData = append(weatherData, data)
							// The range will automatically exit when the channel is closed
							// No need for break as we'll consume until the last item
						}
						if err != nil {
							return fmt.Errorf("failed to call tool: %w", err)
						}

						var weather getWeatherOutput
						// Aggregate the weather data
						for _, data := range weatherData {
							weather.Humidity += fmt.Sprintf("%v", data.(getWeatherOutput).Humidity)
							weather.Temperature += fmt.Sprintf("%v", data.(getWeatherOutput).Temperature)
							weather.WindSpeed += fmt.Sprintf("%v", data.(getWeatherOutput).WindSpeed)
						}

						bts, err := json.Marshal(weather)
						if err != nil {
							return fmt.Errorf("failed to marshal weather data: %w", err)
						}
						// Print the weather data
						fmt.Printf("CallTool at local: Weather in %s: %s\n", location, bts)
						request.Messages = append(request.Messages, (model.NewToolCallMessage(string(bts), toolCall.ID)))
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

				toolCalls := choice.Message.ToolCalls
				if len(toolCalls) == 0 {
					fmt.Println("No tool calls made.")
				} else {
					fmt.Println("Tool calls:")
					for _, toolCall := range toolCalls {
						fmt.Printf("func_name: %v\n", toolCall.Function.Name)
					}
				}
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
	Humidity    string `json:"humidity"`
	Temperature string `json:"temperature"`
	WindSpeed   string `json:"wind_speed"`
	// Weather     string `json:"weather"`
}

func getWeather(i getWeatherInput) <-chan getWeatherOutput {
	// In a real implementation, this function would call a weather API
	ch := make(chan getWeatherOutput, 1)
	go func() {
		ch <- getWeatherOutput{Humidity: "65%"}
		ch <- getWeatherOutput{Temperature: "22°C"}
		ch <- getWeatherOutput{WindSpeed: "15 km/h"}
		close(ch)
	}()
	return ch
}
