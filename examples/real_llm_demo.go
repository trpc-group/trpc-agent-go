package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// Command line flags
var (
	openaiURL     = flag.String("openai-url", "https://api.openai.com/v1", "OpenAI API base URL")
	modelName     = flag.String("model-name", "gpt-3.5-turbo", "Model name to use")
	modelProvider = flag.String("model-provider", "openai", "Model provider")
	apiKeyEnv     = flag.String("api-key-env", "OPENAI_API_KEY", "Environment variable for API key")
	interactive   = flag.Bool("interactive", false, "Run in interactive mode")
)

// simpleCalculatorTool for testing tool calling with real LLMs.
type simpleCalculatorTool struct{}

func (t *simpleCalculatorTool) Name() string {
	return "calculator"
}

func (t *simpleCalculatorTool) Description() string {
	return "A simple calculator that can perform basic arithmetic operations. Use operation parameter with value 'add', 'subtract', 'multiply', or 'divide', and provide two numbers as parameters 'a' and 'b'. For example, to calculate 123 + 456, use operation='add', a=123, b=456."
}

func (t *simpleCalculatorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"description": "The operation to perform: add, subtract, multiply, or divide",
				"enum":        []string{"add", "subtract", "multiply", "divide"},
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "First number",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "Second number",
			},
		},
		"required": []string{"operation", "a", "b"},
	}
}

func (t *simpleCalculatorTool) GetDefinition() *tool.ToolDefinition {
	return tool.ToolDefinitionFromParameters(t.Name(), t.Description(), t.Parameters())
}

func (t *simpleCalculatorTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	operation, ok := args["operation"].(string)
	if !ok {
		return nil, fmt.Errorf("parameter 'operation' must be a string")
	}

	a, ok := args["a"].(float64)
	if !ok {
		return nil, fmt.Errorf("parameter 'a' must be a number")
	}

	b, ok := args["b"].(float64)
	if !ok {
		return nil, fmt.Errorf("parameter 'b' must be a number")
	}

	var result float64
	switch operation {
	case "add":
		result = a + b
	case "subtract":
		result = a - b
	case "multiply":
		result = a * b
	case "divide":
		if b == 0 {
			return nil, fmt.Errorf("cannot divide by zero")
		}
		result = a / b
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	return &tool.Result{
		Output: fmt.Sprintf("%.2f", result),
	}, nil
}

func createRealModel() (model.Model, error) {
	apiKey := os.Getenv(*apiKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found in environment variable %s", *apiKeyEnv)
	}

	switch *modelProvider {
	case "openai":
		// Use the OpenAI model implementation from existing code
		openaiModel := model.NewOpenAIModel(*modelName,
			model.WithOpenAIAPIKey(apiKey),
			model.WithOpenAIBaseURL(*openaiURL),
		)
		return openaiModel, nil
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", *modelProvider)
	}
}

func runBasicTests(ctx context.Context, llmAgent agent.Agent) {
	fmt.Println("=== Test 1: Basic Text Response ===")

	textContent := event.NewTextContent("Hello! Please introduce yourself in one sentence.")
	fmt.Printf("Calling LLM with text: %s\n", textContent.GetText())

	response, err := llmAgent.Process(ctx, textContent)
	if err != nil {
		log.Printf("❌ Basic text test failed: %v", err)
		fmt.Printf("Debug: Error details: %v\n", err)
		return
	}

	fmt.Printf("Input: %s\n", textContent.GetText())
	fmt.Printf("Output: %s\n", response.GetText())
	fmt.Println("✅ Basic text response successful")

	fmt.Println("\n=== Test 2: Async Processing ===")

	asyncContent := event.NewTextContent("Tell me a very short joke.")
	eventCh, err := llmAgent.ProcessAsync(ctx, asyncContent)
	if err != nil {
		log.Printf("❌ Async test failed: %v", err)
		return
	}

	fmt.Printf("Input: %s\n", asyncContent.GetText())
	fmt.Print("Output: ")
	for event := range eventCh {
		if event.Content != nil && event.Content.GetText() != "" {
			fmt.Print(event.Content.GetText())
		}
	}
	fmt.Println()
	fmt.Println("✅ Async processing successful")

	fmt.Println("\n=== Test 3: Tool Usage ===")

	toolRequestContent := event.NewTextContent("Please calculate 123 + 456 using the calculator tool.")
	toolEventCh, err := llmAgent.ProcessAsync(ctx, toolRequestContent)
	if err != nil {
		log.Printf("❌ Tool test failed: %v", err)
		return
	}

	fmt.Printf("Input: %s\n", toolRequestContent.GetText())
	fmt.Print("Processing: ")

	hasToolCall := false
	hasResult := false

	for event := range toolEventCh {
		if event.Content != nil {
			// Check for function calls
			if calls := event.Content.GetFunctionCalls(); len(calls) > 0 {
				hasToolCall = true
				for _, call := range calls {
					fmt.Printf("\n🔧 Tool call: %s with args %v", call.Name, call.Arguments)
				}
			}

			// Check for function responses
			if responses := event.Content.GetFunctionResponses(); len(responses) > 0 {
				hasResult = true
				for _, resp := range responses {
					fmt.Printf("\n📊 Tool result: %s = %v", resp.Name, resp.Result)
				}
			}

			// Show text content
			if text := event.Content.GetText(); text != "" {
				fmt.Printf("\n💬 Response: %s", text)
			}
		}
	}

	fmt.Println()
	if hasToolCall && hasResult {
		fmt.Println("✅ Tool usage successful")
	} else {
		fmt.Printf("⚠️  Tool usage incomplete (call: %v, result: %v)", hasToolCall, hasResult)
	}
}

func runInteractiveMode(ctx context.Context, llmAgent agent.Agent) {
	fmt.Println("\n=== Interactive Mode ===")
	fmt.Println("Type your messages (type 'quit' to exit):")

	for {
		fmt.Print("\n> ")
		var input string
		fmt.Scanln(&input)

		if input == "quit" {
			break
		}

		if input == "" {
			continue
		}

		content := event.NewTextContent(input)
		eventCh, err := llmAgent.ProcessAsync(ctx, content)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			continue
		}

		fmt.Print("Assistant: ")
		for event := range eventCh {
			if event.Content != nil {
				// Show tool calls
				if calls := event.Content.GetFunctionCalls(); len(calls) > 0 {
					for _, call := range calls {
						fmt.Printf("[Using tool: %s] ", call.Name)
					}
				}

				// Show text
				if text := event.Content.GetText(); text != "" {
					fmt.Print(text)
				}
			}
		}
		fmt.Println()
	}
}

func main() {
	flag.Parse()

	fmt.Printf("Real LLM Test\n")
	fmt.Printf("=============\n")
	fmt.Printf("Provider: %s\n", *modelProvider)
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Base URL: %s\n", *openaiURL)
	fmt.Printf("API Key Env: %s\n", *apiKeyEnv)
	fmt.Printf("\n")

	ctx := context.Background()

	// Create real model
	realModel, err := createRealModel()
	if err != nil {
		log.Fatalf("Failed to create model: %v", err)
	}

	// Create calculator tool
	calculatorTool := &simpleCalculatorTool{}

	// Create LLM agent configuration
	config := agent.LLMAgentConfig{
		Name:         "real-llm-agent",
		Description:  "A real LLM agent for testing",
		Model:        realModel,
		SystemPrompt: "You are a helpful assistant. When asked to do calculations, use the calculator tool available to you. The calculator requires 'operation' (add/subtract/multiply/divide), 'a' (first number), and 'b' (second number) parameters.",
		Tools:        []tool.Tool{calculatorTool},
	}

	// Create the agent
	llmAgent, err := agent.NewLLMAgent(config)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// Run basic tests
	runBasicTests(ctx, llmAgent)

	// Run interactive mode if requested
	if *interactive {
		runInteractiveMode(ctx, llmAgent)
	}

	fmt.Println("\n=== Test Summary ===")
	fmt.Printf("Successfully tested real LLM: %s\n", *modelName)
	fmt.Printf("With provider: %s\n", *modelProvider)
	fmt.Printf("Base URL: %s\n", *openaiURL)
	fmt.Println("✅ All tests completed")
}
