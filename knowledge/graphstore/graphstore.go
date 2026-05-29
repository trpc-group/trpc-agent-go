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
