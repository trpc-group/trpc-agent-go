//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package compiler

import (
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockToolProvider implements dsl.ToolProvider for testing
type mockToolProvider struct {
	tools map[string]tool.Tool
}

func (m *mockToolProvider) Get(name string) (tool.Tool, error) {
	if t, ok := m.tools[name]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("tool %q not found", name)
}

func (m *mockToolProvider) GetMultiple(names []string) (map[string]tool.Tool, error) {
	result := make(map[string]tool.Tool)
	for _, name := range names {
		t, err := m.Get(name)
		if err != nil {
			return nil, err
		}
		result[name] = t
	}
	return result, nil
}

func (m *mockToolProvider) GetAll() map[string]tool.Tool {
	return m.tools
}

func TestCompileTools_ValidationErrors(t *testing.T) {
	// Create a mock provider that has "my_tool"
	mockProvider := &mockToolProvider{
		tools: map[string]tool.Tool{
			"my_tool": nil, // We just need it to exist for validation
		},
	}

	tests := []struct {
		name        string
		cfg         map[string]any
		wantErr     bool
		errContains string
	}{
		{
			name: "single web_search is valid",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "web_search"},
				},
			},
			wantErr: false,
		},
		{
			name: "multiple web_search should error",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "web_search"},
					map[string]any{"type": "web_search", "provider": "google"},
				},
			},
			wantErr:     true,
			errContains: "multiple web_search tools configured",
		},
		{
			name: "multiple knowledge_search should error",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{
						"type": "knowledge_search",
						"vector_store": map[string]any{
							"type":       "tcvector",
							"url":        "http://localhost:8080",
							"user":       "test",
							"password":   "test",
							"database":   "test",
							"collection": "test",
						},
						"embedder": map[string]any{
							"type":    "openai",
							"api_key": "test-key",
						},
					},
					map[string]any{
						"type": "knowledge_search",
						"vector_store": map[string]any{
							"type":       "tcvector",
							"url":        "http://localhost:8080",
							"user":       "test",
							"password":   "test",
							"database":   "test",
							"collection": "test",
						},
						"embedder": map[string]any{
							"type":    "openai",
							"api_key": "test-key",
						},
					},
				},
			},
			wantErr:     true,
			errContains: "multiple knowledge_search tools configured",
		},
		{
			name: "multiple code_interpreter should error",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "code_interpreter"},
					map[string]any{"type": "code_interpreter"},
				},
			},
			wantErr:     true,
			errContains: "multiple code_interpreter tools configured",
		},
		{
			name: "duplicate builtin tool should error",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "builtin", "name": "my_tool"},
					map[string]any{"type": "builtin", "name": "my_tool"},
				},
			},
			wantErr:     true,
			errContains: "duplicate builtin tool",
		},
		{
			name: "builtin tool not found should error",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "builtin", "name": "nonexistent_tool"},
				},
			},
			wantErr:     true,
			errContains: "builtin tool \"nonexistent_tool\" not found",
		},
		{
			name: "builtin tool without provider should error",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "builtin", "name": "my_tool"},
				},
			},
			wantErr:     true,
			errContains: "no ToolProvider available",
		},
		{
			name: "multiple mcp without server_label should error",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "mcp", "server_url": "http://server1"},
					map[string]any{"type": "mcp", "server_url": "http://server2"},
				},
			},
			wantErr:     true,
			errContains: "multiple mcp tools configured without server_label",
		},
		{
			name: "multiple mcp with duplicate server_label should error",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "mcp", "server_url": "http://server1", "server_label": "my_mcp"},
					map[string]any{"type": "mcp", "server_url": "http://server2", "server_label": "my_mcp"},
				},
			},
			wantErr:     true,
			errContains: "duplicate mcp server_label",
		},
		{
			name: "multiple mcp with unique server_label is valid",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "mcp", "server_url": "http://server1", "server_label": "mcp_a"},
					map[string]any{"type": "mcp", "server_url": "http://server2", "server_label": "mcp_b"},
				},
			},
			wantErr: false,
		},
		{
			name: "mixed tools with no conflicts is valid",
			cfg: map[string]any{
				"tools": []any{
					map[string]any{"type": "web_search"},
					map[string]any{"type": "code_interpreter"},
					map[string]any{"type": "mcp", "server_url": "http://server1", "server_label": "mcp_a"},
				},
			},
			wantErr: false,
		},
		// Note: conditioned_filter and agentic_filter parsing tests are in toolspec_test.go
		// because CompileTools attempts to connect to external services (vector stores).
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use nil provider only for "builtin tool without provider should error" test
			var provider dsl.ToolProvider
			if tt.name != "builtin tool without provider should error" {
				provider = mockProvider
			}

			_, err := CompileTools(tt.cfg, provider)
			if tt.wantErr {
				if err == nil {
					t.Errorf("CompileTools() expected error containing %q, got nil", tt.errContains)
					return
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("CompileTools() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("CompileTools() unexpected error = %v", err)
				}
			}
		})
	}
}
