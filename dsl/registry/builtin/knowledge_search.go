// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package builtin

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/knowledgeconfig"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	registry.MustRegister(&KnowledgeSearchComponent{})
}

// KnowledgeSearchComponent is a built-in component for knowledge base search.
//
// This component provides RAG (Retrieval-Augmented Generation) capabilities by
// searching a vector store and returning relevant documents.
//
// Configuration:
//   - query: CEL Expression that evaluates to the search query string
//   - max_results: Maximum number of results to return (default: 10)
//   - min_score: Minimum similarity score threshold (default: 0.0)
//   - search_mode: Search strategy - "hybrid" (default), "vector", "keyword", "filter"
//   - vector_store: Vector store configuration (type + provider-specific fields)
//   - embedder: Embedding model configuration (type + provider-specific fields)
//   - conditioned_filter: Optional static complex condition filter
//
// Output structure: { documents: [{ text, score, metadata }], message? }
//
// NOTE: AgenticFilter (LLM-driven dynamic filtering) is only supported in Agent Tool mode,
// not in Tool as Node mode. Use conditioned_filter for static filtering in this mode.
//
// NOTE: This component is handled specially by the compiler (createKnowledgeSearchNodeFunc).
// Execute should not be called directly.
type KnowledgeSearchComponent struct{}

func (c *KnowledgeSearchComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.knowledge_search",
		DisplayName: "Knowledge Search",
		Description: "Search a knowledge base / vector store for relevant documents",
		Category:    "Tools",
		Version:     "1.0.0",
		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "query",
				DisplayName: "Query",
				Description: "CEL Expression that evaluates to the search query string",
				Type:        "Expression",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    true,
			},
			{
				Name:        "max_results",
				DisplayName: "Max Results",
				Description: "Maximum number of search results to return (top-k)",
				Type:        "integer",
				TypeID:      "integer",
				Kind:        "integer",
				GoType:      reflect.TypeOf(0),
				Required:    false,
				Default:     10,
			},
			{
				Name:        "min_score",
				DisplayName: "Min Score",
				Description: "Minimum similarity score threshold (0.0-1.0)",
				Type:        "number",
				TypeID:      "number",
				Kind:        "number",
				GoType:      reflect.TypeOf(0.0),
				Required:    false,
				Default:     0.0,
			},
			{
				Name:        "search_mode",
				DisplayName: "Search Mode",
				Description: "Search strategy: hybrid (default), vector, keyword, filter",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
				Default:     "hybrid",
			},
			{
				Name:        "vector_store",
				DisplayName: "Vector Store",
				Description: "Vector store configuration (type + provider-specific fields)",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    true,
			},
			{
				Name:        "embedder",
				DisplayName: "Embedder",
				Description: "Embedding model configuration (type + provider-specific fields)",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    true,
			},
			{
				Name:        "conditioned_filter",
				DisplayName: "Conditioned Filter",
				Description: "Optional static complex condition filter (supports and/or/eq/ne/gt/gte/lt/lte/in/not in/like/between)",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
			{
				Name:        "input_schema",
				DisplayName: "Input Schema",
				Description: "Optional explicit input schema (typically inferred from edge inspection)",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
			{
				Name:        "output_schema",
				DisplayName: "Output Schema",
				Description: "Optional output schema override (defaults to framework Knowledge Search format)",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
		},
	}
}

// Execute should not be called directly - component is handled by compiler.
func (c *KnowledgeSearchComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	return nil, fmt.Errorf("builtin.knowledge_search.Execute should not be called directly - component is handled by compiler")
}

// Validate validates the knowledge search configuration.
func (c *KnowledgeSearchComponent) Validate(config registry.ComponentConfig) error {
	_, err := knowledgeconfig.ParseNodeConfig(map[string]any(config))
	return err
}
