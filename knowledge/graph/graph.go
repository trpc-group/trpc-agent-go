//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graph defines graph-native knowledge data and query models.
package graph

// Node represents a graph node.
type Node struct {
	ID       string         `json:"id"`
	Name     string         `json:"name,omitempty"`
	Content  string         `json:"content,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Edge represents a directed graph edge.
// ID is optional (omitempty): edges are uniquely identified by the
// (FromID, ToID, Type) tuple in the graph store, so a separate ID is not
// required for MERGE operations. When present it is stored as an edge property.
type Edge struct {
	ID       string         `json:"id,omitempty"`
	FromID   string         `json:"from_id"`
	ToID     string         `json:"to_id"`
	Type     string         `json:"type"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Data contains graph nodes and edges.
type Data struct {
	Nodes []*Node `json:"nodes"`
	Edges []*Edge `json:"edges"`
}
