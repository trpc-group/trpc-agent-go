package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func TestNewMCPToolSet(t *testing.T) {
	config := MCPConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}

	toolset := NewMCPToolSet(config)
	if toolset == nil {
		t.Fatal("Expected toolset to be created")
	}

	// Test that default configuration is applied
	if toolset.config.retryConfig == nil {
		t.Error("Expected default retry config to be set")
	}

	// Clean up
	if err := toolset.Close(); err != nil {
		t.Errorf("Failed to close toolset: %v", err)
	}
}

func TestMCPToolSetWithOptions(t *testing.T) {
	config := MCPConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}

	retryConfig := RetryConfig{
		Enabled:     true,
		MaxAttempts: 5,
	}

	toolset := NewMCPToolSet(config,
		WithRetry(retryConfig),
		WithAutoRefresh(5*time.Minute),
	)

	if toolset == nil {
		t.Fatal("Expected toolset to be created")
	}

	// Test that custom configuration is applied
	if toolset.config.retryConfig.MaxAttempts != 5 {
		t.Errorf("Expected retry max attempts to be 5, got %d", toolset.config.retryConfig.MaxAttempts)
	}

	if toolset.config.autoRefresh != 5*time.Minute {
		t.Errorf("Expected auto refresh to be 5 minutes, got %v", toolset.config.autoRefresh)
	}

	// Clean up
	if err := toolset.Close(); err != nil {
		t.Errorf("Failed to close toolset: %v", err)
	}
}

func TestMCPToolSetBasicOperations(t *testing.T) {
	config := MCPConnectionConfig{
		Transport: "stdio",
		Command:   "echo", // This won't actually work as an MCP server, but we're testing the interface
		Args:      []string{"hello"},
		Timeout:   5 * time.Second,
	}
	toolset := NewMCPToolSet(config)
	defer toolset.Close()

	ctx := context.Background()

	// Test Tools() method - this will likely fail to connect but should return empty slice
	tools := toolset.Tools(ctx)
	if tools == nil {
		t.Error("Expected tools slice to be non-nil even when empty")
	}

	// Test IsConnected() method
	connected := toolset.IsConnected()
	if connected {
		t.Error("Expected not to be connected to echo command as MCP server")
	}

	// Test GetToolByName() method
	tool := toolset.GetToolByName(ctx, "nonexistent")
	if tool != nil {
		t.Error("Expected GetToolByName to return nil for nonexistent tool")
	}
}

func TestToolContext(t *testing.T) {
	ctx := context.Background()

	// Test WithToolContext and GetToolContext
	toolCtx := &ToolContext{
		AgentID:     "agent-123",
		SessionID:   "session-456",
		UserID:      "user-789",
		RequestID:   "req-abc",
		Permissions: []string{"read", "write"},
		Metadata: map[string]interface{}{
			"key": "value",
		},
	}

	ctxWithTool := WithToolContext(ctx, toolCtx)

	retrievedCtx, ok := GetToolContext(ctxWithTool)
	if !ok {
		t.Fatal("Expected to retrieve tool context")
	}

	if retrievedCtx.AgentID != toolCtx.AgentID {
		t.Errorf("Expected AgentID %s, got %s", toolCtx.AgentID, retrievedCtx.AgentID)
	}

	if retrievedCtx.SessionID != toolCtx.SessionID {
		t.Errorf("Expected SessionID %s, got %s", toolCtx.SessionID, retrievedCtx.SessionID)
	}

	// Test without tool context
	_, ok = GetToolContext(ctx)
	if ok {
		t.Error("Expected not to retrieve tool context from plain context")
	}
}

//func TestLogger(t *testing.T) {
//	// Test standard logger
//	logger := NewStandardLogger(LogLevelInfo, os.Stderr)
//	logger.Info("Test info message", "key", "value")
//	logger.Debug("Test debug message") // Should not appear with Info level
//	logger.Error("Test error message", "error", "test error")
//
//	// Test noop logger
//	noopLogger := NewNoopLogger()
//	noopLogger.Info("This should not appear anywhere")
//	noopLogger.Error("This should also not appear")
//}

func TestRetryConfig(t *testing.T) {
	config := RetryConfig{
		Enabled:       true,
		MaxAttempts:   3,
		InitialDelay:  100 * time.Millisecond,
		BackoffFactor: 2.0,
		MaxDelay:      5 * time.Second,
	}

	if !config.Enabled {
		t.Error("Expected retry to be enabled")
	}

	if config.MaxAttempts != 3 {
		t.Errorf("Expected max attempts to be 3, got %d", config.MaxAttempts)
	}
}

func TestMCPToolParameterProcessing(t *testing.T) {
	// Create a mock tool with schema using the correct MCP tool creation
	mcpToolData := *mcp.NewTool("test_tool",
		mcp.WithDescription("A test tool for parameter processing"),
		mcp.WithString("query", mcp.Description("The search query"), mcp.Required()),
		mcp.WithString("location", mcp.Description("The location to search in")),
		mcp.WithInteger("limit", mcp.Description("Maximum number of results")),
	)

	//logger := NewStandardLogger(LogLevelInfo, os.Stdout)
	sessionManager := &mcpSessionManager{}

	tool := newMCPTool(mcpToolData, sessionManager, nil)

	// Test Cases
	tests := []struct {
		name           string
		input          string
		contextData    map[string]interface{}
		expectedParams map[string]interface{}
		shouldError    bool
	}{
		{
			name:  "Direct parameter",
			input: `{"query": "test search"}`,
			expectedParams: map[string]interface{}{
				"query": "test search",
			},
			shouldError: false,
		},
		{
			name:  "Nested tool_input object",
			input: `{"tool_input": {"query": "nested search", "location": "Beijing"}}`,
			expectedParams: map[string]interface{}{
				"query":    "nested search",
				"location": "Beijing",
			},
			shouldError: false,
		},
		{
			name:  "Tool_input as JSON string",
			input: `{"tool_input": "{\"query\": \"json string search\", \"limit\": 10}"}`,
			expectedParams: map[string]interface{}{
				"query": "json string search",
				"limit": float64(10), // JSON numbers are float64
			},
			shouldError: false,
		},
		{
			name:  "Tool_input as direct string",
			input: `{"tool_input": "direct string search"}`,
			expectedParams: map[string]interface{}{
				"query": "direct string search", // Should infer "query" as primary parameter
			},
			shouldError: false,
		},
		{
			name:  "Single parameter",
			input: `{"query": "single param search"}`,
			expectedParams: map[string]interface{}{
				"query": "single param search",
			},
			shouldError: false,
		},
		{
			name:        "Missing required parameter",
			input:       `{"location": "Beijing"}`,
			shouldError: true,
		},
		{
			name:  "Type validation - integer as float",
			input: `{"query": "test", "limit": 5.0}`,
			expectedParams: map[string]interface{}{
				"query": "test",
				"limit": 5.0,
			},
			shouldError: false,
		},
		{
			name:        "Type validation - invalid integer",
			input:       `{"query": "test", "limit": 5.5}`,
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Add context data if provided
			if tt.contextData != nil {
				for key, value := range tt.contextData {
					ctx = context.WithValue(ctx, key, value)
				}
			}

			// Parse input
			var rawArgs map[string]interface{}
			err := json.Unmarshal([]byte(tt.input), &rawArgs)
			if err != nil {
				t.Fatalf("Failed to parse test input: %v", err)
			}

			// Test parameter normalization
			normalizedParams, err := tool.normalizeParameters(ctx, rawArgs)
			if tt.shouldError {
				// Continue to validation to see if that catches the error
			} else if err != nil {
				t.Fatalf("Parameter normalization failed: %v", err)
			}

			// Test parameter validation
			err = tool.validateParameters(normalizedParams)
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected validation error but got none")
				}
				return
			} else if err != nil {
				t.Fatalf("Parameter validation failed: %v", err)
			}

			// Verify normalized parameters match expected
			if !tt.shouldError {
				for key, expectedValue := range tt.expectedParams {
					actualValue, exists := normalizedParams[key]
					if !exists {
						t.Errorf("Expected parameter %s not found", key)
						continue
					}
					if actualValue != expectedValue {
						t.Errorf("Parameter %s: expected %v, got %v", key, expectedValue, actualValue)
					}
				}

				// Check no unexpected parameters
				for key := range normalizedParams {
					if key == "_context" {
						continue // Skip context parameter
					}
					if _, expected := tt.expectedParams[key]; !expected {
						t.Errorf("Unexpected parameter %s with value %v", key, normalizedParams[key])
					}
				}
			}
		})
	}
}

func TestMCPToolParameterInference(t *testing.T) {
	// Create a tool with location parameter using the correct MCP tool creation
	mcpToolData := *mcp.NewTool("weather_tool",
		mcp.WithDescription("Get weather information"),
		mcp.WithString("location", mcp.Description("The location to get weather for"), mcp.Required()),
	)
	sessionManager := &mcpSessionManager{}

	tool := newMCPTool(mcpToolData, sessionManager, nil)

	// Test context-based parameter inference
	tests := []struct {
		name             string
		input            string
		contextQuery     string
		expectedLocation string
	}{
		{
			name:             "Infer location from query",
			input:            `{}`,
			contextQuery:     "What's the weather in Beijing?",
			expectedLocation: "Beijing",
		},
		{
			name:             "Infer location with prefix",
			input:            `{}`,
			contextQuery:     "Can you tell me the weather near Shanghai today?",
			expectedLocation: "Shanghai",
		},
		{
			name:             "Infer capitalized location",
			input:            `{}`,
			contextQuery:     "I want to know about Tokyo weather conditions",
			expectedLocation: "Tokyo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), "user_query", tt.contextQuery)

			var rawArgs map[string]interface{}
			err := json.Unmarshal([]byte(tt.input), &rawArgs)
			if err != nil {
				t.Fatalf("Failed to parse test input: %v", err)
			}

			normalizedParams, err := tool.normalizeParameters(ctx, rawArgs)
			if err != nil {
				t.Fatalf("Parameter normalization failed: %v", err)
			}

			err = tool.validateParameters(normalizedParams)
			if err != nil {
				t.Fatalf("Parameter validation failed: %v", err)
			}

			location, exists := normalizedParams["location"]
			if !exists {
				t.Errorf("Expected location parameter to be inferred")
				return
			}

			if location != tt.expectedLocation {
				t.Errorf("Expected location %s, got %s", tt.expectedLocation, location)
			}
		})
	}
}

func TestToolFilters(t *testing.T) {
	ctx := context.Background()

	// Create test tools
	testTools := []MCPToolInfo{
		{Name: "echo", Description: "Echoes the input message"},
		{Name: "calculate", Description: "Performs mathematical calculations"},
		{Name: "time_current", Description: "Gets the current time"},
		{Name: "file_read", Description: "Reads a file from the system"},
		{Name: "system_info", Description: "Gets system information"},
		{Name: "basic_math", Description: "Basic math operations"},
	}

	t.Run("IncludeFilter", func(t *testing.T) {
		filter := NewIncludeFilter("echo", "calculate")
		filtered := filter.Filter(ctx, testTools)

		if len(filtered) != 2 {
			t.Errorf("Expected 2 tools, got %d", len(filtered))
		}

		names := make(map[string]bool)
		for _, tool := range filtered {
			names[tool.Name] = true
		}

		if !names["echo"] || !names["calculate"] {
			t.Error("Expected echo and calculate tools to be included")
		}
	})

	t.Run("ExcludeFilter", func(t *testing.T) {
		filter := NewExcludeFilter("file_read", "system_info")
		filtered := filter.Filter(ctx, testTools)

		if len(filtered) != 4 {
			t.Errorf("Expected 4 tools, got %d", len(filtered))
		}

		for _, tool := range filtered {
			if tool.Name == "file_read" || tool.Name == "system_info" {
				t.Error("file_read and system_info should be excluded")
			}
		}
	})

	t.Run("PatternIncludeFilter", func(t *testing.T) {
		filter := NewPatternIncludeFilter("^(echo|calc|time).*")
		filtered := filter.Filter(ctx, testTools)

		// Should match: echo, calculate, time_current
		if len(filtered) != 3 {
			t.Errorf("Expected 3 tools, got %d", len(filtered))
		}

		names := make(map[string]bool)
		for _, tool := range filtered {
			names[tool.Name] = true
		}

		expected := []string{"echo", "calculate", "time_current"}
		for _, name := range expected {
			if !names[name] {
				t.Errorf("Expected %s to be included", name)
			}
		}
	})

	t.Run("PatternExcludeFilter", func(t *testing.T) {
		filter := NewPatternExcludeFilter("^(file|system).*")
		filtered := filter.Filter(ctx, testTools)

		// Should exclude: file_read, system_info
		if len(filtered) != 4 {
			t.Errorf("Expected 4 tools, got %d", len(filtered))
		}

		for _, tool := range filtered {
			if strings.HasPrefix(tool.Name, "file") || strings.HasPrefix(tool.Name, "system") {
				t.Errorf("Tool %s should be excluded", tool.Name)
			}
		}
	})

	t.Run("DescriptionFilter", func(t *testing.T) {
		filter := NewDescriptionFilter(".*math.*")
		filtered := filter.Filter(ctx, testTools)

		// Should match: calculate, basic_math (both have "math" in description)
		if len(filtered) != 2 {
			t.Errorf("Expected 2 tools, got %d", len(filtered))
		}

		names := make(map[string]bool)
		for _, tool := range filtered {
			names[tool.Name] = true
		}

		if !names["calculate"] || !names["basic_math"] {
			t.Error("Expected calculate and basic_math tools to be included")
		}
	})

	t.Run("CompositeFilter", func(t *testing.T) {
		// Combine: include pattern + exclude specific tools
		includeFilter := NewPatternIncludeFilter(".*") // Include all
		excludeFilter := NewExcludeFilter("file_read", "system_info")

		composite := NewCompositeFilter(includeFilter, excludeFilter)
		filtered := composite.Filter(ctx, testTools)

		if len(filtered) != 4 {
			t.Errorf("Expected 4 tools, got %d", len(filtered))
		}

		for _, tool := range filtered {
			if tool.Name == "file_read" || tool.Name == "system_info" {
				t.Error("file_read and system_info should be excluded by composite filter")
			}
		}
	})

	t.Run("FuncFilter", func(t *testing.T) {
		// Custom function filter: only tools with names shorter than 8 characters
		filter := NewFuncFilter(func(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo {
			var filtered []MCPToolInfo
			for _, tool := range tools {
				if len(tool.Name) < 8 {
					filtered = append(filtered, tool)
				}
			}
			return filtered
		})

		filtered := filter.Filter(ctx, testTools)

		// Should match: echo (4), file_read (9 - excluded)
		// calculate (9 - excluded), time_current (12 - excluded), system_info (11 - excluded), basic_math (10 - excluded)
		expectedNames := []string{"echo"}
		if len(filtered) != len(expectedNames) {
			t.Errorf("Expected %d tools, got %d", len(expectedNames), len(filtered))
		}

		for _, tool := range filtered {
			if tool.Name != "echo" {
				t.Errorf("Only echo should pass the length filter, got %s", tool.Name)
			}
		}
	})

	t.Run("NoFilter", func(t *testing.T) {
		filtered := NoFilter.Filter(ctx, testTools)

		if len(filtered) != len(testTools) {
			t.Errorf("NoFilter should return all tools. Expected %d, got %d", len(testTools), len(filtered))
		}
	})

	t.Run("EmptyToolList", func(t *testing.T) {
		filter := NewIncludeFilter("echo")
		filtered := filter.Filter(ctx, []MCPToolInfo{})

		if len(filtered) != 0 {
			t.Errorf("Filter on empty list should return empty list, got %d tools", len(filtered))
		}
	})

	t.Run("EmptyFilterList", func(t *testing.T) {
		filter := NewIncludeFilter() // No tools specified
		filtered := filter.Filter(ctx, testTools)

		if len(filtered) != len(testTools) {
			t.Errorf("Empty include filter should return all tools. Expected %d, got %d", len(testTools), len(filtered))
		}
	})
}
