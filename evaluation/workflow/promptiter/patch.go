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

// PatchSet contains all surface patches proposed by one optimization round.
type PatchSet struct {
	// Patches stores per-surface patch proposals before acceptance.
	Patches []SurfacePatch
}

// SurfacePatch represents one atomic profile change candidate.
type SurfacePatch struct {
	// SurfaceID identifies which surface this patch will modify.
	SurfaceID string
	// Value is the replacement value emitted by optimizer.
	Value astructure.SurfaceValue
	// Reason records why this patch is proposed.
	Reason string
}
