package agent

import (
	"context"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// MockModel is a simple mock model for testing
type MockModel struct {
	response string
}

func (m *MockModel) Name() string {
	return "mock-model"
}

func (m *MockModel) Provider() string {
	return "mock"
}

func (m *MockModel) SetTools(tools []*tool.ToolDefinition) {
	// Mock implementation - do nothing
}

func (m *MockModel) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	return &model.Response{
		Text: m.response,
	}, nil
}

func (m *MockModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	return &model.Response{
		Text: m.response,
	}, nil
}

// MockTool is a simple calculator tool for testing
type MockCalculatorTool struct {
	*tool.BaseTool
}

func NewMockCalculatorTool() *MockCalculatorTool {
	return &MockCalculatorTool{
		BaseTool: tool.NewBaseTool(
			"calculator",
			"Performs basic arithmetic operations",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"a": map[string]interface{}{
						"type":        "number",
						"description": "First number",
					},
					"b": map[string]interface{}{
						"type":        "number",
						"description": "Second number",
					},
					"operation": map[string]interface{}{
						"type":        "string",
						"description": "Operation to perform",
						"enum":        []string{"add", "subtract", "multiply", "divide"},
					},
				},
				"required": []string{"a", "b", "operation"},
			},
		),
	}
}

func (t *MockCalculatorTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	a, ok := args["a"].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid argument 'a': expected number")
	}

	b, ok := args["b"].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid argument 'b': expected number")
	}

	operation, ok := args["operation"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid argument 'operation': expected string")
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
			return nil, fmt.Errorf("division by zero")
		}
		result = a / b
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	return tool.NewResult(result), nil
}

func TestBasicLLMAgentWithTools(t *testing.T) {
	ctx := context.Background()

	// Create mock model
	mockModel := &MockModel{
		response: "The result of 2 + 3 is 5",
	}

	// Create calculator tool
	calculatorTool := NewMockCalculatorTool()

	// Create LLM agent with tools
	config := LLMAgentConfig{
		Name:         "test-agent",
		Description:  "A test agent with calculator",
		Model:        mockModel,
		SystemPrompt: "You are a helpful assistant with access to a calculator.",
		Tools:        []tool.Tool{calculatorTool},
	}

	agent, err := NewLLMAgent(config)
	if err != nil {
		t.Fatalf("Failed to create LLM agent: %v", err)
	}

	// Test basic text response
	t.Run("BasicTextResponse", func(t *testing.T) {
		input := event.NewTextContent("Hello")
		events, err := agent.ProcessAsync(ctx, input)
		if err != nil {
			t.Fatalf("ProcessAsync failed: %v", err)
		}

		var responseEvent *event.Event
		for event := range events {
			responseEvent = event
		}

		if responseEvent == nil {
			t.Fatal("No response event received")
		}

		if responseEvent.Author != "test-agent" {
			t.Fatalf("Expected author 'test-agent', got %q", responseEvent.Author)
		}

		if !responseEvent.HasText() {
			t.Fatal("Response event should have text content")
		}

		responseText := responseEvent.GetText()
		if responseText != mockModel.response {
			t.Fatalf("Expected response text %q, got %q", mockModel.response, responseText)
		}
	})

	// Test tool calling functionality
	t.Run("ToolCalling", func(t *testing.T) {
		// Create content with function call
		functionCall := &event.FunctionCall{
			Name: "calculator",
			Arguments: map[string]interface{}{
				"a":         2.0,
				"b":         3.0,
				"operation": "add",
			},
			ID: "call_123",
		}

		input := event.NewContentWithParts([]*event.Part{
			event.NewTextPart("Calculate 2 + 3"),
			{FunctionCall: functionCall},
		})

		events, err := agent.ProcessAsync(ctx, input)
		if err != nil {
			t.Fatalf("ProcessAsync failed: %v", err)
		}

		var toolResponseEvent *event.Event
		var finalResponseEvent *event.Event

		eventCount := 0
		for event := range events {
			eventCount++
			if event.HasFunctionCalls() {
				// This would be tool call confirmation
			} else if len(event.GetFunctionResponses()) > 0 {
				toolResponseEvent = event
			} else if event.HasText() {
				finalResponseEvent = event
			}
		}

		// Should have received tool response
		if toolResponseEvent == nil {
			t.Fatal("No tool response event received")
		}

		responses := toolResponseEvent.GetFunctionResponses()
		if len(responses) != 1 {
			t.Fatalf("Expected 1 function response, got %d", len(responses))
		}

		response := responses[0]
		if response.Name != "calculator" {
			t.Fatalf("Expected function response name 'calculator', got %q", response.Name)
		}

		if response.Result != 5.0 {
			t.Fatalf("Expected function response result 5.0, got %v", response.Result)
		}

		if response.ID != "call_123" {
			t.Fatalf("Expected function response ID 'call_123', got %q", response.ID)
		}

		// Should also have final response
		if finalResponseEvent == nil {
			t.Fatal("No final response event received")
		}

		if !finalResponseEvent.HasText() {
			t.Fatal("Final response should have text content")
		}
	})
}

func TestLLMAgentBasicResponse(t *testing.T) {
	ctx := context.Background()

	// Create mock model
	mockModel := &MockModel{
		response: "Hello! How can I help you today?",
	}

	// Create simple agent without tools
	config := LLMAgentConfig{
		Name:        "simple-agent",
		Description: "A simple test agent",
		Model:       mockModel,
	}

	agent, err := NewLLMAgent(config)
	if err != nil {
		t.Fatalf("Failed to create LLM agent: %v", err)
	}

	// Test Process method
	input := event.NewTextContent("Hello")
	response, err := agent.Process(ctx, input)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if response == nil {
		t.Fatal("Response is nil")
	}

	if !response.HasText() {
		t.Fatal("Response should have text content")
	}

	responseText := response.GetText()
	if responseText != mockModel.response {
		t.Fatalf("Expected response text %q, got %q", mockModel.response, responseText)
	}
}

func TestLLMAgentToolCall(t *testing.T) {
	ctx := context.Background()

	// Create calculator tool
	calculatorTool := NewMockCalculatorTool()

	// Create simple agent with tools (no model needed for tool execution)
	config := LLMAgentConfig{
		Name:        "tool-agent",
		Description: "An agent with tools",
		Model:       &MockModel{response: "Done"},
		Tools:       []tool.Tool{calculatorTool},
	}

	agent, err := NewLLMAgent(config)
	if err != nil {
		t.Fatalf("Failed to create LLM agent: %v", err)
	}

	// Create content with function call
	functionCall := &event.FunctionCall{
		Name: "calculator",
		Arguments: map[string]interface{}{
			"a":         10.0,
			"b":         5.0,
			"operation": "divide",
		},
		ID: "test_call",
	}

	input := event.NewContentWithParts([]*event.Part{
		{FunctionCall: functionCall},
	})

	events, err := agent.ProcessAsync(ctx, input)
	if err != nil {
		t.Fatalf("ProcessAsync failed: %v", err)
	}

	var toolResponseEvent *event.Event
	for event := range events {
		if len(event.GetFunctionResponses()) > 0 {
			toolResponseEvent = event
			break
		}
	}

	if toolResponseEvent == nil {
		t.Fatal("No tool response event received")
	}

	responses := toolResponseEvent.GetFunctionResponses()
	if len(responses) != 1 {
		t.Fatalf("Expected 1 function response, got %d", len(responses))
	}

	response := responses[0]
	if response.Name != "calculator" {
		t.Fatalf("Expected function response name 'calculator', got %q", response.Name)
	}

	if response.Result != 2.0 {
		t.Fatalf("Expected function response result 2.0, got %v", response.Result)
	}
}
