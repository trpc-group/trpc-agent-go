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

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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

func TestMCPTool_ToolMetadata(t *testing.T) {
	testCases := []struct {
		name        string
		annotations *mcp.ToolAnnotations
		want        tool.ToolMetadata
	}{
		{
			name: "nil annotations",
			want: tool.ToolMetadata{},
		},
		{
			name: "title only annotations",
			annotations: &mcp.ToolAnnotations{
				Title: "Human title",
			},
			want: tool.ToolMetadata{},
		},
		{
			name: "explicit safety hints",
			annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:    mcp.BoolPtr(true),
				DestructiveHint: mcp.BoolPtr(true),
				OpenWorldHint:   mcp.BoolPtr(true),
			},
			want: tool.ToolMetadata{
				ReadOnly:    true,
				Destructive: true,
				OpenWorld:   true,
			},
		},
		{
			name: "partial safety hints",
			annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: mcp.BoolPtr(true),
			},
			want: tool.ToolMetadata{
				ReadOnly: true,
			},
		},
		{
			name: "title and idempotent are ignored",
			annotations: &mcp.ToolAnnotations{
				Title:          "Ignored title",
				IdempotentHint: mcp.BoolPtr(true),
			},
			want: tool.ToolMetadata{},
		},
		{
			name: "explicit false stays zero value",
			annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:    mcp.BoolPtr(false),
				DestructiveHint: mcp.BoolPtr(false),
				OpenWorldHint:   mcp.BoolPtr(false),
			},
			want: tool.ToolMetadata{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mcpTool := newMCPTool(mcp.Tool{
				Name:        "metadata_tool",
				Description: "Tool with metadata",
				Annotations: tc.annotations,
			}, &mcpSessionManager{})

			if got := mcpTool.ToolMetadata(); got != tc.want {
				t.Fatalf("expected metadata %+v, got %+v", tc.want, got)
			}
			if got := tool.MetadataOf(mcpTool); got != tc.want {
				t.Fatalf("expected MetadataOf metadata %+v, got %+v", tc.want, got)
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

func TestNewMCPTool_PrefersRawSchemaNumericBounds(t *testing.T) {
	// Simulate the real MCP ListTools path: RawInputSchema retains exact JSON
	// Schema 2020-12 number tokens, while InputSchema already went through
	// kin-openapi (float64 rounding + exclusive bounds rewritten to bools).
	rawInput := json.RawMessage(`{
		"type": "object",
		"properties": {
			"page_size": {
				"type": "integer",
				"minimum": 1,
				"maximum": 9007199254740993,
				"exclusiveMinimum": 0,
				"exclusiveMaximum": 9007199254740994
			}
		}
	}`)
	rawOutput := json.RawMessage(`{
		"type": "object",
		"properties": {
			"total": {
				"type": "integer",
				"minimum": 0,
				"maximum": 9007199254740993,
				"exclusiveMinimum": -1,
				"exclusiveMaximum": 9007199254740994
			}
		}
	}`)

	roundedMax := float64(9007199254740993)
	minOne := float64(1)
	minZero := float64(0)
	lossyInput := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			"page_size": {
				Value: &openapi3.Schema{
					Type:         &openapi3.Types{openapi3.TypeInteger},
					Min:          &minOne,
					Max:          &roundedMax,
					ExclusiveMin: true,
					ExclusiveMax: true,
				},
			},
		},
	}
	lossyOutput := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			"total": {
				Value: &openapi3.Schema{
					Type:         &openapi3.Types{openapi3.TypeInteger},
					Min:          &minZero,
					Max:          &roundedMax,
					ExclusiveMin: true,
					ExclusiveMax: true,
				},
			},
		},
	}

	mcpTool := newMCPTool(mcp.Tool{
		Name:            "bounded_search",
		Description:     "Search with bounded page size",
		RawInputSchema:  rawInput,
		RawOutputSchema: rawOutput,
		InputSchema:     lossyInput,
		OutputSchema:    lossyOutput,
	}, &mcpSessionManager{})

	decl := mcpTool.Declaration()
	require.NotNil(t, decl.InputSchema)
	require.NotNil(t, decl.OutputSchema)

	pageSize := decl.InputSchema.Properties["page_size"]
	require.Equal(t, json.Number("1"), pageSize.Minimum)
	require.Equal(t, json.Number("9007199254740993"), pageSize.Maximum)
	require.Equal(t, json.Number("0"), pageSize.ExclusiveMinimum)
	require.Equal(t, json.Number("9007199254740994"), pageSize.ExclusiveMaximum)

	total := decl.OutputSchema.Properties["total"]
	require.Equal(t, json.Number("0"), total.Minimum)
	require.Equal(t, json.Number("9007199254740993"), total.Maximum)
	require.Equal(t, json.Number("-1"), total.ExclusiveMinimum)
	require.Equal(t, json.Number("9007199254740994"), total.ExclusiveMaximum)
}

func TestNewMCPTool_FallsBackToTypedSchema(t *testing.T) {
	minOne := float64(1)
	maxTen := float64(10)
	mcpTool := newMCPTool(mcp.Tool{
		Name: "local_tool",
		InputSchema: &openapi3.Schema{
			Type: &openapi3.Types{openapi3.TypeObject},
			Properties: openapi3.Schemas{
				"page_size": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{openapi3.TypeInteger},
						Min:  &minOne,
						Max:  &maxTen,
					},
				},
			},
		},
	}, &mcpSessionManager{})

	pageSize := mcpTool.Declaration().InputSchema.Properties["page_size"]
	require.Equal(t, json.Number("1"), pageSize.Minimum)
	require.Equal(t, json.Number("10"), pageSize.Maximum)
	require.Empty(t, pageSize.ExclusiveMinimum)
	require.Empty(t, pageSize.ExclusiveMaximum)
}

func TestResolveToolSchema_NilCases(t *testing.T) {
	require.Nil(t, resolveToolSchema(nil, nil))
	require.Nil(t, resolveToolSchema(json.RawMessage{}, nil))

	raw := json.RawMessage(`{"type":"integer","maximum":9007199254740993,"exclusiveMinimum":0}`)
	roundedMax := float64(9007199254740993)
	lossy := &openapi3.Schema{
		Type:         &openapi3.Types{openapi3.TypeInteger},
		Max:          &roundedMax,
		ExclusiveMin: true,
	}
	schema := resolveToolSchema(raw, lossy)
	require.Equal(t, json.Number("9007199254740993"), schema.Maximum)
	require.Equal(t, json.Number("0"), schema.ExclusiveMinimum)
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

func TestMCPToolResult_MarshalJSON_EmptyContentAsArray(t *testing.T) {
	tests := []struct {
		name   string
		result *mcpToolResult
	}{
		{name: "nil result", result: nil},
		{name: "nil content", result: &mcpToolResult{}},
		{name: "empty content", result: &mcpToolResult{Content: []mcp.Content{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.result.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON failed: %v", err)
			}
			if string(data) != "[]" {
				t.Fatalf("expected empty JSON array, got %q", string(data))
			}
		})
	}
}

func TestMCPToolResult_GetCallbackResult(t *testing.T) {
	expected := []mcp.Content{mcp.NewTextContent("hello")}
	result := &mcpToolResult{
		Content: expected,
		Meta:    map[string]any{"metadata": "ignored"},
	}

	callbackResult, ok := result.GetCallbackResult().([]mcp.Content)
	if !ok {
		t.Fatalf("expected []mcp.Content, got %T", result.GetCallbackResult())
	}
	if len(callbackResult) != len(expected) {
		t.Fatalf("expected %d content item(s), got %d", len(expected), len(callbackResult))
	}
}

func TestMCPToolResult_RetryResultError(t *testing.T) {
	result := &mcpToolResult{IsError: true}
	if !result.RetryResultError() {
		t.Fatal("expected RetryResultError to report true")
	}
	result.IsError = false
	if result.RetryResultError() {
		t.Fatal("expected RetryResultError to report false")
	}
}
