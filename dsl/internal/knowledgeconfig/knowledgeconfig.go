// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package knowledgeconfig provides helpers for parsing and validating
// builtin.knowledge_search configuration blocks.
package knowledgeconfig

import (
	"fmt"
)

// NodeConfig holds the parsed configuration for a builtin.knowledge_search node.
type NodeConfig struct {
	// Query is a CEL Expression that evaluates to the search query string.
	// Format: { "expression": "...", "format": "cel" }
	// The expression can reference input.* variables from the upstream node.
	Query Expression

	// MaxResults limits the number of search results returned (top-k).
	MaxResults int

	// MinScore is the minimum similarity score threshold (0.0-1.0).
	MinScore float64

	// VectorStore configuration for the knowledge base.
	VectorStore map[string]any

	// Embedder configuration for text embedding.
	Embedder map[string]any

	// ConditionedFilter is an optional static complex condition filter.
	// Supports operators: eq, ne, gt, gte, lt, lte, in, not in, like, not like, between, and, or.
	ConditionedFilter map[string]any

	// InputSchema is the expected input schema for this node.
	// This is typically inferred from the edge inspection.
	InputSchema map[string]any

	// OutputSchema is the fixed output schema for knowledge search results.
	// This is always { documents: [...] } matching the framework's Knowledge Search output.
	OutputSchema map[string]any
}

// Expression represents a CEL expression used in the engine DSL.
type Expression struct {
	Expression string `json:"expression"`
	Format     string `json:"format,omitempty"`
}

// ParseNodeConfig parses and validates a builtin.knowledge_search node configuration.
func ParseNodeConfig(config map[string]any) (NodeConfig, error) {
	var out NodeConfig

	// Parse query (required) - CEL Expression format
	rawQuery, ok := config["query"].(map[string]any)
	if !ok || rawQuery == nil {
		return out, fmt.Errorf("query is required in knowledge_search node config and must be an Expression object")
	}
	exprStr, _ := rawQuery["expression"].(string)
	if exprStr == "" {
		return out, fmt.Errorf("query.expression is required")
	}
	out.Query = Expression{
		Expression: exprStr,
		Format:     getString(rawQuery, "format"),
	}

	// Parse max_results (optional, default 10)
	out.MaxResults = 10
	if rawMax, ok := config["max_results"]; ok && rawMax != nil {
		switch v := rawMax.(type) {
		case int:
			out.MaxResults = v
		case float64:
			out.MaxResults = int(v)
		default:
			return out, fmt.Errorf("max_results must be a number")
		}
	}

	// Parse min_score (optional, default 0.0)
	out.MinScore = 0.0
	if rawScore, ok := config["min_score"]; ok && rawScore != nil {
		switch v := rawScore.(type) {
		case float64:
			out.MinScore = v
		case int:
			out.MinScore = float64(v)
		default:
			return out, fmt.Errorf("min_score must be a number")
		}
	}

	// Parse vector_store (required) - pass through as map for flexibility
	rawVS, ok := config["vector_store"].(map[string]any)
	if !ok || rawVS == nil {
		return out, fmt.Errorf("vector_store is required in knowledge_search node config")
	}
	if _, hasType := rawVS["type"].(string); !hasType {
		return out, fmt.Errorf("vector_store.type is required")
	}
	out.VectorStore = rawVS

	// Parse embedder (required) - pass through as map for flexibility
	rawEmb, ok := config["embedder"].(map[string]any)
	if !ok || rawEmb == nil {
		return out, fmt.Errorf("embedder is required in knowledge_search node config")
	}
	if _, hasType := rawEmb["type"].(string); !hasType {
		return out, fmt.Errorf("embedder.type is required")
	}
	out.Embedder = rawEmb

	// Parse conditioned_filter (optional) - complex condition filter
	if rawFilter, ok := config["conditioned_filter"].(map[string]any); ok {
		out.ConditionedFilter = rawFilter
	}

	// Parse explicit input_schema if provided
	if rawSchema, ok := config["input_schema"].(map[string]any); ok {
		out.InputSchema = rawSchema
	}

	// Parse explicit output_schema if provided, otherwise use default
	if rawSchema, ok := config["output_schema"].(map[string]any); ok {
		out.OutputSchema = rawSchema
	} else {
		out.OutputSchema = DefaultOutputSchema()
	}

	return out, nil
}

// DefaultOutputSchema returns the fixed output schema for knowledge search.
// This matches the framework's Knowledge Search output structure.
func DefaultOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"documents": DefaultDocumentsSchema(),
			"message": map[string]any{
				"type":        "string",
				"description": "Optional message (e.g., no results found)",
			},
		},
		"required":             []string{"documents"},
		"additionalProperties": false,
	}
}

// DefaultDocumentsSchema returns the schema for the documents array field.
// This is used by schema_inference to provide accurate type hints for input.documents.
// Note: This schema must match the actual DocumentResult struct in knowledge/tool/searchtool.go.
func DefaultDocumentsSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "List of retrieved documents from the knowledge base",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Text content of the document chunk",
				},
				"score": map[string]any{
					"type":        "number",
					"description": "Relevance score (0.0 to 1.0)",
				},
				"metadata": map[string]any{
					"type":        "object",
					"description": "Document metadata (e.g., source, category, tags)",
					"additionalProperties": map[string]any{
						"type": []string{"string", "number", "boolean"},
					},
				},
			},
			"required":             []string{"text", "score"},
			"additionalProperties": false,
		},
	}
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
