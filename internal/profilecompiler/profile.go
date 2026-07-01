//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package profilecompiler

import astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"

// Profile represents a runtime-normalized set of overrides applied on top of a baseline snapshot.
type Profile struct {
	// StructureID binds all overrides to one exported structure version.
	StructureID string `json:"structureID,omitempty"`
	// Overrides stores per-surface replacement values for one optimization attempt.
	Overrides []SurfaceOverride `json:"overrides,omitempty"`
}

// SurfaceOverride carries one replacement value for a surface.
type SurfaceOverride struct {
	// SurfaceID targets the surface to replace during execution.
	SurfaceID string `json:"surfaceID"`
	// NodeID targets the runtime node that owns the surface.
	NodeID string `json:"nodeID"`
	// Type identifies the surface value variant.
	Type astructure.SurfaceType `json:"type"`
	// Value provides the candidate replacement content for the surface.
	Value astructure.SurfaceValue `json:"value"`
}
