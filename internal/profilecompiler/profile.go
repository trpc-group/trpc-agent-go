//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package profilecompiler

import (
	"maps"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

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

// CloneProfile returns a deep copy of profile for isolated runtime ownership.
func CloneProfile(profile *Profile) *Profile {
	if profile == nil {
		return nil
	}
	cloned := *profile
	cloned.Overrides = make([]SurfaceOverride, len(profile.Overrides))
	for i, override := range profile.Overrides {
		cloned.Overrides[i] = override
		cloned.Overrides[i].Value = cloneSurfaceValue(override.Value)
	}
	return &cloned
}

// CloneSnapshot returns a deep copy of snapshot for isolated compilation.
func CloneSnapshot(snapshot *astructure.Snapshot) *astructure.Snapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.Nodes = append([]astructure.Node(nil), snapshot.Nodes...)
	cloned.Edges = append([]astructure.Edge(nil), snapshot.Edges...)
	cloned.Surfaces = make([]astructure.Surface, len(snapshot.Surfaces))
	for i, surface := range snapshot.Surfaces {
		cloned.Surfaces[i] = surface
		cloned.Surfaces[i].Value = cloneSurfaceValue(surface.Value)
	}
	return &cloned
}

func cloneSurfaceValue(value astructure.SurfaceValue) astructure.SurfaceValue {
	cloned := value
	cloned.FewShot = sanitizeExamples(value.FewShot)
	cloned.Tools = cloneProfileToolRefs(value.Tools)
	cloned.Skills = append([]astructure.SkillRef(nil), value.Skills...)
	if value.Text != nil {
		text := *value.Text
		cloned.Text = &text
	}
	if value.PromptSyntax != nil {
		syntax := *value.PromptSyntax
		cloned.PromptSyntax = &syntax
	}
	if value.Model != nil {
		modelRef := *value.Model
		modelRef.Headers = maps.Clone(value.Model.Headers)
		cloned.Model = &modelRef
	}
	return cloned
}

func cloneProfileToolRefs(refs []astructure.ToolRef) []astructure.ToolRef {
	if refs == nil {
		return nil
	}
	cloned := make([]astructure.ToolRef, len(refs))
	for i, ref := range refs {
		cloned[i] = ref
		cloned[i].InputSchema = cloneToolSchema(ref.InputSchema)
		cloned[i].OutputSchema = cloneToolSchema(ref.OutputSchema)
	}
	return cloned
}
