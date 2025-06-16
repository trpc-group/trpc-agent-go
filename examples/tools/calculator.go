package tools

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// NewSimpleCalculatorTool creates a calculator tool using the BaseTool interface.
// This provides a clean calculator implementation with proper error handling.
func NewSimpleCalculatorTool() tool.BaseTool {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"description": "Mathematical operation to perform",
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

	return tool.NewFunctionTool("calculator", "Perform basic arithmetic operations", schema, executeCalculation)
}

func executeCalculation(ctx context.Context, input map[string]interface{}) (string, error) {
	// Extract parameters
	operation, ok := input["operation"].(string)
	if !ok {
		return "", fmt.Errorf("operation parameter is required and must be a string")
	}

	a, ok := input["a"].(float64)
	if !ok {
		return "", fmt.Errorf("parameter 'a' is required and must be a number")
	}

	b, ok := input["b"].(float64)
	if !ok {
		return "", fmt.Errorf("parameter 'b' is required and must be a number")
	}

	// Perform calculation
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
			return "", fmt.Errorf("division by zero is not allowed")
		}
		result = a / b
	default:
		return "", fmt.Errorf("unsupported operation: %s", operation)
	}

	return fmt.Sprintf("%.2f", result), nil
}
