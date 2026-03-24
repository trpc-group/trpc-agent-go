//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chainagent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
)

// Export exports the static structure of the chain agent.
func (a *ChainAgent) Export(
	ctx context.Context,
	exportChild structure.ChildExporter,
) (*structure.Snapshot, error) {
	rootNodeID := istructure.EscapeLocalName(a.name)
	snapshot := &structure.Snapshot{
		EntryNodeID: rootNodeID,
		Nodes: []structure.Node{
			{
				NodeID: rootNodeID,
				Kind:   structure.NodeKindAgent,
				Name:   a.name,
			},
		},
	}
	if len(a.subAgents) == 0 {
		return snapshot, nil
	}
	allocator := istructure.NewPathAllocator(rootNodeID)
	prevTerminals := []string{rootNodeID}
	for _, subAgent := range a.subAgents {
		childSnapshot, err := exportChild(ctx, subAgent)
		if err != nil {
			return nil, err
		}
		mountPath := allocator.Next(subAgent.Info().Name)
		rebased, err := istructure.RebaseSnapshot(childSnapshot, mountPath)
		if err != nil {
			return nil, err
		}
		snapshot.Nodes = append(snapshot.Nodes, rebased.Nodes...)
		snapshot.Edges = append(snapshot.Edges, rebased.Edges...)
		snapshot.Surfaces = append(snapshot.Surfaces, rebased.Surfaces...)
		for _, terminalNodeID := range prevTerminals {
			snapshot.Edges = append(snapshot.Edges, structure.Edge{
				FromNodeID: terminalNodeID,
				ToNodeID:   rebased.EntryNodeID,
			})
		}
		prevTerminals = istructure.TerminalNodeIDs(rebased)
	}
	return snapshot, nil
}
