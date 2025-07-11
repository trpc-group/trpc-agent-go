package main

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// streamingInputExample demonstrates streaming usage.
func streamingInputExample(ctx context.Context, llm *openai.Model) error {
	temperature := 0.9
	maxTokens := 1000

	getWeatherTool := function.NewFunctionTool(getWeather, function.WithName("get_weather"), function.WithDescription("Get weather at the given location"))
	getPopulationTool := function.NewFunctionTool(getPopulation, function.WithName("get_population"), function.WithDescription("Get population at the given city"))

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful weather guide. If you don't have real-time weather data, you should call tool user provided."),
			model.NewUserMessage("What is the weather and population in London City? "),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      true,
		},
		Tools: map[string]tool.Tool{
			"get_weather":    getWeatherTool,
			"get_population": getPopulationTool,
		},
	}

	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	fmt.Print("Streaming response: \n")
	var fullContent string
	// var toolCalls []model.ToolCall
	for response := range responseChan {
		if response.Error != nil {
			return fmt.Errorf("API error: %s", response.Error.Message)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}

			if choice.FinishReason != nil {
				fmt.Printf("\nFinish reason: %s\n", *choice.FinishReason)
			}

			for _, tc := range choice.Message.ToolCalls {
				if tc.Function.Name == "get_weather" {
					// Simulate getting weather data
					location := tc.Function.Arguments
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
					request.Messages = append(request.Messages, model.Message{
						Role:      model.RoleTool,
						Content:   string(bts),
						ToolCalls: []model.ToolCall{tc},
					})
				}
				if tc.Function.Name == "get_population" {
					// Simulate getting population data
					city := tc.Function.Arguments
					populationData, err := getPopulationTool.Call(context.Background(), city)
					if err != nil {
						return fmt.Errorf("failed to call tool: %w", err)
					}
					bts, err := json.Marshal(populationData)
					if err != nil {
						return fmt.Errorf("failed to marshal population data: %w", err)
					}
					// Print the population data
					fmt.Printf("CallTool at local: Population in %s: %s\n", city, bts)
					request.Messages = append(request.Messages, model.Message{
						Role:      model.RoleTool,
						Content:   string(bts),
						ToolCalls: []model.ToolCall{tc},
					})

				}
			}
		}

		if response.Done {
			fmt.Printf("\n\nStreaming completed. Full content length: %d characters\n", len(fullContent))
			break
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

		if len(response2.Choices) > 0 {
			choice := response2.Choices[0]
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}

			if choice.FinishReason != nil {
				fmt.Printf("\nFinish reason: %s\n", *choice.FinishReason)
			}
		}
		if response2.Done {
			fmt.Printf("\n\nStreaming completed. Full content length: %d characters\n", len(fullContent))
			break
		}
	}
	return nil
}
