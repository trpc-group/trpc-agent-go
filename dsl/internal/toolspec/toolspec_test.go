//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolspec

import (
	"os"
	"testing"
)

func TestParseTools_MCPTool(t *testing.T) {
	input := []any{
		map[string]any{
			"type":          "mcp",
			"server_url":    "https://mcp.example.com/mcp",
			"transport":     "sse",
			"server_label":  "example",
			"allowed_tools": []any{"tool1", "tool2"},
			"headers": map[string]any{
				"Authorization": "Bearer token",
			},
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.MCPTools) != 1 {
		t.Fatalf("expected 1 MCP tool, got %d", len(result.MCPTools))
	}
	mcp := result.MCPTools[0]
	if mcp.ServerURL != "https://mcp.example.com/mcp" {
		t.Errorf("unexpected server_url: %s", mcp.ServerURL)
	}
	if mcp.Transport != "sse" {
		t.Errorf("unexpected transport: %s", mcp.Transport)
	}
	if mcp.ServerLabel != "example" {
		t.Errorf("unexpected server_label: %s", mcp.ServerLabel)
	}
	if len(mcp.AllowedTools) != 2 {
		t.Errorf("expected 2 allowed_tools, got %d", len(mcp.AllowedTools))
	}
	if mcp.Headers["Authorization"] != "Bearer token" {
		t.Errorf("unexpected header: %s", mcp.Headers["Authorization"])
	}
}

func TestParseTools_MCPToolDefaultTransport(t *testing.T) {
	input := []any{
		map[string]any{
			"type":       "mcp",
			"server_url": "https://mcp.example.com/mcp",
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MCPTools[0].Transport != "streamable_http" {
		t.Errorf("expected default transport 'streamable_http', got %s", result.MCPTools[0].Transport)
	}
}

func TestParseTools_BuiltinTool(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "builtin",
			"name": "my_tool",
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.BuiltinTools) != 1 {
		t.Fatalf("expected 1 builtin tool, got %d", len(result.BuiltinTools))
	}
	if result.BuiltinTools[0] != "my_tool" {
		t.Errorf("unexpected tool name: %s", result.BuiltinTools[0])
	}
}

func TestParseTools_WebSearchTool(t *testing.T) {
	input := []any{
		map[string]any{
			"type":        "web_search",
			"provider":    "duckduckgo",
			"max_results": float64(10),
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.WebSearchTools) != 1 {
		t.Fatalf("expected 1 web_search tool, got %d", len(result.WebSearchTools))
	}
	ws := result.WebSearchTools[0]
	if ws.Provider != "duckduckgo" {
		t.Errorf("unexpected provider: %s", ws.Provider)
	}
	if ws.MaxResults != 10 {
		t.Errorf("unexpected max_results: %d", ws.MaxResults)
	}
}

func TestParseTools_WebSearchToolDefaultProvider(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "web_search",
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.WebSearchTools[0].Provider != "duckduckgo" {
		t.Errorf("expected default provider 'duckduckgo', got %s", result.WebSearchTools[0].Provider)
	}
}

func TestParseTools_KnowledgeSearchTool(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "knowledge_search",
			"vector_store": map[string]any{
				"type":      "pgvector",
				"host":      "localhost",
				"port":      float64(5432),
				"user":      "postgres",
				"password":  "env:PG_PASSWORD",
				"database":  "knowledge_db",
				"table":     "documents",
				"dimension": float64(1536),
			},
			"embedder": map[string]any{
				"type":       "openai",
				"api_key":    "env:OPENAI_API_KEY",
				"model":      "text-embedding-3-small",
				"dimensions": float64(1536),
			},
			"max_results": float64(10),
			"min_score":   0.7,
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.KnowledgeSearchTools) != 1 {
		t.Fatalf("expected 1 knowledge_search tool, got %d", len(result.KnowledgeSearchTools))
	}
	ks := result.KnowledgeSearchTools[0]
	if ks.VectorStore.Type != VectorStorePgVector {
		t.Errorf("unexpected vector_store type: %s", ks.VectorStore.Type)
	}
	if ks.VectorStore.Host != "localhost" {
		t.Errorf("unexpected host: %s", ks.VectorStore.Host)
	}
	if ks.Embedder.Type != EmbedderOpenAI {
		t.Errorf("unexpected embedder type: %s", ks.Embedder.Type)
	}
	if ks.MaxResults != 10 {
		t.Errorf("unexpected max_results: %d", ks.MaxResults)
	}
	if ks.MinScore != 0.7 {
		t.Errorf("unexpected min_score: %f", ks.MinScore)
	}
}

func TestParseTools_CodeInterpreterTool(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "code_interpreter",
			"executor": map[string]any{
				"type":             "local",
				"work_dir":         "/tmp/code_exec",
				"timeout_seconds":  float64(60),
				"clean_temp_files": true,
			},
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.CodeInterpreterTools) != 1 {
		t.Fatalf("expected 1 code_interpreter tool, got %d", len(result.CodeInterpreterTools))
	}
	ci := result.CodeInterpreterTools[0]
	if ci.Executor.Type != ExecutorLocal {
		t.Errorf("unexpected executor type: %s", ci.Executor.Type)
	}
	if ci.Executor.WorkDir != "/tmp/code_exec" {
		t.Errorf("unexpected work_dir: %s", ci.Executor.WorkDir)
	}
	if ci.Executor.TimeoutSeconds != 60 {
		t.Errorf("unexpected timeout_seconds: %d", ci.Executor.TimeoutSeconds)
	}
}

func TestParseTools_CodeInterpreterToolDefaultExecutor(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "code_interpreter",
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.CodeInterpreterTools) != 1 {
		t.Fatalf("expected 1 code_interpreter tool, got %d", len(result.CodeInterpreterTools))
	}
	if result.CodeInterpreterTools[0].Executor != nil {
		t.Errorf("expected nil executor for default, got %+v", result.CodeInterpreterTools[0].Executor)
	}
}

func TestParseTools_MixedTools(t *testing.T) {
	input := []any{
		map[string]any{
			"type":       "mcp",
			"server_url": "https://mcp.example.com/mcp",
		},
		map[string]any{
			"type": "builtin",
			"name": "calculator",
		},
		map[string]any{
			"type": "web_search",
		},
	}
	result, err := ParseTools(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.MCPTools) != 1 {
		t.Errorf("expected 1 MCP tool, got %d", len(result.MCPTools))
	}
	if len(result.BuiltinTools) != 1 {
		t.Errorf("expected 1 builtin tool, got %d", len(result.BuiltinTools))
	}
	if len(result.WebSearchTools) != 1 {
		t.Errorf("expected 1 web_search tool, got %d", len(result.WebSearchTools))
	}
}

func TestParseTools_ErrorMissingType(t *testing.T) {
	input := []any{
		map[string]any{
			"server_url": "https://example.com",
		},
	}
	_, err := ParseTools(input)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestParseTools_ErrorUnknownType(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "unknown_type",
		},
	}
	_, err := ParseTools(input)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestParseTools_ErrorMCPMissingServerURL(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "mcp",
		},
	}
	_, err := ParseTools(input)
	if err == nil {
		t.Fatal("expected error for missing server_url")
	}
}

func TestParseTools_ErrorBuiltinMissingName(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "builtin",
		},
	}
	_, err := ParseTools(input)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParseTools_ErrorKnowledgeSearchMissingVectorStore(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "knowledge_search",
			"embedder": map[string]any{
				"type":    "openai",
				"api_key": "sk-xxx",
			},
		},
	}
	_, err := ParseTools(input)
	if err == nil {
		t.Fatal("expected error for missing vector_store")
	}
}

func TestParseTools_ErrorKnowledgeSearchMissingEmbedder(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "knowledge_search",
			"vector_store": map[string]any{
				"type": "pgvector",
				"host": "localhost",
			},
		},
	}
	_, err := ParseTools(input)
	if err == nil {
		t.Fatal("expected error for missing embedder")
	}
}

func TestParseTools_ErrorInvalidItem(t *testing.T) {
	input := []any{
		"not_an_object",
	}
	_, err := ParseTools(input)
	if err == nil {
		t.Fatal("expected error for invalid item type")
	}
}

func TestResolveSecret(t *testing.T) {
	os.Setenv("TEST_SECRET", "my_secret_value")
	defer os.Unsetenv("TEST_SECRET")

	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"plain value", "plain", "plain"},
		{"env reference", "env:TEST_SECRET", "my_secret_value"},
		{"missing env var", "env:MISSING_VAR", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveSecret(tt.value)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestHasAnyTools(t *testing.T) {
	tests := []struct {
		name     string
		result   *ParseResult
		expected bool
	}{
		{"empty", &ParseResult{}, false},
		{"builtin", &ParseResult{BuiltinTools: []string{"tool1"}}, true},
		{"mcp", &ParseResult{MCPTools: []*MCPToolSpec{{}}}, true},
		{"web_search", &ParseResult{WebSearchTools: []*WebSearchToolSpec{{}}}, true},
		{"knowledge", &ParseResult{KnowledgeSearchTools: []*KnowledgeSearchToolSpec{{}}}, true},
		{"code_interpreter", &ParseResult{CodeInterpreterTools: []*CodeInterpreterToolSpec{{}}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.HasAnyTools() != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, tt.result.HasAnyTools())
			}
		})
	}
}

func TestParseTools_Nil(t *testing.T) {
	result, err := ParseTools(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAnyTools() {
		t.Error("expected empty result for nil input")
	}
}

func TestParseTools_EmptyArray(t *testing.T) {
	result, err := ParseTools([]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasAnyTools() {
		t.Error("expected empty result for empty array")
	}
}
