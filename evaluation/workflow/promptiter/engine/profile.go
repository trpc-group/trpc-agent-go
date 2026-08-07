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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
)

// CompileProfile validates snapshot, normalizes profile without mutating either
// input, and returns run options that apply the normalized overrides with
// execution tracing enabled. A nil profile represents an empty override set.
// It returns an error for a nil or invalid snapshot and for a profile with a
// mismatched structure ID, duplicate or unknown surfaces, or an invalid surface
// value; on error both returned values are nil. Mutable JSON-visible snapshot
// and profile data, the normalized profile, and the run options have independent
// ownership. Treat each returned RunOption as immutable; the options may be
// applied to independent runs.
func CompileProfile(
	snapshot *astructure.Snapshot,
	profile *promptiter.Profile,
) (*promptiter.Profile, []agent.RunOption, error) {
	structure, err := profilecompiler.NewStructure(
		profilecompiler.CloneSnapshot(snapshot),
	)
	if err != nil {
		return nil, nil, err
	}
	return compileProfile(structure, profile)
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
		converted.Overrides = append(converted.Overrides, profilecompiler.SurfaceOverride{
			SurfaceID: override.SurfaceID,
			Value:     override.Value,
		})
	}
	return converted
}

func fromCompilerProfile(profile *profilecompiler.Profile) *promptiter.Profile {
	if profile == nil {
		return nil
	}
	converted := &promptiter.Profile{
		StructureID: profile.StructureID,
		Overrides:   make([]promptiter.SurfaceOverride, 0, len(profile.Overrides)),
	}
	for _, override := range profile.Overrides {
		converted.Overrides = append(converted.Overrides, promptiter.SurfaceOverride{
			SurfaceID: override.SurfaceID,
			Value:     override.Value,
		})
	}
	return converted
}

func normalizeProfile(
	structure *profilecompiler.Structure,
	profile *promptiter.Profile,
) (*promptiter.Profile, error) {
	normalized, err := structure.NormalizeProfile(toCompilerProfile(profile))
	if err != nil {
		return nil, err
	}
	return fromCompilerProfile(normalized), nil
}

func compileProfile(
	structure *profilecompiler.Structure,
	profile *promptiter.Profile,
) (*promptiter.Profile, []agent.RunOption, error) {
	normalizedCompilerProfile, err := structure.NormalizeProfile(
		toCompilerProfile(profile),
	)
	if err != nil {
		return nil, nil, err
	}
	runOptions, err := profilecompiler.CompileRunOptions(
		profilecompiler.CloneProfile(normalizedCompilerProfile),
		true,
	)
	if err != nil {
		return nil, nil, err
	}
	if len(normalizedCompilerProfile.Overrides) > 0 {
		runOptions = append(
			runOptions,
			profilecompiler.WithProfile(
				profilecompiler.CloneProfile(normalizedCompilerProfile),
			),
		)
	}
	return fromCompilerProfile(
		profilecompiler.CloneProfile(normalizedCompilerProfile),
	), runOptions, nil
}

func applyPatchSet(
	structure *profilecompiler.Structure,
	profile *promptiter.Profile,
	patchSet *promptiter.PatchSet,
) (*promptiter.Profile, error) {
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
	structure *profilecompiler.Structure,
	overrideIndex map[string]promptiter.SurfaceOverride,
) *promptiter.Profile {
	overrides := make([]promptiter.SurfaceOverride, 0, len(overrideIndex))
	for _, override := range overrideIndex {
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
	surface, ok := structure.SurfaceIndex[surfaceID]
	if !ok {
		return astructure.Surface{}, fmt.Errorf("surface id %q is unknown", surfaceID)
	}
	if override, ok := overrideIndex[surfaceID]; ok {
		surface.Value = override.Value
		return surface, nil
	}
	return surface, nil
}
