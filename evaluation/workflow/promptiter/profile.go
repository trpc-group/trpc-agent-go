//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptiter defines shared domain models used by the PromptIter workflow.
package promptiter

import astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"

// Profile represents a candidate set of overrides applied on top of a baseline snapshot.
type Profile struct {
	// StructureID binds all overrides to one exported structure version.
	StructureID string
	// Overrides stores per-surface replacement values for one optimization attempt.
	Overrides []SurfaceOverride
}

// SurfaceOverride carries one replacement value for a surface.
type SurfaceOverride struct {
	// SurfaceID targets the surface to replace during execution.
	SurfaceID string
	// Value provides the candidate replacement content for the surface.
	Value astructure.SurfaceValue
}
