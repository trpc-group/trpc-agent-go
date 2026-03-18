//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package surface provides internal helpers for PromptIter surface semantics.
package surface

import "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"

// IsSupportedType reports whether the surface type is supported by PromptIter.
func IsSupportedType(surfaceType promptiter.SurfaceType) bool {
	switch surfaceType {
	case promptiter.SurfaceTypeInstruction,
		promptiter.SurfaceTypeGlobalInstruction,
		promptiter.SurfaceTypeFewShot,
		promptiter.SurfaceTypeModel:
		return true
	default:
		return false
	}
}
