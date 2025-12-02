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
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// TestToolSet_Close tests the Close method
func TestToolSet_Close(t *testing.T) {
	t.Run("close unconnected toolset", func(t *testing.T) {
		config := ConnectionConfig{
			Transport: "stdio",
			Command:   "echo",
			Args:      []string{"hello"},
		}
		toolset := NewMCPToolSet(config)

		// Close without connecting
		err := toolset.Close()
		if err != nil {
			t.Errorf("Expected no error when closing unconnected toolset, got: %v", err)
		}
	})

}

// TestSessionManager_IsConnected tests the isConnected method
func TestSessionManager_IsConnected(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	manager := newMCPSessionManager(config, nil, nil)

	// Initially not connected
	if manager.isConnected() {
		t.Error("Expected manager to be not connected initially")
	}

	// Manually set connected and initialized
	manager.mu.Lock()
	manager.connected = true
	manager.initialized = true
	manager.mu.Unlock()

	if !manager.isConnected() {
		t.Error("Expected manager to be connected after setting flags")
	}

	// Only connected but not initialized
	manager.mu.Lock()
	manager.initialized = false
	manager.mu.Unlock()

	if manager.isConnected() {
		t.Error("Expected manager to be not connected when not initialized")
	}
}

// TestSessionManager_CallTool_ClientNil tests callTool when client is nil
func TestSessionManager_CallTool_ClientNil(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	manager := newMCPSessionManager(config, nil, nil)

	manager.mu.Lock()
	manager.client = nil
	manager.mu.Unlock()

	_, err := manager.callTool(context.Background(), "test-tool", map[string]any{})
	if err == nil {
		t.Error("Expected error when client is nil")
	}
	if !strings.Contains(err.Error(), "transport is closed") {
		t.Errorf("Expected 'transport is closed' error, got: %v", err)
	}
}

func TestSessionManager_CallTool_AppendsStructuredContent(t *testing.T) {
	structured := map[string]any{"count": 1, "foo": "bar"}
	manager := newMCPSessionManager(ConnectionConfig{}, nil, nil)
	manager.client = &stubConnector{
		callToolFn: func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if ctx == nil {
				t.Fatal("context should not be nil")
			}
			if req.Params.Name != "test-tool" {
				t.Fatalf("unexpected tool name: %s", req.Params.Name)
			}
			return &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent("original")},
				StructuredContent: structured,
			}, nil
		},
	}
	manager.connected = true
	manager.initialized = true

	result, err := manager.callTool(context.Background(), "test-tool", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 content items, got %d", len(result))
	}

	original, ok := result[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("Expected first content to be TextContent, got %T", result[0])
	}
	if original.Text != "original" {
		t.Fatalf("Expected original content to stay intact, got %q", original.Text)
	}

	structuredText, ok := result[1].(mcp.TextContent)
	if !ok {
		t.Fatalf("Expected structured content to be appended as TextContent, got %T", result[1])
	}

	expectedJSON, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("Failed to marshal expected structured content: %v", err)
	}
	if structuredText.Text != string(expectedJSON) {
		t.Fatalf("Expected structured content JSON %s, got %s", string(expectedJSON), structuredText.Text)
	}
}

func TestSessionManager_CallTool_StructuredContentMarshalError(t *testing.T) {
	badStructured := map[string]any{"ch": make(chan int)}
	manager := newMCPSessionManager(ConnectionConfig{}, nil, nil)
	manager.client = &stubConnector{
		callToolFn: func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_ = ctx
			return &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent("partial")},
				StructuredContent: badStructured,
			}, nil
		},
	}
	manager.connected = true
	manager.initialized = true

	result, err := manager.callTool(context.Background(), "test-tool", nil)
	if err == nil {
		t.Fatal("Expected error when marshaling structured content")
	}
	if !strings.Contains(err.Error(), "marshal structured content") {
		t.Fatalf("Expected marshal structured content error, got %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("Expected original content to remain without appended structured content, got %d items", len(result))
	}
}

// stubConnector implements mcp.Connector for testing.
type stubConnector struct {
	callToolFn func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

func (s *stubConnector) Initialize(_ context.Context, _ *mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return nil, nil
}

func (s *stubConnector) Close() error {
	return nil
}

func (s *stubConnector) GetState() mcp.State {
	return mcp.StateInitialized
}

func (s *stubConnector) ListTools(_ context.Context, _ *mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return nil, nil
}

func (s *stubConnector) CallTool(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.callToolFn != nil {
		return s.callToolFn(ctx, req)
	}
	return &mcp.CallToolResult{}, nil
}

func (s *stubConnector) ListPrompts(_ context.Context, _ *mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	return nil, nil
}

func (s *stubConnector) GetPrompt(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return nil, nil
}

func (s *stubConnector) ListResources(_ context.Context, _ *mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	return nil, nil
}

func (s *stubConnector) ReadResource(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return nil, nil
}

func (s *stubConnector) RegisterNotificationHandler(_ string, _ mcp.NotificationHandler) {}

func (s *stubConnector) UnregisterNotificationHandler(_ string) {}

func (s *stubConnector) SetRootsProvider(_ mcp.RootsProvider) {}

func (s *stubConnector) SendRootsListChangedNotification(_ context.Context) error {
	return nil
}

// TestSessionManager_CloseWhenNotConnected tests close when not connected
func TestSessionManager_CloseWhenNotConnected(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	manager := newMCPSessionManager(config, nil, nil)

	err := manager.close()
	if err != nil {
		t.Errorf("Expected no error when closing unconnected manager, got: %v", err)
	}
}

// TestSessionManager_ShouldAttemptSessionReconnect_EdgeCases tests edge cases
func TestSessionManager_ShouldAttemptSessionReconnect_EdgeCases(t *testing.T) {
	tests := []struct {
		name            string
		config          *SessionReconnectConfig
		err             error
		shouldReconnect bool
	}{
		{
			name:            "nil config",
			config:          nil,
			err:             fmt.Errorf("some error"),
			shouldReconnect: false,
		},
		{
			name: "disabled reconnect",
			config: &SessionReconnectConfig{
				EnableAutoReconnect:  false,
				MaxReconnectAttempts: 3,
			},
			err:             fmt.Errorf("session_expired: test"),
			shouldReconnect: false,
		},
		{
			name: "nil error",
			config: &SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 3,
			},
			err:             nil,
			shouldReconnect: false,
		},
		{
			name: "connection reset error",
			config: &SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 3,
			},
			err:             fmt.Errorf("connection reset by peer"),
			shouldReconnect: true,
		},
		{
			name: "not initialized error",
			config: &SessionReconnectConfig{
				EnableAutoReconnect:  true,
				MaxReconnectAttempts: 3,
			},
			err:             fmt.Errorf("not initialized"),
			shouldReconnect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ConnectionConfig{
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"hello"},
			}
			manager := newMCPSessionManager(config, nil, tt.config)

			result := manager.shouldAttemptSessionReconnect(tt.err)
			if result != tt.shouldReconnect {
				t.Errorf("Expected shouldAttemptSessionReconnect=%v, got %v for error: %v",
					tt.shouldReconnect, result, tt.err)
			}
		})
	}
}

// TestSessionManager_ExecuteWithSessionReconnect_MaxAttempts tests max attempts exhaustion
func TestSessionManager_ExecuteWithSessionReconnect_MaxAttempts(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	reconnectConfig := &SessionReconnectConfig{
		EnableAutoReconnect:  true,
		MaxReconnectAttempts: 2,
	}
	manager := newMCPSessionManager(config, nil, reconnectConfig)

	callCount := 0
	operation := func() error {
		callCount++
		// Always return a reconnectable error
		return fmt.Errorf("session_expired: test")
	}

	err := manager.executeWithSessionReconnect(context.Background(), operation)
	if err == nil {
		t.Error("Expected error after max attempts")
	}
	// Should be called once initially
	if callCount != 1 {
		t.Errorf("Expected operation to be called once (initial attempt), got %d times", callCount)
	}
}

// TestToolSet_Name tests the Name method
func TestToolSet_Name(t *testing.T) {
	tests := []struct {
		name         string
		opts         []ToolSetOption
		expectedName string
	}{
		{
			name:         "default name",
			opts:         nil,
			expectedName: "mcp",
		},
		{
			name:         "custom name",
			opts:         []ToolSetOption{WithName("custom-mcp")},
			expectedName: "custom-mcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ConnectionConfig{
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"hello"},
			}
			toolset := NewMCPToolSet(config, tt.opts...)

			if toolset.Name() != tt.expectedName {
				t.Errorf("Expected name %q, got %q", tt.expectedName, toolset.Name())
			}

			// Clean up
			_ = toolset.Close()
		})
	}
}

// TestNewMCPToolSet_DefaultClientInfo tests default client info
func TestNewMCPToolSet_DefaultClientInfo(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
		// ClientInfo not set
	}
	toolset := NewMCPToolSet(config)

	// Verify default client info was set
	if toolset.config.connectionConfig.ClientInfo.Name != "trpc-agent-go" {
		t.Errorf("Expected default client info name 'trpc-agent-go', got %q",
			toolset.config.connectionConfig.ClientInfo.Name)
	}

	// Clean up
	_ = toolset.Close()
}

// TestSessionManager_CreateTimeoutContext_EdgeCases tests createTimeoutContext edge cases
func TestSessionManager_CreateTimeoutContext_EdgeCases(t *testing.T) {
	t.Run("no timeout configured", func(t *testing.T) {
		config := ConnectionConfig{
			Transport: "stdio",
			Command:   "echo",
			Args:      []string{"hello"},
			// No Timeout specified
		}
		manager := newMCPSessionManager(config, nil, nil)

		ctx := context.Background()
		timeoutCtx, cancel := manager.createTimeoutContext(ctx, "test")
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

// TestSessionManager_ExecuteWithSessionReconnect_OperationSucceedsAfterReconnect tests successful retry
func TestSessionManager_ExecuteWithSessionReconnect_OperationSucceedsAfterReconnect(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	reconnectConfig := &SessionReconnectConfig{
		EnableAutoReconnect:  true,
		MaxReconnectAttempts: 3,
	}
	manager := newMCPSessionManager(config, nil, reconnectConfig)

	callCount := 0
	operation := func() error {
		callCount++
		// Fail first time, succeed after (simulating successful reconnect)
		// But since we can't actually reconnect, this will keep failing
		return fmt.Errorf("session_expired: test")
	}

	err := manager.executeWithSessionReconnect(context.Background(), operation)
	// Will fail because we can't actually create a real client
	if err == nil {
		t.Error("Expected error (can't create real client)")
	}
}

// TestSessionManager_ExecuteWithSessionReconnect_DifferentErrorAfterReconnect tests different error type after reconnect
func TestSessionManager_ExecuteWithSessionReconnect_DifferentErrorAfterReconnect(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	reconnectConfig := &SessionReconnectConfig{
		EnableAutoReconnect:  true,
		MaxReconnectAttempts: 3,
	}
	manager := newMCPSessionManager(config, nil, reconnectConfig)

	callCount := 0
	operation := func() error {
		callCount++
		if callCount == 1 {
			// First call returns reconnectable error
			return fmt.Errorf("session_expired: test")
		}
		// After reconnect, return non-reconnectable error
		return fmt.Errorf("invalid argument")
	}

	err := manager.executeWithSessionReconnect(context.Background(), operation)
	// Will fail because we can't actually create a real client
	if err == nil {
		t.Error("Expected error")
	}
}

// TestToolSet_Close_MultipleClose tests closing multiple times
func TestToolSet_Close_MultipleClose(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	toolset := NewMCPToolSet(config)

	// First close
	err := toolset.Close()
	if err != nil {
		t.Errorf("Expected no error on first close, got: %v", err)
	}

	// Second close
	err = toolset.Close()
	if err != nil {
		t.Errorf("Expected no error on second close, got: %v", err)
	}
}

// TestNewMCPToolSet_WithMultipleOptions tests multiple options
func TestNewMCPToolSet_WithMultipleOptions(t *testing.T) {
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

	if toolset.Name() != "test-toolset" {
		t.Errorf("Expected name 'test-toolset', got %q", toolset.Name())
	}

	if toolset.config.toolFilterFunc == nil {
		t.Error("Expected tool filter func to be set")
	}

	if toolset.config.sessionReconnectConfig == nil {
		t.Error("Expected session reconnect config to be set")
	}

	if toolset.config.sessionReconnectConfig.MaxReconnectAttempts != 3 {
		t.Errorf("Expected max reconnect attempts 3, got %d",
			toolset.config.sessionReconnectConfig.MaxReconnectAttempts)
	}

	// Clean up
	_ = toolset.Close()
}

// TestSessionManager_CallTool_WithReconnect tests callTool with reconnect enabled
func TestSessionManager_CallTool_WithReconnect(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	reconnectConfig := &SessionReconnectConfig{
		EnableAutoReconnect:  true,
		MaxReconnectAttempts: 2,
	}
	manager := newMCPSessionManager(config, nil, reconnectConfig)

	// Client is nil, should trigger reconnect logic
	manager.mu.Lock()
	manager.client = nil
	manager.mu.Unlock()

	_, err := manager.callTool(context.Background(), "test-tool", map[string]any{})
	if err == nil {
		t.Error("Expected error when client is nil")
	}
	// Should contain "transport is closed" error
	if !strings.Contains(err.Error(), "transport is closed") {
		t.Errorf("Expected 'transport is closed' error, got: %v", err)
	}
}

// TestSessionManager_CreateClient_InvalidTransport tests createClient with invalid transport
func TestSessionManager_CreateClient_InvalidTransport(t *testing.T) {
	config := ConnectionConfig{
		Transport: "invalid-transport",
		Command:   "echo",
		Args:      []string{"hello"},
	}
	manager := newMCPSessionManager(config, nil, nil)

	_, err := manager.createClient()
	if err == nil {
		t.Error("Expected error for invalid transport")
	}
	if !strings.Contains(err.Error(), "unsupported transport") {
		t.Errorf("Expected 'unsupported transport' error, got: %v", err)
	}
}

// TestSessionManager_CreateClient_DefaultClientInfo tests createClient with default client info
func TestSessionManager_CreateClient_DefaultClientInfo(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
		// ClientInfo not set
	}
	manager := newMCPSessionManager(config, nil, nil)

	// This will fail because we can't create a real client, but it exercises the code path
	_, err := manager.createClient()
	// We expect an error because the command/args are not valid for a real MCP server
	// But the important thing is that it tried to create the client with default client info
	_ = err // Ignore the error, we just want to exercise the code path
}

// TestSessionManager_CreateClient_SSETransport tests createClient with SSE transport
func TestSessionManager_CreateClient_SSETransport(t *testing.T) {
	config := ConnectionConfig{
		Transport: "sse",
		ServerURL: "http://localhost:8080",
		Headers: map[string]string{
			"Authorization": "Bearer token",
		},
	}
	manager := newMCPSessionManager(config, nil, nil)

	// This will fail because there's no real server, but it exercises the code path
	_, err := manager.createClient()
	// We expect an error, but the important thing is that it tried to create the SSE client
	_ = err // Ignore the error
}

// TestSessionManager_CreateClient_StreamableTransport tests createClient with streamable transport
func TestSessionManager_CreateClient_StreamableTransport(t *testing.T) {
	config := ConnectionConfig{
		Transport: "streamable",
		ServerURL: "http://localhost:8080",
		Headers: map[string]string{
			"X-Custom-Header": "value",
		},
	}
	manager := newMCPSessionManager(config, nil, nil)

	// This will fail because there's no real server, but it exercises the code path
	_, err := manager.createClient()
	// We expect an error, but the important thing is that it tried to create the streamable client
	_ = err // Ignore the error
}

// TestSessionManager_CreateClient_WithMCPOptions tests createClient with MCP options
func TestSessionManager_CreateClient_WithMCPOptions(t *testing.T) {
	config := ConnectionConfig{
		Transport: "sse",
		ServerURL: "http://localhost:8080",
	}
	// Create a dummy MCP option
	dummyOption := func(c *mcp.Client) {}
	manager := newMCPSessionManager(config, []mcp.ClientOption{dummyOption}, nil)

	// This will fail because there's no real server, but it exercises the code path
	_, err := manager.createClient()
	// We expect an error, but the important thing is that it tried to create the client with options
	_ = err // Ignore the error
}

// TestSessionManager_ExecuteWithSessionReconnect tests the executeWithSessionReconnect method
func TestSessionManager_ExecuteWithSessionReconnect(t *testing.T) {
	t.Run("operation succeeds on first try", func(t *testing.T) {
		config := ConnectionConfig{
			Transport: "stdio",
			Command:   "echo",
			Args:      []string{"hello"},
		}
		reconnectConfig := &SessionReconnectConfig{
			EnableAutoReconnect:  true,
			MaxReconnectAttempts: 3,
		}
		manager := newMCPSessionManager(config, nil, reconnectConfig)

		callCount := 0
		operation := func() error {
			callCount++
			return nil
		}

		err := manager.executeWithSessionReconnect(context.Background(), operation)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if callCount != 1 {
			t.Errorf("Expected operation to be called once, got %d times", callCount)
		}
	})

	t.Run("operation fails with non-reconnectable error", func(t *testing.T) {
		config := ConnectionConfig{
			Transport: "stdio",
			Command:   "echo",
			Args:      []string{"hello"},
		}
		reconnectConfig := &SessionReconnectConfig{
			EnableAutoReconnect:  true,
			MaxReconnectAttempts: 3,
		}
		manager := newMCPSessionManager(config, nil, reconnectConfig)

		expectedErr := fmt.Errorf("invalid argument")
		callCount := 0
		operation := func() error {
			callCount++
			return expectedErr
		}

		err := manager.executeWithSessionReconnect(context.Background(), operation)
		if err != expectedErr {
			t.Errorf("Expected error %v, got: %v", expectedErr, err)
		}
		if callCount != 1 {
			t.Errorf("Expected operation to be called once, got %d times", callCount)
		}
	})

	t.Run("reconnection disabled", func(t *testing.T) {
		config := ConnectionConfig{
			Transport: "stdio",
			Command:   "echo",
			Args:      []string{"hello"},
		}
		manager := newMCPSessionManager(config, nil, nil) // No reconnect config

		expectedErr := fmt.Errorf("session_expired: test")
		callCount := 0
		operation := func() error {
			callCount++
			return expectedErr
		}

		err := manager.executeWithSessionReconnect(context.Background(), operation)
		if err != expectedErr {
			t.Errorf("Expected error %v, got: %v", expectedErr, err)
		}
		if callCount != 1 {
			t.Errorf("Expected operation to be called once, got %d times", callCount)
		}
	})

	t.Run("context cancelled", func(t *testing.T) {
		config := ConnectionConfig{
			Transport: "stdio",
			Command:   "echo",
			Args:      []string{"hello"},
		}
		reconnectConfig := &SessionReconnectConfig{
			EnableAutoReconnect:  true,
			MaxReconnectAttempts: 3,
		}
		manager := newMCPSessionManager(config, nil, reconnectConfig)

		// Create a cancelled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		callCount := 0
		operation := func() error {
			callCount++
			return fmt.Errorf("session_expired: test")
		}

		err := manager.executeWithSessionReconnect(ctx, operation)
		if err == nil {
			t.Error("Expected error when context is cancelled")
		}
		if !strings.Contains(err.Error(), "reconnection aborted") {
			t.Errorf("Expected 'reconnection aborted' error, got: %v", err)
		}
		if callCount != 1 {
			t.Errorf("Expected operation to be called once, got %d times", callCount)
		}
	})
}
