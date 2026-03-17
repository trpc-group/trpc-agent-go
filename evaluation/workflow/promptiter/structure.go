//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptiter defines shared domain models used by the PromptIter workflow.
package promptiter

// StructureSnapshot is a frozen view of one exported structure version.
type StructureSnapshot struct {
	// StructureID uniquely identifies the snapshot version used for this run.
	StructureID string
	// Nodes lists all static nodes defined by the structure.
	Nodes []StructureNode
	// Edges lists all static edges that may be traversed at runtime.
	Edges []StructureEdge
	// Surfaces lists all editable prompt surfaces used by optimization.
	Surfaces []Surface
}

// StructureNode is one immutable node descriptor from the static structure.
type StructureNode struct {
	// NodeID is the stable key used to reference the node across runs.
	NodeID string
	// Kind is the implementation category of the node.
	Kind string
	// Name is the human-readable label of the node.
	Name string
}

// StructureEdge records one potential runtime edge between two nodes.
type StructureEdge struct {
	// FromNodeID is the upstream node identifier.
	FromNodeID string
	// ToNodeID is the downstream node identifier.
	ToNodeID string
}
