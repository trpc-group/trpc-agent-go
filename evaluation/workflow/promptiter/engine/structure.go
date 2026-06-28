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

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	isurface "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/surface"
)

type structureState struct {
	snapshot        *astructure.Snapshot
	nodeIndex       map[string]astructure.Node
	surfaceIndex    map[string]astructure.Surface
	knownSurfaceIDs map[string]struct{}
}

func newStructureState(snapshot *astructure.Snapshot) (*structureState, error) {
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
	supportedSurfaces, err := supportedPromptIterSurfaces(
		snapshot.Surfaces,
		nodeIndex,
	)
	if err != nil {
		return nil, err
	}
	surfaceIndex, err := isurface.BuildIndex(supportedSurfaces)
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
				return nil, fmt.Errorf(
					"surface %q expected surface id %q",
					surface.SurfaceID,
					canonicalID,
				)
			}
			continue
		}
		nodeTypeKey := fmt.Sprintf("%s\x00%s", surface.NodeID, surface.Type)
		if _, ok := seenNodeTypes[nodeTypeKey]; ok {
			return nil, fmt.Errorf(
				"duplicate surface type %q for node id %q",
				surface.Type,
				surface.NodeID,
			)
		}
		seenNodeTypes[nodeTypeKey] = struct{}{}
	}
	return &structureState{
		snapshot:        snapshot,
		nodeIndex:       nodeIndex,
		surfaceIndex:    surfaceIndex,
		knownSurfaceIDs: knownSurfaceIDs,
	}, nil
}

func supportedPromptIterSurfaces(
	surfaces []astructure.Surface,
	nodeIndex map[string]astructure.Node,
) ([]astructure.Surface, error) {
	supported := make([]astructure.Surface, 0, len(surfaces))
	toolDeclarationNodeIDs := toolDeclarationPatchNodeIDs(surfaces, nodeIndex)
	for _, surface := range surfaces {
		if !isurface.IsSupportedType(surface.Type) {
			continue
		}
		if surface.Type != astructure.SurfaceTypeTool {
			supported = append(supported, surface)
			continue
		}
		if _, ok := toolDeclarationNodeIDs[surface.NodeID]; !ok {
			continue
		}
		expanded, err := expandToolSurface(surface)
		if err != nil {
			return nil, fmt.Errorf("surface %q is invalid: %w", surface.SurfaceID, err)
		}
		supported = append(supported, expanded...)
	}
	return supported, nil
}

func toolDeclarationPatchNodeIDs(
	surfaces []astructure.Surface,
	nodeIndex map[string]astructure.Node,
) map[string]struct{} {
	out := make(map[string]struct{})
	for _, surface := range surfaces {
		if surface.Type != astructure.SurfaceTypeGlobalInstruction {
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

func promptIterStructureSnapshot(snapshot *astructure.Snapshot) (*astructure.Snapshot, error) {
	if snapshot == nil {
		return nil, nil
	}
	projected := *snapshot
	nodeIndex := make(map[string]astructure.Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodeIndex[node.NodeID] = node
	}
	toolDeclarationNodeIDs := toolDeclarationPatchNodeIDs(snapshot.Surfaces, nodeIndex)
	projected.Surfaces = make([]astructure.Surface, 0, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		if surface.Type != astructure.SurfaceTypeTool {
			projected.Surfaces = append(projected.Surfaces, surface)
			continue
		}
		if _, ok := toolDeclarationNodeIDs[surface.NodeID]; !ok {
			projected.Surfaces = append(projected.Surfaces, surface)
			continue
		}
		expanded, err := expandToolSurface(surface)
		if err != nil {
			return nil, fmt.Errorf("surface %q is invalid: %w", surface.SurfaceID, err)
		}
		if len(expanded) == 0 {
			projected.Surfaces = append(projected.Surfaces, surface)
			continue
		}
		projected.Surfaces = append(projected.Surfaces, expanded...)
	}
	return &projected, nil
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
