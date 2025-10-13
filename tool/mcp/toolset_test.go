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

	// Clean up
	if err := toolset.Close(); err != nil {
		t.Errorf("Failed to close toolset: %v", err)
	}
}

// getTestTools returns a slice of test tools for testing filters.
func getTestTools() []ToolInfo {
	return []ToolInfo{
		{Name: "echo", Description: "Echoes the input message"},
		{Name: "calculate", Description: "Performs mathematical calculations"},
		{Name: "time_current", Description: "Gets the current time"},
		{Name: "file_read", Description: "Reads a file from the system"},
		{Name: "system_info", Description: "Gets system information"},
		{Name: "basic_math", Description: "Basic math operations"},
	}
}

func TestIncludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

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
}

func TestExcludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

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
}

func TestPatternIncludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

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
}

func TestPatternExcludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

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
}

func TestDescriptionFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

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
}

func TestCompositeFilterWithPattern(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

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
}

func TestFuncFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	// Custom function filter: only tools with names shorter than 8 characters
	filter := NewFuncFilter(func(ctx context.Context, tools []ToolInfo) []ToolInfo {
		var filtered []ToolInfo
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
}

func TestNoFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filtered := NoFilter.Filter(ctx, testTools)

	if len(filtered) != len(testTools) {
		t.Errorf("NoFilter should return all tools. Expected %d, got %d", len(testTools), len(filtered))
	}
}

func TestEmptyToolList(t *testing.T) {
	ctx := context.Background()

	filter := NewIncludeFilter("echo")
	filtered := filter.Filter(ctx, []ToolInfo{})

	if len(filtered) != 0 {
		t.Errorf("Filter on empty list should return empty list, got %d tools", len(filtered))
	}
}

func TestEmptyFilterList(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filter := NewIncludeFilter() // No tools specified
	filtered := filter.Filter(ctx, testTools)

	if len(filtered) != len(testTools) {
		t.Errorf("Empty include filter should return all tools. Expected %d, got %d", len(testTools), len(filtered))
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
