//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// streamingOutputExample demonstrates non-streaming usage.
func streamingOutputExample(ctx context.Context, llm *openai.Model) error {
	temperature := 0.8
	maxTokens := 1000

	getWeatherStreamingTool := function.NewStreamableFunctionTool[getWeatherInput, getWeatherOutput](
		getStreamableWeather, function.WithName("get_weather"),
		function.WithDescription("Get weather at the given location"))

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(
				"You are a helpful weather guide. If you don't have real-time " +
					"weather data, you should call the user-provided tool.",
			),
			model.NewUserMessage("What is the weather in XYZ City? "),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      false,
		},
		Tools: map[string]tool.Tool{
			"get_weather": getWeatherStreamingTool,
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
			fmt.Printf("Response: delta %v\n", choice.Delta)
			fmt.Printf("Response: reason %v\n", *choice.FinishReason)
			fmt.Printf("Response: len(Choices) %d\n", len(response.Choices))

			if len(choice.Message.ToolCalls) == 0 {
				fmt.Println("No tool calls made.")
			} else {
				fmt.Println("StreamableTool calls:")
				for _, toolCall := range choice.Message.ToolCalls {
					if toolCall.Function.Name == "get_weather" {
						// Simulate getting weather data
						location := toolCall.Function.Arguments
						reader, err := getWeatherStreamingTool.StreamableCall(context.Background(), location)
						if err != nil {
							return fmt.Errorf("failed to call tool: %w", err)
						}

						var contents []any
						for {
							chunk, err := reader.Recv()
							if err == io.EOF {
								break
							}
							if err != nil {
								log.Errorf("StreamableTool execution failed for %s: "+
									"receive chunk from stream reader failed: %v, "+
									"may merge incomplete data", toolCall.Function.Name, err)
								break
							}
							contents = append(contents, chunk.Content)
						}
						reader.Close()
						result := tool.Merge(contents)
						bts, err := json.Marshal(result)
						if err != nil {
							return fmt.Errorf("failed to marshal weather data: %w", err)
						}
						// Print the weather data
						fmt.Printf("CallTool at local: Weather in %s: %s\n", location, bts)
						request.Messages = append(request.Messages, model.Message{
							Role:      model.RoleTool,
							Content:   string(bts),
							ToolCalls: []model.ToolCall{toolCall},
						})
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
