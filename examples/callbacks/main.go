package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/core/tool/function"
)

// Tool input/output structures
type CalculatorInput struct {
	A int `json:"a"`
	B int `json:"b"`
}

type CalculatorOutput struct {
	Result int `json:"result"`
}

// Calculator tool function
func calculator(input CalculatorInput) CalculatorOutput {
	return CalculatorOutput{Result: input.A + input.B}
}

type WeatherInput struct {
	City string `json:"city"`
}

type WeatherOutput struct {
	Weather string `json:"weather"`
}

// Weather tool function
func weather(input WeatherInput) WeatherOutput {
	return WeatherOutput{Weather: "Sunny, 25°C"}
}

// MockModel implements model.Model for testing purposes.
type MockModel struct{}

func (m *MockModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	toolCount := 0
	for _, msg := range request.Messages {
		if msg.Role == model.RoleTool {
			toolCount++
		}
	}
	var response *model.Response
	if toolCount == 0 {
		response = &model.Response{
			ID:      "mock-response-1",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "mock-model",
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID: "call_1",
								Function: model.FunctionDefinitionParam{
									Name:      "calculator",
									Arguments: []byte(`{"a": 5, "b": 3}`),
								},
							},
							{
								ID: "call_2",
								Function: model.FunctionDefinitionParam{
									Name:      "weather",
									Arguments: []byte(`{"city": "Beijing"}`),
								},
							},
						},
					},
				},
			},
		}
	} else {
		response = &model.Response{
			ID:      "mock-response-2",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "mock-model",
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Based on the calculations and weather data, 5 + 3 = 8 and the weather in Beijing is sunny at 25°C.",
					},
				},
			},
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

func main() {
	fmt.Println("🚀 Starting Comprehensive Callbacks Example")
	fmt.Println("==========================================")

	// Create all types of callbacks
	agentCallbacks := createAgentCallbacks()
	modelCallbacks := createModelCallbacks()
	toolCallbacks := createToolCallbacks()

	// Create tools
	calculatorTool := function.NewFunctionTool(calculator,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations"),
	)

	weatherTool := function.NewFunctionTool(weather,
		function.WithName("weather"),
		function.WithDescription("Get weather information for a city"),
	)

	// Create LLM model (using mock for demonstration)
	llm := &MockModel{}

	// Create LLM Agent with callbacks
	llmAgent := llmagent.New("example-agent",
		llmagent.WithModel(llm),
		llmagent.WithAgentCallbacks(agentCallbacks),
		llmagent.WithModelCallbacks(modelCallbacks),
		llmagent.WithTools([]tool.Tool{
			calculatorTool,
			weatherTool,
		}),
	)

	// Create invocation
	invocation := &agent.Invocation{
		Agent:          llmAgent,
		AgentName:      "example-agent",
		InvocationID:   "example-invocation",
		Model:          llm,
		Message:        model.NewUserMessage("Please calculate 5 + 3 and tell me the weather in Beijing"),
		AgentCallbacks: agentCallbacks,
		ModelCallbacks: modelCallbacks,
		ToolCallbacks:  toolCallbacks,
	}

	fmt.Println("📝 User Message:", invocation.Message.Content)
	fmt.Println()

	// Run the agent
	ctx := context.Background()
	eventChan, err := llmAgent.Run(ctx, invocation)
	if err != nil {
		log.Fatalf("Failed to run agent: %v", err)
	}

	// Process events
	fmt.Println("📡 Processing events...")
	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("❌ Error: %v\n", event.Error)
			continue
		}

		if event.Response != nil && len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]

			// Handle tool calls
			if len(choice.Message.ToolCalls) > 0 {
				fmt.Println("🔧 Tool calls detected:")
				for _, toolCall := range choice.Message.ToolCalls {
					fmt.Printf("   - Tool: %s\n", toolCall.Function.Name)
					fmt.Printf("   - Args: %s\n", string(toolCall.Function.Arguments))
				}
			}

			// Handle tool responses
			if choice.Message.Role == model.RoleTool {
				fmt.Printf("✅ Tool response: %s\n", choice.Message.Content)
			}

			// Handle assistant content
			if choice.Message.Content != "" {
				fmt.Printf("🤖 Assistant: %s\n", choice.Message.Content)
				break // Break the loop if assistant has content to avoid infinite loop.
			}

			if choice.Delta.Content != "" {
				fmt.Printf("🤖 Assistant: %s\n", choice.Delta.Content)
			}
		}

		if event.Done {
			break
		}
	}

	fmt.Println()
	fmt.Println("✨ Example completed!")
}

// createAgentCallbacks creates agent callbacks with comprehensive logging
func createAgentCallbacks() *agent.AgentCallbacks {
	callbacks := agent.NewAgentCallbacks()

	// Before Agent Callback
	callbacks.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		fmt.Println("🔄 Before Agent Callback:")
		fmt.Printf("   - Agent: %s\n", invocation.AgentName)
		fmt.Printf("   - Invocation ID: %s\n", invocation.InvocationID)
		fmt.Printf("   - Message: %s\n", invocation.Message.Content)

		// Example: Skip agent execution for certain conditions
		if invocation.Message.Content == "skip" {
			fmt.Println("   ⏭️  Skipping agent execution")
			return &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Agent execution was skipped by callback",
						},
					},
				},
			}, true, nil
		}

		// Example: Return custom response for certain conditions
		if invocation.Message.Content == "custom" {
			fmt.Println("   🎯 Returning custom response")
			return &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "This is a custom response from before agent callback",
						},
					},
				},
			}, false, nil
		}

		fmt.Println("   ✅ Proceeding with normal agent execution")
		return nil, false, nil
	})

	// After Agent Callback
	callbacks.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
		fmt.Println("🔄 After Agent Callback:")
		if runErr != nil {
			fmt.Printf("   ❌ Agent execution failed: %v\n", runErr)
			return &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Agent execution failed, but we handled it gracefully",
						},
					},
				},
			}, true, nil
		}

		fmt.Println("   ✅ Agent execution completed successfully")
		return nil, false, nil
	})

	return callbacks
}

// createModelCallbacks creates model callbacks with comprehensive logging
func createModelCallbacks() *model.ModelCallbacks {
	callbacks := model.NewModelCallbacks()

	// Before Model Callback
	callbacks.AddBeforeModel(func(ctx context.Context, request *model.Request) (*model.Response, bool, error) {
		fmt.Println("🔄 Before Model Callback:")
		fmt.Printf("   - Messages count: %d\n", len(request.Messages))
		fmt.Printf("   - Tools count: %d\n", len(request.Tools))

		// Example: Skip model call for certain conditions
		if len(request.Messages) == 0 {
			fmt.Println("   ⏭️  Skipping model call (no messages)")
			return &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "No messages to process",
						},
					},
				},
			}, true, nil
		}

		// Example: Return custom response for certain conditions
		if request.Messages[len(request.Messages)-1].Content == "custom_model" {
			fmt.Println("   🎯 Returning custom model response")
			return &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "This is a custom response from before model callback",
						},
					},
				},
			}, false, nil
		}

		fmt.Println("   ✅ Proceeding with normal model call")
		return nil, false, nil
	})

	// After Model Callback
	callbacks.AddAfterModel(func(ctx context.Context, response *model.Response, runErr error) (*model.Response, bool, error) {
		fmt.Println("🔄 After Model Callback:")
		if runErr != nil {
			fmt.Printf("   ❌ Model call failed: %v\n", runErr)
			return &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Model call failed, but we handled it gracefully",
						},
					},
				},
			}, true, nil
		}

		if response != nil && len(response.Choices) > 0 {
			fmt.Printf("   ✅ Model call successful, choices: %d\n", len(response.Choices))

			// Example: Override response for certain conditions
			if len(response.Choices) > 0 && response.Choices[0].Message.Content == "override" {
				fmt.Println("   🎯 Overriding model response")
				return &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "This response was overridden by after model callback",
							},
						},
					},
				}, true, nil
			}
		}

		return nil, false, nil
	})

	return callbacks
}

// createToolCallbacks creates tool callbacks with comprehensive logging
func createToolCallbacks() *tool.ToolCallbacks {
	callbacks := tool.NewToolCallbacks()

	// Before Tool Callback
	callbacks.AddBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
		fmt.Println("🔄 Before Tool Callback:")
		fmt.Printf("   - Tool: %s\n", toolName)
		fmt.Printf("   - Args: %s\n", string(jsonArgs))

		// Example: Skip tool execution for certain conditions
		if toolName == "skip-tool" {
			fmt.Println("   ⏭️  Skipping tool execution")
			return map[string]string{"skipped": "true"}, true, nil
		}

		// Example: Return custom result for certain conditions
		if toolName == "calculator" {
			var args CalculatorInput
			if err := json.Unmarshal(jsonArgs, &args); err == nil && args.A == 0 && args.B == 0 {
				fmt.Println("   🎯 Returning custom calculator result")
				return CalculatorOutput{Result: 42}, false, nil
			}
		}

		// Example: Modify arguments
		if toolName == "weather" {
			var args WeatherInput
			if err := json.Unmarshal(jsonArgs, &args); err == nil {
				if args.City == "test" {
					fmt.Println("   🔧 Modifying weather tool arguments")
					args.City = "Beijing"
					// Note: In a real implementation, you would modify the args and continue
					// For this example, we just log the modification
				}
			}
		}

		fmt.Println("   ✅ Proceeding with normal tool execution")
		return nil, false, nil
	})

	// After Tool Callback
	callbacks.AddAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		fmt.Println("🔄 After Tool Callback:")
		fmt.Printf("   - Tool: %s\n", toolName)

		if runErr != nil {
			fmt.Printf("   ❌ Tool execution failed: %v\n", runErr)
			return map[string]string{"error": "handled"}, true, nil
		}

		fmt.Printf("   ✅ Tool execution successful, result: %v\n", result)

		// Example: Override result for certain conditions
		if toolName == "calculator" {
			if calcResult, ok := result.(CalculatorOutput); ok {
				if calcResult.Result == 8 {
					fmt.Println("   🎯 Overriding calculator result")
					return map[string]string{
						"formatted_result": fmt.Sprintf("The answer is %d", calcResult.Result),
						"original_result":  fmt.Sprintf("%d", calcResult.Result),
					}, true, nil
				}
			}
		}

		// Example: Add metadata to weather results
		if toolName == "weather" {
			if weatherResult, ok := result.(WeatherOutput); ok {
				fmt.Println("   📊 Adding metadata to weather result")
				return map[string]interface{}{
					"weather":  weatherResult.Weather,
					"metadata": map[string]string{"source": "callback", "timestamp": time.Now().Format(time.RFC3339)},
				}, true, nil
			}
		}

		return nil, false, nil
	})

	return callbacks
}
