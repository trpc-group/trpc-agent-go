//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

// Direction describes graph traversal direction.
type Direction string

const (
	// DirectionOut traverses outgoing edges.
	DirectionOut Direction = "out"
	// DirectionIn traverses incoming edges.
	DirectionIn Direction = "in"
	// DirectionBoth traverses both incoming and outgoing edges.
	DirectionBoth Direction = "both"
)

// TraverseQuery describes a graph traversal from one or more start nodes.
type TraverseQuery struct {
	StartIDs  []string  `json:"start_ids"`
	Direction Direction `json:"direction,omitempty"`
	EdgeTypes []string  `json:"edge_types,omitempty"`
	MaxDepth  int       `json:"max_depth,omitempty"`
	MaxNodes  int       `json:"max_nodes,omitempty"`
}

// TraverseResult is the result of a graph traversal.
type TraverseResult struct {
	Nodes     []*Node `json:"nodes"`
	Edges     []*Edge `json:"edges"`
	Truncated bool    `json:"truncated,omitempty"`
	Message   string  `json:"message,omitempty"`
}

// PathQuery describes a path search between two graph nodes.
type PathQuery struct {
	FromID    string    `json:"from_id"`
	ToID      string    `json:"to_id"`
	Direction Direction `json:"direction,omitempty"`
	EdgeTypes []string  `json:"edge_types,omitempty"`
	MaxDepth  int       `json:"max_depth,omitempty"`
	MaxPaths  int       `json:"max_paths,omitempty"`
}

// PathResult is the result of a path search.
type PathResult struct {
	Paths     []*Path `json:"paths"`
	Truncated bool    `json:"truncated,omitempty"`
	Message   string  `json:"message,omitempty"`
}

// Path represents a graph path.
type Path struct {
	Nodes []*Node `json:"nodes"`
	Edges []*Edge `json:"edges"`
}
