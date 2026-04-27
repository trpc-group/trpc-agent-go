//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestNewMCPToolSet(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}

	toolset := NewMCPToolSet(config)
	if toolset == nil {
		t.Fatal("Expected toolset to be created")
	}

	// Test default name
	if toolset.Name() != "mcp" {
		t.Errorf("Expected default name 'mcp', got %q", toolset.Name())
	}

	// Clean up
	if err := toolset.Close(); err != nil {
		t.Errorf("Failed to close toolset: %v", err)
	}
}

func TestNewMCPToolSet_WithOptions(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}

	filterFunc := tool.NewIncludeToolNamesFilter("tool1", "tool2")
	toolset := NewMCPToolSet(config,
		WithName("test-toolset"),
		WithToolFilterFunc(filterFunc),
		WithSessionReconnect(3),
	)

	if toolset == nil {
		t.Fatal("Expected toolset to be created")
	}

	if toolset.Name() != "test-toolset" {
		t.Errorf("Expected name 'test-toolset', got %q", toolset.Name())
	}

	// Clean up
	if err := toolset.Close(); err != nil {
		t.Errorf("Failed to close toolset: %v", err)
	}
}

// getTestTools returns a slice of test tools for testing filters.
func getTestTools() []tool.Tool {
	// Use function tools for testing
	echoFunc := func(ctx context.Context, msg string) (string, error) { return msg, nil }
	calcFunc := func(ctx context.Context, args struct {
		A float64 `json:"a"`
		B float64 `json:"b"`
	}) (float64, error) {
		return args.A + args.B, nil
	}
	timeFunc := func(ctx context.Context, args struct{}) (string, error) { return "now", nil }
	fileFunc := func(ctx context.Context, path string) (string, error) { return "", nil }
	sysFunc := func(ctx context.Context, args struct{}) (string, error) { return "", nil }
	mathFunc := func(ctx context.Context, x float64) (float64, error) { return x * 2, nil }

	return []tool.Tool{
		function.NewFunctionTool(echoFunc, function.WithName("echo"), function.WithDescription("Echoes the input message")),
		function.NewFunctionTool(calcFunc, function.WithName("calculate"), function.WithDescription("Performs mathematical calculations")),
		function.NewFunctionTool(timeFunc, function.WithName("time_current"), function.WithDescription("Gets the current time")),
		function.NewFunctionTool(fileFunc, function.WithName("file_read"), function.WithDescription("Reads a file from the system")),
		function.NewFunctionTool(sysFunc, function.WithName("system_info"), function.WithDescription("Gets system information")),
		function.NewFunctionTool(mathFunc, function.WithName("basic_math"), function.WithDescription("Basic math operations")),
	}
}

func TestIncludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filterFunc := tool.NewIncludeToolNamesFilter("echo", "calculate")
	filtered := tool.FilterTools(ctx, testTools, filterFunc)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, tl := range filtered {
		decl := tl.Declaration()
		if decl != nil {
			names[decl.Name] = true
		}
	}

	if !names["echo"] || !names["calculate"] {
		t.Error("Expected echo and calculate tools to be included")
	}
}

func TestExcludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filterFunc := tool.NewExcludeToolNamesFilter("file_read", "system_info")
	filtered := tool.FilterTools(ctx, testTools, filterFunc)

	if len(filtered) != 4 {
		t.Errorf("Expected 4 tools, got %d", len(filtered))
	}

	for _, tl := range filtered {
		decl := tl.Declaration()
		if decl != nil && (decl.Name == "file_read" || decl.Name == "system_info") {
			t.Error("file_read and system_info should be excluded")
		}
	}
}

func TestPatternIncludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	// Custom filter func for pattern matching
	filterFunc := func(ctx context.Context, t tool.Tool) bool {
		decl := t.Declaration()
		if decl == nil {
			return false
		}
		// Match: echo, calculate (calc*), time_current (time*)
		return strings.HasPrefix(decl.Name, "echo") ||
			strings.HasPrefix(decl.Name, "calc") ||
			strings.HasPrefix(decl.Name, "time")
	}
	filtered := tool.FilterTools(ctx, testTools, filterFunc)

	// Should match: echo, calculate, time_current
	if len(filtered) != 3 {
		t.Errorf("Expected 3 tools, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, tl := range filtered {
		decl := tl.Declaration()
		if decl != nil {
			names[decl.Name] = true
		}
	}

	expected := []string{"echo", "calculate", "time_current"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("Expected %s to be included", name)
		}
	}
}

func TestPatternExcludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	// Custom filter func for pattern exclusion
	filterFunc := func(ctx context.Context, t tool.Tool) bool {
		decl := t.Declaration()
		if decl == nil {
			return false
		}
		// Exclude: file*, system*
		return !strings.HasPrefix(decl.Name, "file") &&
			!strings.HasPrefix(decl.Name, "system")
	}
	filtered := tool.FilterTools(ctx, testTools, filterFunc)

	// Should exclude: file_read, system_info
	if len(filtered) != 4 {
		t.Errorf("Expected 4 tools, got %d", len(filtered))
	}

	for _, tl := range filtered {
		decl := tl.Declaration()
		if decl != nil && (strings.HasPrefix(decl.Name, "file") || strings.HasPrefix(decl.Name, "system")) {
			t.Errorf("Tool %s should be excluded", decl.Name)
		}
	}
}

func TestCustomFilterFunc(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	// Custom filter function: only tools with names shorter than 8 characters
	filterFunc := func(ctx context.Context, t tool.Tool) bool {
		decl := t.Declaration()
		if decl == nil {
			return false
		}
		return len(decl.Name) < 8
	}

	filtered := tool.FilterTools(ctx, testTools, filterFunc)

	// Should match: echo (4)
	expectedNames := []string{"echo"}
	if len(filtered) != len(expectedNames) {
		t.Errorf("Expected %d tools, got %d", len(expectedNames), len(filtered))
	}

	for _, tl := range filtered {
		decl := tl.Declaration()
		if decl != nil && decl.Name != "echo" {
			t.Errorf("Only echo should pass the length filter, got %s", decl.Name)
		}
	}
}

func TestEmptyToolList(t *testing.T) {
	ctx := context.Background()

	filterFunc := tool.NewIncludeToolNamesFilter("echo")
	filtered := tool.FilterTools(ctx, []tool.Tool{}, filterFunc)

	if len(filtered) != 0 {
		t.Errorf("Filter on empty list should return empty list, got %d tools", len(filtered))
	}
}

func TestEmptyFilterList(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	// When no filter is specified, FilterTools should return all tools
	// Note: NewIncludeToolNamesFilter() with no args creates an empty allowlist,
	// which filters out everything. This is the expected behavior.
	filterFunc := tool.NewIncludeToolNamesFilter() // No tools specified - empty allowlist
	filtered := tool.FilterTools(ctx, testTools, filterFunc)

	// Empty include filter (no allowed names) should return no tools
	if len(filtered) != 0 {
		t.Errorf("Empty include filter should return no tools. Expected 0, got %d", len(filtered))
	}
}

// TestTimeoutContextCreation tests the createTimeoutContext method
func TestTimeoutContextCreation(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
		Timeout:   2 * time.Second,
	}

	manager := newMCPSessionManager(config, nil, nil)

	t.Run("adds timeout when context has no deadline", func(t *testing.T) {
		ctx := context.Background() // No deadline
		timeoutCtx, cancel := manager.createTimeoutContext(ctx, "test")
		defer cancel()

		deadline, hasDeadline := timeoutCtx.Deadline()
		if !hasDeadline {
			t.Error("Expected context to have deadline when timeout is configured")
		}

		// Check that deadline is approximately 2 seconds from now
		expectedDeadline := time.Now().Add(2 * time.Second)
		if deadline.Before(expectedDeadline.Add(-100*time.Millisecond)) ||
			deadline.After(expectedDeadline.Add(100*time.Millisecond)) {
			t.Errorf("Deadline not within expected range. Got: %v, Expected around: %v", deadline, expectedDeadline)
		}
	})

	t.Run("preserves existing deadline", func(t *testing.T) {
		originalDeadline := time.Now().Add(5 * time.Second)
		ctx, cancel := context.WithDeadline(context.Background(), originalDeadline)
		defer cancel()

		timeoutCtx, timeoutCancel := manager.createTimeoutContext(ctx, "test")
		defer timeoutCancel()

		deadline, hasDeadline := timeoutCtx.Deadline()
		if !hasDeadline {
			t.Error("Expected context to preserve existing deadline")
		}

		if !deadline.Equal(originalDeadline) {
			t.Errorf("Expected deadline to be preserved. Got: %v, Expected: %v", deadline, originalDeadline)
		}
	})

	t.Run("no timeout when not configured", func(t *testing.T) {
		configNoTimeout := ConnectionConfig{
			Transport: "stdio",
			Command:   "echo",
			Args:      []string{"hello"},
			// No Timeout specified
		}
		managerNoTimeout := newMCPSessionManager(configNoTimeout, nil, nil)

		ctx := context.Background()
		timeoutCtx, cancel := managerNoTimeout.createTimeoutContext(ctx, "test")
		defer cancel()

		_, hasDeadline := timeoutCtx.Deadline()
		if hasDeadline {
			t.Error("Expected no deadline when timeout is not configured")
		}

		// Should return the same context
		if timeoutCtx != ctx {
			t.Error("Expected same context to be returned when no timeout is configured")
		}
	})
}

// TestWithSessionReconnect tests the WithSessionReconnect option
func TestWithSessionReconnect(t *testing.T) {
	tests := []struct {
		name           string
		maxAttempts    int
		expectedConfig *SessionReconnectConfig
	}{
		{
			name:        "valid attempts within range",
			maxAttempts: 3,
			expectedConfig: &SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 3,
			},
		},
		{
			name:        "attempts below minimum - clamped to 1",
			maxAttempts: 0,
			expectedConfig: &SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 1,
			},
		},
		{
			name:        "attempts above maximum - clamped to 10",
			maxAttempts: 15,
			expectedConfig: &SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 10,
			},
		},
		{
			name:        "negative attempts - clamped to minimum",
			maxAttempts: -5,
			expectedConfig: &SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &toolSetConfig{}
			opt := WithSessionReconnect(tt.maxAttempts)
			opt(cfg)

			if cfg.sessionReconnectConfig == nil {
				t.Fatal("Expected sessionReconnectConfig to be set")
			}

			if cfg.sessionReconnectConfig.EnableAutoReconnect != tt.expectedConfig.EnableAutoReconnect {
				t.Errorf("Expected EnableAutoReconnect=%v, got %v",
					tt.expectedConfig.EnableAutoReconnect,
					cfg.sessionReconnectConfig.EnableAutoReconnect)
			}

			if cfg.sessionReconnectConfig.MaxReconnectAttempts != tt.expectedConfig.MaxReconnectAttempts {
				t.Errorf("Expected MaxReconnectAttempts=%d, got %d",
					tt.expectedConfig.MaxReconnectAttempts,
					cfg.sessionReconnectConfig.MaxReconnectAttempts)
			}
		})
	}
}

// TestWithSessionReconnectConfig tests the WithSessionReconnectConfig option
func TestWithSessionReconnectConfig(t *testing.T) {
	tests := []struct {
		name           string
		inputConfig    SessionReconnectConfig
		expectedConfig SessionReconnectConfig
	}{
		{
			name: "valid config",
			inputConfig: SessionReconnectConfig{
				EnableAutoReconnect:  false, // Will be forced to true
				MaxReconnectAttempts: 5,
			},
			expectedConfig: SessionReconnectConfig{
				EnableAutoReconnect:  true, // Always enabled
				MaxReconnectAttempts: 5,
			},
		},
		{
			name: "attempts below minimum - clamped",
			inputConfig: SessionReconnectConfig{
				MaxReconnectAttempts: 0,
			},
			expectedConfig: SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 1,
			},
		},
		{
			name: "attempts above maximum - clamped",
			inputConfig: SessionReconnectConfig{
				MaxReconnectAttempts: 20,
			},
			expectedConfig: SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &toolSetConfig{}
			opt := WithSessionReconnectConfig(tt.inputConfig)
			opt(cfg)

			if cfg.sessionReconnectConfig == nil {
				t.Fatal("Expected sessionReconnectConfig to be set")
			}

			if cfg.sessionReconnectConfig.EnableAutoReconnect != tt.expectedConfig.EnableAutoReconnect {
				t.Errorf("Expected EnableAutoReconnect=%v, got %v",
					tt.expectedConfig.EnableAutoReconnect,
					cfg.sessionReconnectConfig.EnableAutoReconnect)
			}

			if cfg.sessionReconnectConfig.MaxReconnectAttempts != tt.expectedConfig.MaxReconnectAttempts {
				t.Errorf("Expected MaxReconnectAttempts=%d, got %d",
					tt.expectedConfig.MaxReconnectAttempts,
					cfg.sessionReconnectConfig.MaxReconnectAttempts)
			}
		})
	}
}

// TestShouldAttemptSessionReconnect tests error pattern matching for session reconnection
func TestShouldAttemptSessionReconnect(t *testing.T) {
	tests := []struct {
		name        string
		errorMsg    string
		shouldRetry bool
		description string
	}{
		// Should trigger reconnection
		{
			name:        "session_expired prefix",
			errorMsg:    "session_expired: session has expired",
			shouldRetry: true,
		},
		{
			name:        "transport is closed",
			errorMsg:    "transport is closed",
			shouldRetry: true,
		},
		{
			name:        "client not initialized",
			errorMsg:    "client not initialized",
			shouldRetry: true,
		},
		{
			name:        "not initialized",
			errorMsg:    "not initialized",
			shouldRetry: true,
		},
		{
			name:        "connection refused",
			errorMsg:    "dial tcp: connection refused",
			shouldRetry: true,
		},
		{
			name:        "connection reset",
			errorMsg:    "read tcp: connection reset by peer",
			shouldRetry: true,
		},
		{
			name:        "EOF error",
			errorMsg:    "unexpected EOF",
			shouldRetry: true,
		},
		{
			name:        "broken pipe",
			errorMsg:    "write: broken pipe",
			shouldRetry: true,
		},
		{
			name:        "HTTP 404",
			errorMsg:    "HTTP 404: session not found",
			shouldRetry: true,
		},
		{
			name:        "session not found",
			errorMsg:    "error: session not found on server",
			shouldRetry: true,
		},

		// Should NOT trigger reconnection (conservative approach)
		{
			name:        "DNS resolution failure",
			errorMsg:    "no such host",
			shouldRetry: false,
			description: "DNS failures indicate configuration errors",
		},
		{
			name:        "I/O timeout",
			errorMsg:    "i/o timeout",
			shouldRetry: false,
			description: "Timeouts may indicate performance issues, not disconnection",
		},
		{
			name:        "authentication error",
			errorMsg:    "authentication failed",
			shouldRetry: false,
			description: "Auth errors are not connection issues",
		},
		{
			name:        "bad request",
			errorMsg:    "bad request: invalid parameters",
			shouldRetry: false,
			description: "Client errors should not trigger reconnection",
		},
		{
			name:        "nil error",
			errorMsg:    "",
			shouldRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build a manager with auto-reconnect enabled to exercise the real method
			mgr := &mcpSessionManager{
				sessionReconnectConfig: &SessionReconnectConfig{
					EnableAutoReconnect:  true,
					MaxReconnectAttempts: 3,
				},
			}
			var err error
			if tt.errorMsg != "" {
				err = fmt.Errorf(tt.errorMsg)
			}
			result := mgr.shouldAttemptSessionReconnect(err)
			if result != tt.shouldRetry {
				if tt.description != "" {
					t.Errorf("Error '%s': expected shouldRetry=%v, got %v (%s)",
						tt.errorMsg, tt.shouldRetry, result, tt.description)
				} else {
					t.Errorf("Error '%s': expected shouldRetry=%v, got %v",
						tt.errorMsg, tt.shouldRetry, result)
				}
			}
		})
	}
}
