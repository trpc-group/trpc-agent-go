package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCalculatorTool_Execute(t *testing.T) {
	calcTool := NewCalculatorTool()

	testCases := []struct {
		name        string
		operation   string
		a           float64
		b           float64
		expected    float64
		expectError bool
		errorMsg    string
	}{
		{
			name:      "Valid addition",
			operation: "add",
			a:         2,
			b:         2,
			expected:  4,
		},
		{
			name:      "Valid subtraction",
			operation: "subtract",
			a:         5,
			b:         3,
			expected:  2,
		},
		{
			name:      "Valid multiplication",
			operation: "multiply",
			a:         3,
			b:         7,
			expected:  21,
		},
		{
			name:      "Valid division",
			operation: "divide",
			a:         10,
			b:         2,
			expected:  5,
		},
		{
			name:      "Valid power",
			operation: "power",
			a:         2,
			b:         3,
			expected:  8,
		},
		{
			name:        "Invalid operation",
			operation:   "invalid",
			a:           2,
			b:           2,
			expectError: true,
			errorMsg:    "unsupported operation",
		},
		{
			name:        "Division by zero",
			operation:   "divide",
			a:           10,
			b:           0,
			expectError: true,
			errorMsg:    "division by zero",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{
				"operation": tc.operation,
				"a":         tc.a,
				"b":         tc.b,
			}
			result, err := calcTool.Execute(context.Background(), input)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error for operation '%s', but got nil", tc.operation)
				} else if tc.errorMsg != "" && !strings.Contains(strings.ToLower(err.Error()), tc.errorMsg) {
					t.Errorf("Expected error message to contain '%s', but got '%s'", tc.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error for operation '%s': %v", tc.operation, err)
				return
			}

			if result == nil {
				t.Fatal("Expected result, but got nil")
			}
			outputData := result.Output

			var resultVal float64
			switch v := outputData.(type) {
			case float64:
				resultVal = v
			case string:
				var err error
				resultVal, err = strconv.ParseFloat(v, 64)
				if err != nil {
					t.Errorf("Expected result data to be convertible to float64, got %T: %v", outputData, outputData)
					return
				}
			default:
				t.Errorf("Expected result data to be float64 or string, got %T", outputData)
				return
			}

			if resultVal != tc.expected {
				t.Errorf("Expected result %f, got %f", tc.expected, resultVal)
			}
		})
	}
}

func TestCalculatorTool_Schema(t *testing.T) {
	calcTool := NewCalculatorTool()
	var iTool tool.Tool = calcTool

	name := iTool.Name()
	description := iTool.Description()
	parameters := iTool.Parameters()

	if name != "calculator" {
		t.Errorf("Expected schema name 'calculator', got '%s'", name)
	}
	if description != "Performs basic arithmetic operations like add, subtract, multiply, divide, and power" {
		t.Errorf("Unexpected schema description: %s", description)
	}

	props, ok := parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected parameters to have properties")
	}
	if _, ok := props["operation"]; !ok {
		t.Error("Expected 'operation' property in schema parameters")
	}
}

func TestHTTPClientTool_Execute_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected method GET, got %s", r.Method)
		}
		if r.URL.Path != "/test" {
			t.Errorf("Expected path /test, got %s", r.URL.Path)
		}
		if r.Header.Get("X-Custom-Header") != "custom_value" {
			t.Errorf("Expected X-Custom-Header 'custom_value', got '%s'", r.Header.Get("X-Custom-Header"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"key": "value", "number": 123}`)
	}))
	defer server.Close()

	httpClientTool := NewHTTPClientTool()
	input := map[string]any{
		"method": "GET",
		"url":    server.URL + "/test",
		"headers": map[string]any{
			"X-Custom-Header": "custom_value",
		},
	}

	result, err := httpClientTool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, but got nil")
	}

	outputData := result.Output
	var data map[string]any
	err = json.Unmarshal([]byte(outputData.(string)), &data)
	if err != nil {
		t.Fatalf("Failed to unmarshal result data: %v", err)
	}

	if val, ok := data["key"].(string); !ok || val != "value" {
		t.Errorf("Expected key 'value', got '%v'", data["key"])
	}
	if val, ok := data["number"].(float64); !ok || val != 123 { // JSON numbers are float64
		t.Errorf("Expected number 123, got '%v'", data["number"])
	}
}

func TestHTTPClientTool_Execute_Error(t *testing.T) {
	// Create a test server for controlled error responses
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/unsupported_method" && r.Method == "INVALID" {
			http.Error(w, "Unsupported method", http.StatusMethodNotAllowed)
			return
		}
		// Normal 200 response for other cases
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	httpClientTool := NewHTTPClientTool()

	// Test case: Invalid URL (malformed)
	inputInvalidURL := map[string]any{
		"method": "GET",
		"url":    "http://invalid.url.that.definitely.doesnt.exist",
	}
	_, err := httpClientTool.Execute(context.Background(), inputInvalidURL)
	if err == nil {
		t.Error("Expected an error for invalid URL, but got nil")
	}

	// Test case: Missing URL
	inputMissingURL := map[string]any{
		"method": "GET",
	}
	_, err = httpClientTool.Execute(context.Background(), inputMissingURL)
	if err == nil {
		t.Error("Expected an error for missing URL, but got nil")
	} else if !strings.Contains(err.Error(), "url is required") {
		t.Errorf("Expected error message for missing URL, got: %s", err.Error())
	}

	// Test case: Missing method (should default to GET)
	inputMissingMethod := map[string]any{
		"url": server.URL,
	}
	result, err := httpClientTool.Execute(context.Background(), inputMissingMethod)
	if err != nil {
		t.Errorf("Unexpected error when method is missing (should default): %v", err)
	}
	if result == nil {
		t.Error("Expected result when method is missing, got nil")
	}
}

func TestHTTPClientTool_Execute_PostSuccess(t *testing.T) {
	expectedBody := `{"data_key":"data_value"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected method POST, got %s", r.Method)
		}
		if r.URL.Path != "/submit" {
			t.Errorf("Expected path /submit, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type 'application/json', got '%s'", r.Header.Get("Content-Type"))
		}

		bodyBytes, _ := io.ReadAll(r.Body)
		defer r.Body.Close()
		if string(bodyBytes) != expectedBody {
			t.Errorf("Expected body '%s', got '%s'", expectedBody, string(bodyBytes))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintln(w, `{"status": "created", "id": "123"}`)
	}))
	defer server.Close()

	httpClientTool := NewHTTPClientTool()
	input := map[string]any{
		"method": "POST",
		"url":    server.URL + "/submit",
		"headers": map[string]any{
			"Content-Type": "application/json",
		},
		"body": expectedBody,
	}

	result, err := httpClientTool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	outputData := result.Output
	var data map[string]any
	err = json.Unmarshal([]byte(outputData.(string)), &data)
	if err != nil {
		t.Fatalf("Failed to unmarshal result data: %v", err)
	}

	if val, ok := data["status"].(string); !ok || val != "created" {
		t.Errorf("Expected status 'created', got '%v'", data["status"])
	}
	if val, ok := data["id"].(string); !ok || val != "123" {
		t.Errorf("Expected id '123', got '%v'", data["id"])
	}
}

func TestHTTPClientTool_Schema(t *testing.T) {
	clientTool := NewHTTPClientTool()
	var iTool tool.Tool = clientTool

	name := iTool.Name()
	parameters := iTool.Parameters()

	if name != "http_client" {
		t.Errorf("Expected schema name 'http_client', got '%s'", name)
	}

	props, ok := parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected parameters to have properties")
	}
	if _, ok := props["url"]; !ok {
		t.Error("Expected 'url' property in schema parameters")
	}
	if _, ok := props["method"]; !ok {
		t.Error("Expected 'method' property in schema parameters")
	}
}

func TestHTTPClientTool_Execute_NetworkError(t *testing.T) {
	httpClientTool := NewHTTPClientTool()
	input := map[string]any{
		"method": "GET",
		"url":    "http://localhost:12345/nonexistent",
	}

	_, err := httpClientTool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("Expected a network error, but got nil")
	}
	t.Logf("Received expected network error: %v", err)
}
