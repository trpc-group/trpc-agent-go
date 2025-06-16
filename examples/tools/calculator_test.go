package tools

import (
	"context"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// TestBaseToolInterface tests the basic BaseTool interface.
func TestBaseToolInterface(t *testing.T) {
	calc := NewSimpleCalculatorTool()

	// Test Name()
	name := calc.Name()
	if name != "calculator" {
		t.Errorf("Expected name 'calculator', got '%s'", name)
	}

	// Test Description()
	description := calc.Description()
	if description == "" {
		t.Error("Expected non-empty description")
	}

	// Test InputSchema()
	schema := calc.InputSchema()
	if schema == nil {
		t.Error("Expected non-nil schema")
	}

	// Check schema structure
	if schema["type"] != "object" {
		t.Errorf("Expected schema type 'object', got '%v'", schema["type"])
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Error("Expected properties to be map[string]interface{}")
	}

	// Check required parameters
	required, ok := schema["required"].([]string)
	if !ok {
		t.Error("Expected required to be []string")
	}
	if len(required) != 3 {
		t.Errorf("Expected 3 required parameters, got %d", len(required))
	}

	// Check specific properties exist
	if _, exists := properties["operation"]; !exists {
		t.Error("Expected 'operation' property")
	}
	if _, exists := properties["a"]; !exists {
		t.Error("Expected 'a' property")
	}
	if _, exists := properties["b"]; !exists {
		t.Error("Expected 'b' property")
	}
}

// TestCalculatorTool tests the calculator tool functionality.
func TestCalculatorTool(t *testing.T) {
	calc := NewSimpleCalculatorTool()
	ctx := context.Background()

	tests := []struct {
		name     string
		input    map[string]interface{}
		expected string
		hasError bool
	}{
		{
			name: "addition",
			input: map[string]interface{}{
				"operation": "add",
				"a":         5.0,
				"b":         3.0,
			},
			expected: "8.00",
			hasError: false,
		},
		{
			name: "subtraction",
			input: map[string]interface{}{
				"operation": "subtract",
				"a":         10.0,
				"b":         3.0,
			},
			expected: "7.00",
			hasError: false,
		},
		{
			name: "multiplication",
			input: map[string]interface{}{
				"operation": "multiply",
				"a":         4.0,
				"b":         6.0,
			},
			expected: "24.00",
			hasError: false,
		},
		{
			name: "division",
			input: map[string]interface{}{
				"operation": "divide",
				"a":         15.0,
				"b":         3.0,
			},
			expected: "5.00",
			hasError: false,
		},
		{
			name: "division by zero",
			input: map[string]interface{}{
				"operation": "divide",
				"a":         15.0,
				"b":         0.0,
			},
			expected: "",
			hasError: true,
		},
		{
			name: "invalid operation",
			input: map[string]interface{}{
				"operation": "invalid",
				"a":         15.0,
				"b":         3.0,
			},
			expected: "",
			hasError: true,
		},
		{
			name: "missing operation",
			input: map[string]interface{}{
				"a": 15.0,
				"b": 3.0,
			},
			expected: "",
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := calc.Run(ctx, tt.input)

			if tt.hasError {
				if err == nil {
					t.Errorf("Expected error for test '%s', but got none", tt.name)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for test '%s': %v", tt.name, err)
				}
				if result != tt.expected {
					t.Errorf("Expected result '%s' for test '%s', got '%s'", tt.expected, tt.name, result)
				}
			}
		})
	}
}

// TestFunctionTool tests the FunctionTool implementation.
func TestFunctionTool(t *testing.T) {
	// Create a simple test function
	testFunc := func(ctx context.Context, input map[string]interface{}) (string, error) {
		name, ok := input["name"].(string)
		if !ok {
			return "", fmt.Errorf("name parameter required")
		}
		return "Hello, " + name + "!", nil
	}

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Name to greet",
			},
		},
		"required": []string{"name"},
	}

	toolImpl := tool.NewFunctionTool("greeter", "A greeting tool", schema, testFunc)

	// Test interface implementation
	if toolImpl.Name() != "greeter" {
		t.Errorf("Expected name 'greeter', got '%s'", toolImpl.Name())
	}

	if toolImpl.Description() != "A greeting tool" {
		t.Errorf("Expected description 'A greeting tool', got '%s'", toolImpl.Description())
	}

	if toolImpl.InputSchema() == nil {
		t.Error("Expected non-nil schema")
	}

	// Test execution
	ctx := context.Background()
	result, err := toolImpl.Run(ctx, map[string]interface{}{
		"name": "World",
	})

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	expected := "Hello, World!"
	if result != expected {
		t.Errorf("Expected result '%s', got '%s'", expected, result)
	}
}
