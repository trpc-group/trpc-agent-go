//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package structure provides static structure export for agents.
package structure

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ChildExporter exports a child agent snapshot within the same export session.
type ChildExporter func(ctx context.Context, a agent.Agent) (*Snapshot, error)

// Exporter exports a static structure snapshot for an agent.
type Exporter interface {
	Export(ctx context.Context, exportChild ChildExporter) (*Snapshot, error)
}

// NodeKind is the semantic kind of a static node.
type NodeKind string

const (
	// NodeKindAgent represents an agent node.
	NodeKindAgent NodeKind = "agent"
	// NodeKindLLM represents an LLM node.
	NodeKindLLM NodeKind = "llm"
	// NodeKindFunction represents a function node.
	NodeKindFunction NodeKind = "function"
	// NodeKindTool represents a tool node.
	NodeKindTool NodeKind = "tool"
)

// SurfaceType is the semantic type of a surface.
type SurfaceType string

const (
	// SurfaceTypeInstruction represents an instruction surface.
	SurfaceTypeInstruction SurfaceType = "instruction"
	// SurfaceTypeGlobalInstruction represents a global instruction surface.
	SurfaceTypeGlobalInstruction SurfaceType = "global_instruction"
	// SurfaceTypeFewShot represents a few-shot surface.
	SurfaceTypeFewShot SurfaceType = "few_shot"
	// SurfaceTypeModel represents a model selection surface.
	SurfaceTypeModel SurfaceType = "model"
	// SurfaceTypeTool represents a tool set surface.
	SurfaceTypeTool SurfaceType = "tool"
	// SurfaceTypeSkill represents a skill set surface.
	SurfaceTypeSkill SurfaceType = "skill"
)

// Snapshot is a normalized static structure snapshot.
type Snapshot struct {
	StructureID string
	EntryNodeID string
	Nodes       []Node
	Edges       []Edge
	Surfaces    []Surface
}

// Node is a static node in a structure snapshot.
type Node struct {
	NodeID string
	Kind   NodeKind
	Name   string
}

// Edge is a static possible edge in a structure snapshot.
type Edge struct {
	FromNodeID string
	ToNodeID   string
}

// Surface is a static editable surface in a structure snapshot.
type Surface struct {
	SurfaceID string
	NodeID    string
	Type      SurfaceType
	Value     SurfaceValue
}

// SurfaceValue is a discriminated union keyed by SurfaceType.
type SurfaceValue struct {
	Text    *string
	FewShot []FewShotExample
	Model   *ModelRef
	Tools   []ToolRef
	Skills  []SkillRef
}

// FewShotExample is one few-shot example group.
type FewShotExample struct {
	Messages []FewShotMessage
}

// FewShotMessage is one few-shot message.
type FewShotMessage struct {
	Role    string
	Content string
}

// ModelRef is a stable model reference.
type ModelRef struct {
	Name string
}

// ToolRef is a stable tool declaration snapshot.
type ToolRef struct {
	ID           string
	Description  string
	InputSchema  *tool.Schema
	OutputSchema *tool.Schema
}

// SkillRef is a stable skill summary snapshot.
type SkillRef struct {
	ID          string
	Description string
}
