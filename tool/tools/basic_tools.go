// Package tools provides implementations of common tools.
package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CalculatorTool is a tool for performing basic arithmetic operations.
type CalculatorTool struct {
	tool.BaseTool
}

// NewCalculatorTool creates a new calculator tool.
func NewCalculatorTool() *CalculatorTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "subtract", "multiply", "divide", "power"},
				"description": "The arithmetic operation to perform",
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "The first operand",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "The second operand",
			},
		},
		"required": []string{"operation", "a", "b"},
	}

	return &CalculatorTool{
		BaseTool: *tool.NewBaseTool(
			"calculator",
			"Performs basic arithmetic operations like add, subtract, multiply, divide, and power",
			parameters,
		),
	}
}

// Execute performs the arithmetic operation.
func (t *CalculatorTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract and validate the operation
	operation, ok := args["operation"].(string)
	if !ok {
		return nil, errors.New("operation must be a string")
	}

	// Extract and validate operands
	a, err := getNumberArg(args, "a")
	if err != nil {
		return nil, err
	}

	b, err := getNumberArg(args, "b")
	if err != nil {
		return nil, err
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
			return nil, errors.New("division by zero")
		}
		result = a / b
	case "power":
		result = math.Pow(a, b)
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	// Format result as string if it's an integer
	var output interface{} = result
	if result == math.Floor(result) {
		output = strconv.FormatFloat(result, 'f', 0, 64)
	}

	return tool.NewResult(output), nil
}

// getNumberArg extracts a number argument from the args map.
func getNumberArg(args map[string]interface{}, name string) (float64, error) {
	arg, ok := args[name]
	if !ok {
		return 0, fmt.Errorf("%s is required", name)
	}

	// Handle float64
	if val, ok := arg.(float64); ok {
		return val, nil
	}

	// Handle int
	if val, ok := arg.(int); ok {
		return float64(val), nil
	}

	// Handle string (try to parse)
	if val, ok := arg.(string); ok {
		num, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a number, got string '%s'", name, val)
		}
		return num, nil
	}

	return 0, fmt.Errorf("%s must be a number", name)
}

// HTTPClientTool is a tool for making HTTP requests.
type HTTPClientTool struct {
	tool.BaseTool
	client *http.Client
}

// NewHTTPClientTool creates a new HTTP client tool.
func NewHTTPClientTool() *HTTPClientTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "The URL to request",
			},
			"method": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD"},
				"description": "The HTTP method to use",
				"default":     "GET",
			},
			"headers": map[string]interface{}{
				"type":        "object",
				"description": "HTTP headers to include in the request",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "The body of the request (for POST, PUT, PATCH)",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Timeout in seconds",
				"default":     30,
			},
		},
		"required": []string{"url"},
	}

	return &HTTPClientTool{
		BaseTool: *tool.NewBaseTool(
			"http_client",
			"Makes HTTP requests to specified URLs",
			parameters,
		),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Execute makes an HTTP request.
func (t *HTTPClientTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract URL (required)
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return nil, errors.New("url is required and must be a string")
	}

	// Extract method (optional, default to GET)
	method, ok := args["method"].(string)
	if !ok || method == "" {
		method = "GET"
	}

	// Extract body (optional)
	body, _ := args["body"].(string)

	// Create request
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers if provided
	if headersObj, ok := args["headers"].(map[string]interface{}); ok {
		for key, val := range headersObj {
			if strVal, ok := val.(string); ok {
				req.Header.Add(key, strVal)
			}
		}
	}

	// Extract timeout (optional)
	timeout, _ := args["timeout"].(float64)
	if timeout > 0 {
		client := *t.client
		client.Timeout = time.Duration(timeout) * time.Second
		t.client = &client
	}

	// Execute request
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Create result
	result := tool.NewResult(string(responseBody))
	result.Metadata = map[string]interface{}{
		"status_code":    resp.StatusCode,
		"status":         resp.Status,
		"headers":        resp.Header,
		"content_length": resp.ContentLength,
		"content_type":   resp.Header.Get("Content-Type"),
	}

	// Set content type
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		result.ContentType = contentType
	}

	return result, nil
}

// RegisterBasicTools registers all basic tools with the default registry.
func RegisterBasicTools() {
	// Register calculator tool
	_ = tool.DefaultRegistry.Register(NewCalculatorTool())

	// Register HTTP client tool
	_ = tool.DefaultRegistry.Register(NewHTTPClientTool())
	
	// Register final answer tool
	_ = tool.DefaultRegistry.Register(NewFinalAnswerTool())
}
