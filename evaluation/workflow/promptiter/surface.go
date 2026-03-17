//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptiter defines shared domain models used by the PromptIter workflow.
package promptiter

// SurfaceType is a discriminator for stable prompt injection points.
type SurfaceType string

const (
	// SurfaceTypeInstruction means the surface is injected into a single node prompt.
	SurfaceTypeInstruction SurfaceType = "instruction"
	// SurfaceTypeGlobalInstruction means the surface is injected globally to all nodes.
	SurfaceTypeGlobalInstruction SurfaceType = "global_instruction"
	// SurfaceTypeFewShot means the surface stores few-shot examples.
	SurfaceTypeFewShot SurfaceType = "few_shot"
	// SurfaceTypeModel means the surface controls node model choice.
	SurfaceTypeModel SurfaceType = "model"
)

// Surface binds an editable surface to its owning node and type.
type Surface struct {
	// SurfaceID is the stable identifier of the surface.
	SurfaceID string
	// NodeID links this surface to one structure node.
	NodeID string
	// Type selects how this surface is interpreted and injected.
	Type SurfaceType
	// Value keeps the exported baseline content before any profile override.
	Value SurfaceValue
}

// SurfaceValue is a union-like payload carrying one concrete surface representation.
type SurfaceValue struct {
	// Text stores instruction-style content when the surface is text.
	Text *string
	// Message stores few-shot examples when the surface carries examples.
	Message []Messages
	// Model stores model-selection metadata when the surface is model type.
	Model *Model
}

// Model stores node model-selection settings used as a surface override.
type Model struct {
	// Provider identifies the model provider.
	Provider string
	// Name identifies the concrete model inside the provider.
	Name string
}

// Messages stores one few-shot example set.
type Messages struct {
	// Messages is the ordered message list that composes this few-shot sample.
	Messages []Message
}

// Message describes one role-content pair inside a few-shot example.
type Message struct {
	// Role is the message speaker role, such as user or assistant.
	Role string
	// Content is the textual body carried by this message.
	Content string
}
