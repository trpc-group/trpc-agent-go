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
	snapshot     *astructure.Snapshot
	nodeIndex    map[string]astructure.Node
	surfaceIndex map[string]astructure.Surface
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
	supportedSurfaces := make([]astructure.Surface, 0, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		if !isurface.IsSupportedType(surface.Type) {
			continue
		}
		supportedSurfaces = append(supportedSurfaces, surface)
	}
	surfaceIndex, err := isurface.BuildIndex(supportedSurfaces)
	if err != nil {
		return nil, fmt.Errorf("build surface index: %w", err)
	}
	seenNodeTypes := make(map[string]struct{}, len(supportedSurfaces))
	for _, surface := range supportedSurfaces {
		if _, ok := nodeIndex[surface.NodeID]; !ok {
			return nil, fmt.Errorf("surface %q references unknown node id %q", surface.SurfaceID, surface.NodeID)
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
		snapshot:     snapshot,
		nodeIndex:    nodeIndex,
		surfaceIndex: surfaceIndex,
	}, nil
}
