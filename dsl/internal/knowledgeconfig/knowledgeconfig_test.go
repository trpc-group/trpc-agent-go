// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package knowledgeconfig

import (
	"testing"
)

func TestParseNodeConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		wantErr bool
		check   func(t *testing.T, cfg NodeConfig)
	}{
		{
			name: "valid config with CEL expression",
			config: map[string]any{
				"query": map[string]any{
					"expression": "input.output_parsed.search_query",
					"format":     "cel",
				},
				"vector_store": map[string]any{
					"type": "tcvector",
					"url":  "http://localhost:8080",
				},
				"embedder": map[string]any{
					"type":     "openai",
					"base_url": "https://api.openai.com/v1",
					"api_key":  "env:OPENAI_API_KEY",
					"model":    "text-embedding-3-large",
				},
				"max_results": 20,
				"min_score":   0.5,
			},
			wantErr: false,
			check: func(t *testing.T, cfg NodeConfig) {
				if cfg.Query.Expression != "input.output_parsed.search_query" {
					t.Errorf("Query.Expression = %q, want %q", cfg.Query.Expression, "input.output_parsed.search_query")
				}
				if cfg.Query.Format != "cel" {
					t.Errorf("Query.Format = %q, want %q", cfg.Query.Format, "cel")
				}
				vsType, _ := cfg.VectorStore["type"].(string)
				if vsType != "tcvector" {
					t.Errorf("VectorStore.type = %q, want %q", vsType, "tcvector")
				}
				embType, _ := cfg.Embedder["type"].(string)
				if embType != "openai" {
					t.Errorf("Embedder.type = %q, want %q", embType, "openai")
				}
				if cfg.MaxResults != 20 {
					t.Errorf("MaxResults = %d, want %d", cfg.MaxResults, 20)
				}
				if cfg.MinScore != 0.5 {
					t.Errorf("MinScore = %f, want %f", cfg.MinScore, 0.5)
				}
			},
		},
		{
			name: "valid config with string concatenation expression",
			config: map[string]any{
				"query": map[string]any{
					"expression": "'与 ' + input.output_parsed.for_query.q1 + ' 相关但不与 ' + input.output_parsed.for_query.q2 + ' 相关'",
					"format":     "cel",
				},
				"vector_store": map[string]any{
					"type": "milvus",
					"url":  "http://milvus:19530",
				},
			"embedder": map[string]any{
				"type":  "openai",
				"model": "text-embedding-3-small",
			},
			},
			wantErr: false,
			check: func(t *testing.T, cfg NodeConfig) {
				if cfg.MaxResults != 10 {
					t.Errorf("MaxResults = %d, want default 10", cfg.MaxResults)
				}
				if cfg.MinScore != 0.0 {
					t.Errorf("MinScore = %f, want default 0.0", cfg.MinScore)
				}
			},
		},
		{
			name: "valid config with conditioned_filter",
			config: map[string]any{
				"query": map[string]any{
					"expression": "input.query",
					"format":     "cel",
				},
				"vector_store": map[string]any{
					"type": "pgvector",
				},
				"embedder": map[string]any{
					"type": "openai",
				},
				"conditioned_filter": map[string]any{
					"operator": "and",
					"value": []any{
						map[string]any{
							"field":    "category",
							"operator": "eq",
							"value":    "documentation",
						},
						map[string]any{
							"field":    "status",
							"operator": "in",
							"value":    []any{"active", "published"},
						},
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, cfg NodeConfig) {
				if cfg.ConditionedFilter == nil {
					t.Error("ConditionedFilter should not be nil")
				}
				if op, _ := cfg.ConditionedFilter["operator"].(string); op != "and" {
					t.Errorf("ConditionedFilter.operator = %q, want and", op)
				}
			},
		},
		{
			name: "missing query",
			config: map[string]any{
				"vector_store": map[string]any{"type": "tcvector"},
				"embedder":     map[string]any{"type": "openai"},
			},
			wantErr: true,
		},
		{
			name: "missing vector_store",
			config: map[string]any{
				"query": map[string]any{
					"expression": "input.query",
					"format":     "cel",
				},
				"embedder": map[string]any{"type": "openai"},
			},
			wantErr: true,
		},
		{
			name: "missing embedder",
			config: map[string]any{
				"query": map[string]any{
					"expression": "input.query",
					"format":     "cel",
				},
				"vector_store": map[string]any{"type": "tcvector"},
			},
			wantErr: true,
		},
		{
			name: "empty query expression",
			config: map[string]any{
				"query": map[string]any{
					"expression": "",
					"format":     "cel",
				},
				"vector_store": map[string]any{"type": "tcvector"},
				"embedder":     map[string]any{"type": "openai"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseNodeConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseNodeConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestDefaultOutputSchema(t *testing.T) {
	schema := DefaultOutputSchema()

	// Verify it's an object type
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}

	// Verify documents property exists
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema properties is not a map")
	}

	documents, ok := props["documents"].(map[string]any)
	if !ok {
		t.Fatal("documents property is not a map")
	}

	if documents["type"] != "array" {
		t.Errorf("documents type = %v, want array", documents["type"])
	}

	// Verify items structure matches framework Knowledge Search format
	items, ok := documents["items"].(map[string]any)
	if !ok {
		t.Fatal("documents items is not a map")
	}

	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("items properties is not a map")
	}

	// Check expected fields exist (must match actual DocumentResult struct)
	expectedFields := []string{"text", "score", "metadata"}
	for _, field := range expectedFields {
		if _, exists := itemProps[field]; !exists {
			t.Errorf("missing expected field %q in output schema", field)
		}
	}

	// Verify required fields
	required, ok := items["required"].([]string)
	if !ok {
		t.Fatal("items required is not a string slice")
	}
	requiredMap := make(map[string]bool)
	for _, r := range required {
		requiredMap[r] = true
	}
	if !requiredMap["text"] || !requiredMap["score"] {
		t.Errorf("required fields should include 'text' and 'score', got %v", required)
	}
}
