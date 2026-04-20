//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	isurface "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/surface"
)

func normalizeProfile(
	structure *structureState,
	profile *promptiter.Profile,
) (*promptiter.Profile, error) {
	if structure == nil {
		return nil, errors.New("structure state is nil")
	}
	normalized := &promptiter.Profile{
		StructureID: structure.snapshot.StructureID,
		Overrides:   []promptiter.SurfaceOverride{},
	}
	if profile == nil {
		return normalized, nil
	}
	if profile.StructureID != "" && profile.StructureID != structure.snapshot.StructureID {
		return nil, fmt.Errorf(
			"profile structure id %q does not match structure id %q",
			profile.StructureID,
			structure.snapshot.StructureID,
		)
	}
	seen := make(map[string]struct{}, len(profile.Overrides))
	normalized.Overrides = make([]promptiter.SurfaceOverride, 0, len(profile.Overrides))
	for _, override := range profile.Overrides {
		if override.SurfaceID == "" {
			return nil, errors.New("profile override surface id is empty")
		}
		if _, ok := seen[override.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate profile override surface id %q", override.SurfaceID)
		}
		seen[override.SurfaceID] = struct{}{}
		surface, ok := structure.surfaceIndex[override.SurfaceID]
		if !ok {
			return nil, fmt.Errorf("profile override surface id %q is unknown", override.SurfaceID)
		}
		value, err := isurface.SanitizeValue(surface.Type, override.Value)
		if err != nil {
			return nil, fmt.Errorf("sanitize profile override %q: %w", override.SurfaceID, err)
		}
		if reflect.DeepEqual(value, surface.Value) {
			continue
		}
		normalized.Overrides = append(normalized.Overrides, promptiter.SurfaceOverride{
			SurfaceID: override.SurfaceID,
			Value:     value,
		})
	}
	sort.SliceStable(normalized.Overrides, func(i, j int) bool {
		return normalized.Overrides[i].SurfaceID < normalized.Overrides[j].SurfaceID
	})
	return normalized, nil
}

func applyPatchSet(
	structure *structureState,
	profile *promptiter.Profile,
	patchSet *promptiter.PatchSet,
) (*promptiter.Profile, error) {
	if structure == nil {
		return nil, errors.New("structure state is nil")
	}
	normalizedProfile, err := normalizeProfile(structure, profile)
	if err != nil {
		return nil, fmt.Errorf("normalize profile: %w", err)
	}
	overrideIndex := buildOverrideIndex(normalizedProfile)
	if patchSet == nil {
		return buildProfileFromOverrideIndex(structure, overrideIndex), nil
	}
	seenPatches := make(map[string]struct{}, len(patchSet.Patches))
	for _, patch := range patchSet.Patches {
		if patch.SurfaceID == "" {
			return nil, errors.New("patch surface id is empty")
		}
		if _, ok := seenPatches[patch.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate patch surface id %q", patch.SurfaceID)
		}
		seenPatches[patch.SurfaceID] = struct{}{}
		surface, ok := structure.surfaceIndex[patch.SurfaceID]
		if !ok {
			return nil, fmt.Errorf("patch surface id %q is unknown", patch.SurfaceID)
		}
		value, err := isurface.SanitizeValue(surface.Type, patch.Value)
		if err != nil {
			return nil, fmt.Errorf("sanitize patch %q: %w", patch.SurfaceID, err)
		}
		if reflect.DeepEqual(value, surface.Value) {
			delete(overrideIndex, patch.SurfaceID)
			continue
		}
		overrideIndex[patch.SurfaceID] = promptiter.SurfaceOverride{
			SurfaceID: patch.SurfaceID,
			Value:     value,
		}
	}
	return buildProfileFromOverrideIndex(structure, overrideIndex), nil
}

func buildOverrideIndex(profile *promptiter.Profile) map[string]promptiter.SurfaceOverride {
	if profile == nil {
		return map[string]promptiter.SurfaceOverride{}
	}
	index := make(map[string]promptiter.SurfaceOverride, len(profile.Overrides))
	for _, override := range profile.Overrides {
		index[override.SurfaceID] = override
	}
	return index
}

func buildProfileFromOverrideIndex(
	structure *structureState,
	overrideIndex map[string]promptiter.SurfaceOverride,
) *promptiter.Profile {
	if structure == nil {
		return nil
	}
	overrides := make([]promptiter.SurfaceOverride, 0, len(overrideIndex))
	for surfaceID, override := range overrideIndex {
		surface := structure.surfaceIndex[surfaceID]
		if reflect.DeepEqual(override.Value, surface.Value) {
			continue
		}
		overrides = append(overrides, override)
	}
	sort.SliceStable(overrides, func(i, j int) bool {
		return overrides[i].SurfaceID < overrides[j].SurfaceID
	})
	return &promptiter.Profile{
		StructureID: structure.snapshot.StructureID,
		Overrides:   overrides,
	}
}

func resolveProfileSurface(
	structure *structureState,
	overrideIndex map[string]promptiter.SurfaceOverride,
	surfaceID string,
) (astructure.Surface, error) {
	if structure == nil {
		return astructure.Surface{}, errors.New("structure state is nil")
	}
	surface, ok := structure.surfaceIndex[surfaceID]
	if !ok {
		return astructure.Surface{}, fmt.Errorf("surface id %q is unknown", surfaceID)
	}
	if override, ok := overrideIndex[surfaceID]; ok {
		surface.Value = isurface.CloneValue(override.Value)
		return surface, nil
	}
	surface.Value = isurface.CloneValue(surface.Value)
	return surface, nil
}
