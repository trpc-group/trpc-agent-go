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
	"sort"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	iprofile "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/profile"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
)

func normalizeProfile(
	structure *profilecompiler.Structure,
	profile *promptiter.Profile,
) (*promptiter.Profile, error) {
	normalized, err := normalizeCompilerProfile(structure, profile)
	if err != nil {
		return nil, err
	}
	return toPromptIterProfile(normalized), nil
}

func normalizeCompilerProfile(
	structure *profilecompiler.Structure,
	profile *promptiter.Profile,
) (*profilecompiler.Profile, error) {
	if structure == nil {
		return nil, errors.New("structure state is nil")
	}
	return structure.NormalizeProfile(toCompilerProfile(profile))
}

func toCompilerProfile(profile *promptiter.Profile) *profilecompiler.Profile {
	if profile == nil {
		return nil
	}
	converted := &profilecompiler.Profile{
		StructureID: profile.StructureID,
		Overrides:   make([]profilecompiler.SurfaceOverride, 0, len(profile.Overrides)),
	}
	for _, override := range profile.Overrides {
		converted.Overrides = append(converted.Overrides, profilecompiler.SurfaceOverride(override))
	}
	return converted
}

func toPromptIterProfile(profile *profilecompiler.Profile) *promptiter.Profile {
	converted := &promptiter.Profile{
		StructureID: profile.StructureID,
		Overrides:   make([]promptiter.SurfaceOverride, 0, len(profile.Overrides)),
	}
	for _, override := range profile.Overrides {
		converted.Overrides = append(converted.Overrides, promptiter.SurfaceOverride(override))
	}
	return converted
}

func applyPatchSet(
	structure *profilecompiler.Structure,
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
		surface, ok := structure.SurfaceIndex[patch.SurfaceID]
		if !ok {
			return nil, fmt.Errorf("patch surface id %q is unknown", patch.SurfaceID)
		}
		value, err := profilecompiler.SanitizePatchValue(surface, patch.Value)
		if err != nil {
			return nil, fmt.Errorf("sanitize patch %q: %w", patch.SurfaceID, err)
		}
		if profilecompiler.PatchValueEqual(surface, value) {
			delete(overrideIndex, patch.SurfaceID)
			continue
		}
		overrideIndex[patch.SurfaceID] = promptiter.SurfaceOverride{
			SurfaceID: patch.SurfaceID,
			NodeID:    surface.NodeID,
			Type:      surface.Type,
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
	structure *profilecompiler.Structure,
	overrideIndex map[string]promptiter.SurfaceOverride,
) *promptiter.Profile {
	if structure == nil {
		return nil
	}
	overrides := make([]promptiter.SurfaceOverride, 0, len(overrideIndex))
	for surfaceID, override := range overrideIndex {
		surface := structure.SurfaceIndex[surfaceID]
		if profilecompiler.PatchValueEqual(surface, override.Value) {
			continue
		}
		override.NodeID = surface.NodeID
		override.Type = surface.Type
		overrides = append(overrides, override)
	}
	sort.SliceStable(overrides, func(i, j int) bool {
		return overrides[i].SurfaceID < overrides[j].SurfaceID
	})
	return &promptiter.Profile{
		StructureID: structure.Snapshot.StructureID,
		Overrides:   overrides,
	}
}

func resolveProfileSurface(
	structure *profilecompiler.Structure,
	overrideIndex map[string]promptiter.SurfaceOverride,
	surfaceID string,
) (astructure.Surface, error) {
	if structure == nil {
		return astructure.Surface{}, errors.New("structure state is nil")
	}
	surface, ok := structure.SurfaceIndex[surfaceID]
	if !ok {
		return astructure.Surface{}, fmt.Errorf("surface id %q is unknown", surfaceID)
	}
	if override, ok := overrideIndex[surfaceID]; ok {
		surface.Value = iprofile.CloneSurfaceValue(override.Value)
		return surface, nil
	}
	surface.Value = iprofile.CloneSurfaceValue(surface.Value)
	return surface, nil
}
