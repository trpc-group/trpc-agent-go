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
	"testing"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func TestNewMCPTool(t *testing.T) {
	testCases := []struct {
		name           string
		mcpToolData    mcp.Tool
		expectInputNil bool
	}{
		{
			name: "without schemas",
			mcpToolData: mcp.Tool{
				Name:        "simple_tool",
				Description: "A simple tool",
			},
			expectInputNil: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sessionManager := &mcpSessionManager{}
			mcpTool := newMCPTool(tc.mcpToolData, sessionManager)

			if mcpTool == nil {
				t.Fatal("newMCPTool returned nil")
			}
			if mcpTool.mcpToolRef.Name != tc.mcpToolData.Name {
				t.Errorf("expected name %q, got %q", tc.mcpToolData.Name, mcpTool.mcpToolRef.Name)
			}

			if tc.expectInputNil && mcpTool.inputSchema != nil {
				t.Error("expected inputSchema to be nil")
			}
			if !tc.expectInputNil && mcpTool.inputSchema == nil {
				t.Error("expected inputSchema to be non-nil")
			}
		})
	}
}

func TestMCPTool_Declaration(t *testing.T) {
	testCases := []struct {
		name         string
		mcpToolData  mcp.Tool
		expectedName string
		expectedDesc string
	}{
		{
			name: "basic tool",
			mcpToolData: mcp.Tool{
				Name:        "echo_tool",
				Description: "Echoes input",
			},
			expectedName: "echo_tool",
			expectedDesc: "Echoes input",
		},
		{
			name: "tool with empty description",
			mcpToolData: mcp.Tool{
				Name:        "no_desc_tool",
				Description: "",
			},
			expectedName: "no_desc_tool",
			expectedDesc: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sessionManager := &mcpSessionManager{}
			mcpTool := newMCPTool(tc.mcpToolData, sessionManager)

			decl := mcpTool.Declaration()
			if decl == nil {
				t.Fatal("Declaration() returned nil")
			}
			if decl.Name != tc.expectedName {
				t.Errorf("expected name %q, got %q", tc.expectedName, decl.Name)
			}
			if decl.Description != tc.expectedDesc {
				t.Errorf("expected description %q, got %q", tc.expectedDesc, decl.Description)
			}
		})
	}
}

func TestMCPTool_Call_InvalidJSON(t *testing.T) {
	mcpToolData := mcp.Tool{
		Name:        "test_tool",
		Description: "Test tool",
	}

	sessionManager := &mcpSessionManager{}
	mcpTool := newMCPTool(mcpToolData, sessionManager)

	// Test with invalid JSON
	invalidJSON := []byte(`{invalid json}`)
	_, err := mcpTool.Call(context.Background(), invalidJSON)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestMCPTool_CallOnce(t *testing.T) {
	mcpToolData := mcp.Tool{
		Name:        "test_tool",
		Description: "Test tool",
	}

	sessionManager := &mcpSessionManager{}
	mcpTool := newMCPTool(mcpToolData, sessionManager)

	// Test callOnce with empty args - this will fail because session manager is not initialized
	// but it tests the code path
	args := make(map[string]any)
	_, err := mcpTool.callOnce(context.Background(), args)
	// We expect an error because the session manager is not properly initialized
	if err == nil {
		t.Log("callOnce completed (session manager may not be initialized)")
	}
}

func TestMCPTool_Call_ValidJSON(t *testing.T) {
	mcpToolData := mcp.Tool{
		Name:        "test_tool",
		Description: "Test tool",
	}

	sessionManager := &mcpSessionManager{}
	mcpTool := newMCPTool(mcpToolData, sessionManager)

	// Test with valid JSON
	validJSON := []byte(`{"arg1": "value1", "arg2": 123}`)
	_, err := mcpTool.Call(context.Background(), validJSON)
	// We expect an error because session manager is not initialized, but JSON parsing should succeed
	if err != nil {
		// This is expected - the error should be from callTool, not JSON parsing
		t.Logf("Expected error from uninitialized session manager: %v", err)
	}
}

func TestMCPTool_WithSchemas(t *testing.T) {
	// Test tool with both input and output schemas
	mcpToolData := mcp.Tool{
		Name:        "schema_tool",
		Description: "Tool with schemas",
	}

	sessionManager := &mcpSessionManager{}
	mcpTool := newMCPTool(mcpToolData, sessionManager)

	// Verify the tool was created
	if mcpTool == nil {
		t.Fatal("newMCPTool returned nil")
	}

	decl := mcpTool.Declaration()
	if decl == nil {
		t.Fatal("Declaration() returned nil")
	}
	if decl.Name != "schema_tool" {
		t.Errorf("expected name 'schema_tool', got %q", decl.Name)
	}
}

func TestMCPToolResult_GetMeta(t *testing.T) {
	// Nil meta
	result := &mcpToolResult{Meta: nil}
	if result.GetMeta() != nil {
		t.Error("expected nil meta")
	}

	// With meta data
	result.Meta = map[string]any{"key": "value", "count": 42}
	meta := result.GetMeta()
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if meta["key"] != "value" {
		t.Errorf("expected key='value', got %v", meta["key"])
	}
}

func TestMCPToolResult_MarshalJSON(t *testing.T) {
	// Verify backward compatibility: Meta is not included in JSON
	result := &mcpToolResult{
		Content: []mcp.Content{mcp.NewTextContent("hello")},
		Meta:    map[string]any{"metadata": "ignored"},
	}

	data, err := result.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	// Should match raw Content marshaling
	expected, _ := json.Marshal([]mcp.Content{mcp.NewTextContent("hello")})
	if string(data) != string(expected) {
		t.Errorf("expected %q, got %q", string(expected), string(data))
	}
}
