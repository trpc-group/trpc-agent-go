//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package profile provides internal helpers for PromptIter profile handling.
package profile

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	isurface "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/surface"
)

// Clone deep-copies one PromptIter profile.
func Clone(profile *promptiter.Profile) *promptiter.Profile {
	if profile == nil {
		return nil
	}
	cloned := &promptiter.Profile{
		StructureID: profile.StructureID,
		Overrides:   make([]promptiter.SurfaceOverride, 0, len(profile.Overrides)),
	}
	for _, override := range profile.Overrides {
		clonedValue := isurface.CloneValue(override.Value)
		cloned.Overrides = append(cloned.Overrides, promptiter.SurfaceOverride{
			SurfaceID: override.SurfaceID,
			Value:     clonedValue,
		})
	}
	return cloned
}
