//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package profilecompiler compiles structure-bound profiles into agent run options.
package profilecompiler

import (
	"errors"
	"fmt"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

// Structure stores a PromptIter-compatible structure view for profile compilation.
type Structure struct {
	// Snapshot stores the normalized structure snapshot.
	Snapshot *astructure.Snapshot
	// NodeIndex stores nodes by node ID.
	NodeIndex map[string]astructure.Node
	// SurfaceIndex stores supported surfaces by surface ID.
	SurfaceIndex map[string]astructure.Surface
	// KnownSurfaceIDs stores every surface ID accepted by trace validation.
	KnownSurfaceIDs map[string]struct{}
}

// NewStructure validates and indexes a structure snapshot for profile compilation.
func NewStructure(snapshot *astructure.Snapshot) (*Structure, error) {
	if snapshot == nil {
		return nil, errors.New("structure snapshot is nil")
	}
	if snapshot.StructureID == "" {
		return nil, errors.New("structure id is empty")
	}
	nodeIndex := make(map[string]astructure.Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.NodeID == "" {
			return nil, errors.New("node id is empty")
		}
		if _, ok := nodeIndex[node.NodeID]; ok {
			return nil, fmt.Errorf("duplicate node id %q", node.NodeID)
		}
		nodeIndex[node.NodeID] = node
	}
	normalized, err := NormalizeStructureSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	supportedSurfaces := supportedPromptIterSurfaces(normalized.Surfaces, nodeIndex)
	surfaceIndex, err := BuildIndex(supportedSurfaces)
	if err != nil {
		return nil, fmt.Errorf("build surface index: %w", err)
	}
	knownSurfaceIDs, err := buildKnownSurfaceIDs(snapshot.Surfaces, nodeIndex)
	if err != nil {
		return nil, err
	}
	for surfaceID := range surfaceIndex {
		knownSurfaceIDs[surfaceID] = struct{}{}
	}
	addAggregateToolSurfaceIDs(knownSurfaceIDs, supportedSurfaces, nodeIndex)
	seenNodeTypes := make(map[string]struct{}, len(supportedSurfaces))
	for _, surface := range supportedSurfaces {
		if _, ok := nodeIndex[surface.NodeID]; !ok {
			return nil, fmt.Errorf("surface %q references unknown node id %q", surface.SurfaceID, surface.NodeID)
		}
		if surface.Type == astructure.SurfaceTypeTool {
			canonicalID, err := canonicalToolSurfaceID(surface)
			if err != nil {
				return nil, fmt.Errorf("surface %q is invalid: %w", surface.SurfaceID, err)
			}
			if surface.SurfaceID != canonicalID {
				return nil, fmt.Errorf("surface %q expected surface id %q", surface.SurfaceID, canonicalID)
			}
			continue
		}
		nodeTypeKey := fmt.Sprintf("%s\x00%s", surface.NodeID, surface.Type)
		if _, ok := seenNodeTypes[nodeTypeKey]; ok {
			return nil, fmt.Errorf("duplicate surface type %q for node id %q", surface.Type, surface.NodeID)
		}
		seenNodeTypes[nodeTypeKey] = struct{}{}
	}
	return &Structure{
		Snapshot:        normalized,
		NodeIndex:       nodeIndex,
		SurfaceIndex:    surfaceIndex,
		KnownSurfaceIDs: knownSurfaceIDs,
	}, nil
}

// NormalizeStructureSnapshot expands PromptIter tool surfaces while preserving the original structure id.
func NormalizeStructureSnapshot(snapshot *astructure.Snapshot) (*astructure.Snapshot, error) {
	if snapshot == nil {
		return nil, errors.New("structure snapshot is nil")
	}
	normalized := *snapshot
	nodeIndex := make(map[string]astructure.Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodeIndex[node.NodeID] = node
	}
	toolDeclarationNodeIDs := toolSurfacePatchNodeIDs(snapshot.Surfaces, nodeIndex)
	normalized.Surfaces = make([]astructure.Surface, 0, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		if surface.Type != astructure.SurfaceTypeTool {
			normalized.Surfaces = append(normalized.Surfaces, surface)
			continue
		}
		if _, ok := toolDeclarationNodeIDs[surface.NodeID]; !ok {
			normalized.Surfaces = append(normalized.Surfaces, surface)
			continue
		}
		expanded, err := expandToolSurface(surface)
		if err != nil {
			return nil, fmt.Errorf("surface %q is invalid: %w", surface.SurfaceID, err)
		}
		if len(expanded) == 0 {
			continue
		}
		normalized.Surfaces = append(normalized.Surfaces, expanded...)
	}
	return &normalized, nil
}

func supportedPromptIterSurfaces(
	surfaces []astructure.Surface,
	nodeIndex map[string]astructure.Node,
) []astructure.Surface {
	supported := make([]astructure.Surface, 0, len(surfaces))
	toolDeclarationNodeIDs := toolSurfacePatchNodeIDs(surfaces, nodeIndex)
	for _, surface := range surfaces {
		if !IsSupportedType(surface.Type) {
			continue
		}
		if surface.Type != astructure.SurfaceTypeTool {
			supported = append(supported, surface)
			continue
		}
		if _, ok := toolDeclarationNodeIDs[surface.NodeID]; !ok {
			continue
		}
		if len(surface.Value.Tools) == 0 {
			continue
		}
		supported = append(supported, surface)
	}
	return supported
}

func toolSurfacePatchNodeIDs(
	surfaces []astructure.Surface,
	nodeIndex map[string]astructure.Node,
) map[string]struct{} {
	out := make(map[string]struct{})
	for _, surface := range surfaces {
		if surface.Type != astructure.SurfaceTypeTool {
			continue
		}
		node, ok := nodeIndex[surface.NodeID]
		if !ok || node.Kind != astructure.NodeKindLLM {
			continue
		}
		out[surface.NodeID] = struct{}{}
	}
	return out
}

func expandToolSurface(surface astructure.Surface) ([]astructure.Surface, error) {
	if surface.Value.Text != nil || surface.Value.PromptSyntax != nil ||
		len(surface.Value.FewShot) > 0 || surface.Value.Model != nil ||
		len(surface.Value.Skills) > 0 {
		return nil, errors.New("tool surface value contains non-tool fields")
	}
	if len(surface.Value.Tools) == 0 {
		return nil, nil
	}
	if len(surface.Value.Tools) == 1 {
		surfaceID, err := canonicalToolSurfaceID(surface)
		if err != nil {
			return nil, err
		}
		surface.SurfaceID = surfaceID
		return []astructure.Surface{surface}, nil
	}
	out := make([]astructure.Surface, 0, len(surface.Value.Tools))
	seen := make(map[string]struct{}, len(surface.Value.Tools))
	for _, toolRef := range surface.Value.Tools {
		current := surface
		current.Value = astructure.SurfaceValue{Tools: []astructure.ToolRef{toolRef}}
		surfaceID, err := canonicalToolSurfaceID(current)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[surfaceID]; ok {
			return nil, fmt.Errorf("duplicate tool surface id %q", surfaceID)
		}
		seen[surfaceID] = struct{}{}
		current.SurfaceID = surfaceID
		out = append(out, current)
	}
	return out, nil
}

func addAggregateToolSurfaceIDs(
	known map[string]struct{},
	surfaces []astructure.Surface,
	nodeIndex map[string]astructure.Node,
) {
	for _, surface := range surfaces {
		if surface.Type != astructure.SurfaceTypeTool {
			continue
		}
		node, ok := nodeIndex[surface.NodeID]
		if !ok || node.Kind != astructure.NodeKindLLM {
			continue
		}
		known[astructure.SurfaceID(surface.NodeID, astructure.SurfaceTypeTool)] = struct{}{}
	}
}

func canonicalToolSurfaceID(surface astructure.Surface) (string, error) {
	if len(surface.Value.Tools) != 1 {
		return "", fmt.Errorf("tool surface must contain exactly one tool, got %d", len(surface.Value.Tools))
	}
	toolID := surface.Value.Tools[0].ID
	if toolID == "" {
		return "", errors.New("tool id is empty")
	}
	return astructure.SurfaceID(surface.NodeID, astructure.SurfaceTypeTool, toolID), nil
}

func buildKnownSurfaceIDs(
	surfaces []astructure.Surface,
	nodeIndex map[string]astructure.Node,
) (map[string]struct{}, error) {
	known := make(map[string]struct{}, len(surfaces))
	for _, surface := range surfaces {
		if surface.SurfaceID == "" {
			return nil, errors.New("surface id is empty")
		}
		if surface.NodeID == "" {
			return nil, errors.New("surface node id is empty")
		}
		if _, ok := nodeIndex[surface.NodeID]; !ok {
			return nil, fmt.Errorf("surface %q references unknown node id %q", surface.SurfaceID, surface.NodeID)
		}
		if _, ok := known[surface.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate surface id %q", surface.SurfaceID)
		}
		known[surface.SurfaceID] = struct{}{}
	}
	return known, nil
}
