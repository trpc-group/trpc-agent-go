//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graphstore defines storage interfaces for graph-enabled knowledge.
package graphstore

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

// Store defines graph storage operations.
type Store interface {
	// AddNodes inserts or updates graph nodes.
	AddNodes(ctx context.Context, nodes []*graph.Node) error

	// AddEdges inserts or updates graph edges.
	AddEdges(ctx context.Context, edges []*graph.Edge) error

	// Traverse runs graph traversal from one or more start nodes.
	Traverse(ctx context.Context, query *graph.TraverseQuery) (*graph.TraverseResult, error)

	// FindPaths finds paths between two graph nodes.
	FindPaths(ctx context.Context, query *graph.PathQuery) (*graph.PathResult, error)

	// Close releases any resources held by the store (e.g. database connections).
	// Implementations that hold no resources may return nil.
	Close() error
}

// Searcher is an optional native graph-node retrieval capability. Stores that
// do not implement it can still support Traverse and FindPaths, but must be
// wrapped by a Searcher before backing a complete GraphProvider view.
type Searcher interface {
	// SearchNodes searches graph nodes by text, vector, or both.
	SearchNodes(ctx context.Context, req *SearchNodesRequest) (*SearchNodesResult, error)
}

// SearchMode tells a graph backend how to interpret Query and Vector. A
// backend that owns its embedder may accept a text Query in vector or hybrid
// mode and produce the vector internally.
type SearchMode string

const (
	// SearchModeHybrid combines backend-native text and vector retrieval.
	SearchModeHybrid SearchMode = "hybrid"
	// SearchModeVector uses vector retrieval, embedding Query internally when
	// Vector is empty and the backend supports that operation.
	SearchModeVector SearchMode = "vector"
	// SearchModeKeyword uses backend-native keyword retrieval.
	SearchModeKeyword SearchMode = "keyword"
	// SearchModeFilter applies only Filter and does not require Query or Vector.
	SearchModeFilter SearchMode = "filter"
)

// SearchNodesRequest configures native graph-node retrieval. Mode declares the
// requested retrieval semantics. Query always carries the original text;
// Vector optionally carries a caller-produced embedding. In vector or hybrid
// mode, a backend that encapsulates its own embedder may receive Query with an
// empty Vector and produce the vector internally. Filter is combined with the
// retrieval condition. The request must be non-nil.
type SearchNodesRequest struct {
	// Query is the original text query for keyword or hybrid search.
	Query string
	// Vector is a caller-produced embedding for vector or hybrid search.
	Vector []float64
	// Mode is hybrid, vector, keyword, or filter. Empty selects the backend's
	// default mode for direct Searcher callers.
	Mode SearchMode
	// Limit bounds the number of nodes returned. Non-positive values select an
	// implementation-defined bounded default.
	Limit int
	// MinScore is the minimum normalized relevance score in the range [0, 1].
	// Values outside that range are invalid.
	MinScore float64
	// Filter limits candidate graph nodes.
	Filter *SearchNodesFilter
}

// SearchNodesFilter describes portable graph-node filters. FilterCondition
// fields use id, name, content, or metadata.<key>.
type SearchNodesFilter struct {
	// NodeIDs limits results to specific node IDs.
	NodeIDs []string
	// Metadata matches node metadata by key and value.
	Metadata map[string]any
	// FilterCondition expresses nested portable metadata conditions.
	FilterCondition *searchfilter.UniversalFilterCondition
}

// SearchNodesResult contains native graph-node matches sorted by descending
// score, with Node.ID as the deterministic tie-breaker. Filter-only results use
// score 1.
type SearchNodesResult struct {
	// Nodes contains matching nodes and their relevance scores.
	Nodes []*ScoredNode
}

// ScoredNode associates a graph node with a normalized relevance score.
type ScoredNode struct {
	// Node is the matched graph node.
	Node *graph.Node
	// Score is in the range [0, 1], where larger values are more relevant.
	Score float64
}
