//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package team

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
)

// Export exports the static structure of the team.
func (t *Team) Export(
	ctx context.Context,
	exportChild structure.ChildExporter,
) (*structure.Snapshot, error) {
	t.mu.RLock()
	name := t.name
	mode := t.mode
	coordinator := t.coordinator
	entryName := t.entryName
	members := append([]agent.Agent(nil), t.members...)
	t.mu.RUnlock()

	rootNodeID := istructure.EscapeLocalName(name)
	snapshot := &structure.Snapshot{
		EntryNodeID: rootNodeID,
		Nodes: []structure.Node{
			{
				NodeID: rootNodeID,
				Kind:   structure.NodeKindAgent,
				Name:   name,
			},
		},
	}
	switch mode {
	case ModeCoordinator:
		return exportCoordinatorTeam(
			ctx,
			exportChild,
			snapshot,
			rootNodeID,
			coordinator,
			members,
		)
	case ModeSwarm:
		return exportSwarmTeam(
			ctx,
			exportChild,
			snapshot,
			rootNodeID,
			entryName,
			members,
		)
	default:
		return snapshot, nil
	}
}

func exportCoordinatorTeam(
	ctx context.Context,
	exportChild structure.ChildExporter,
	snapshot *structure.Snapshot,
	rootNodeID string,
	coordinator agent.Agent,
	members []agent.Agent,
) (*structure.Snapshot, error) {
	if coordinator == nil {
		return snapshot, nil
	}
	memberAllocator := istructure.NewPathAllocator(rootNodeID)
	coordinatorSnapshot, err := exportChild(ctx, coordinator)
	if err != nil {
		return nil, err
	}
	coordinatorPath := memberAllocator.Next("coordinator")
	rebasedCoordinator, err := istructure.RebaseSnapshot(
		coordinatorSnapshot,
		coordinatorPath,
	)
	if err != nil {
		return nil, err
	}
	snapshot.Nodes = append(snapshot.Nodes, rebasedCoordinator.Nodes...)
	snapshot.Edges = append(snapshot.Edges, rebasedCoordinator.Edges...)
	snapshot.Surfaces = append(snapshot.Surfaces, rebasedCoordinator.Surfaces...)
	snapshot.Edges = append(snapshot.Edges, structure.Edge{
		FromNodeID: rootNodeID,
		ToNodeID:   rebasedCoordinator.EntryNodeID,
	})
	for _, member := range members {
		memberSnapshot, exportErr := exportChild(ctx, member)
		if exportErr != nil {
			return nil, exportErr
		}
		memberPath := memberAllocator.Next(member.Info().Name)
		rebasedMember, rebaseErr := istructure.RebaseSnapshot(memberSnapshot, memberPath)
		if rebaseErr != nil {
			return nil, rebaseErr
		}
		snapshot.Nodes = append(snapshot.Nodes, rebasedMember.Nodes...)
		snapshot.Edges = append(snapshot.Edges, rebasedMember.Edges...)
		snapshot.Surfaces = append(snapshot.Surfaces, rebasedMember.Surfaces...)
		snapshot.Edges = append(snapshot.Edges, structure.Edge{
			FromNodeID: rebasedCoordinator.EntryNodeID,
			ToNodeID:   rebasedMember.EntryNodeID,
		})
	}
	return snapshot, nil
}

func exportSwarmTeam(
	ctx context.Context,
	exportChild structure.ChildExporter,
	snapshot *structure.Snapshot,
	rootNodeID string,
	entryName string,
	members []agent.Agent,
) (*structure.Snapshot, error) {
	allocator := istructure.NewPathAllocator(rootNodeID)
	rebasedMembers := make([]*structure.Snapshot, 0, len(members))
	for _, member := range members {
		memberSnapshot, err := exportChild(ctx, member)
		if err != nil {
			return nil, err
		}
		memberPath := allocator.Next(member.Info().Name)
		rebasedMember, err := istructure.RebaseSnapshot(memberSnapshot, memberPath)
		if err != nil {
			return nil, err
		}
		rebasedMember = istructure.RootOnly(rebasedMember)
		rebasedMembers = append(rebasedMembers, rebasedMember)
		snapshot.Nodes = append(snapshot.Nodes, rebasedMember.Nodes...)
		snapshot.Edges = append(snapshot.Edges, rebasedMember.Edges...)
		snapshot.Surfaces = append(snapshot.Surfaces, rebasedMember.Surfaces...)
	}
	var entrySnapshot *structure.Snapshot
	for i, member := range members {
		if member.Info().Name == entryName {
			entrySnapshot = rebasedMembers[i]
			break
		}
	}
	if entrySnapshot == nil {
		return nil, fmt.Errorf("entry member %q not found", entryName)
	}
	snapshot.Edges = append(snapshot.Edges, structure.Edge{
		FromNodeID: rootNodeID,
		ToNodeID:   entrySnapshot.EntryNodeID,
	})
	for i, memberSnapshot := range rebasedMembers {
		for j, otherSnapshot := range rebasedMembers {
			if i == j {
				continue
			}
			snapshot.Edges = append(snapshot.Edges, structure.Edge{
				FromNodeID: memberSnapshot.EntryNodeID,
				ToNodeID:   otherSnapshot.EntryNodeID,
			})
		}
	}
	return snapshot, nil
}
