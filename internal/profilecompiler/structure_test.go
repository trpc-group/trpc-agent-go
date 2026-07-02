//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package profilecompiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

func TestNormalizeStructureSnapshotExpandsToolSurfaces(t *testing.T) {
	text := "global"
	snapshot := &astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "node_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1", Kind: astructure.NodeKindLLM},
			{NodeID: "tool_node", Kind: astructure.NodeKindTool},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "node_1#global_instruction",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeGlobalInstruction,
				Value:     astructure.SurfaceValue{Text: &text},
			},
			{
				SurfaceID: "tool_node#global_instruction",
				NodeID:    "tool_node",
				Type:      astructure.SurfaceTypeGlobalInstruction,
				Value:     astructure.SurfaceValue{Text: &text},
			},
			{
				SurfaceID: "node_1#tool",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{
						{ID: "lookup", Description: "Lookup."},
						{ID: "delay", Description: "Delay."},
					},
				},
			},
			{
				SurfaceID: "tool_node#tool.lookup",
				NodeID:    "tool_node",
				Type:      astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{{ID: "lookup"}},
				},
			},
		},
	}
	normalized, err := NormalizeStructureSnapshot(snapshot)
	assert.NoError(t, err)
	require.NotNil(t, normalized)
	assert.Len(t, normalized.Surfaces, 5)
	assert.Equal(t, "node_1#global_instruction", normalized.Surfaces[0].SurfaceID)
	assert.Equal(t, "tool_node#global_instruction", normalized.Surfaces[1].SurfaceID)
	assert.Equal(t, "node_1#tool.lookup", normalized.Surfaces[2].SurfaceID)
	assert.Equal(t, "node_1#tool.delay", normalized.Surfaces[3].SurfaceID)
	assert.Equal(t, "tool_node#tool.lookup", normalized.Surfaces[4].SurfaceID)
	normalized, err = NormalizeStructureSnapshot(nil)
	assert.Nil(t, normalized)
	assert.EqualError(t, err, "structure snapshot is nil")
}

func TestNormalizeStructureSnapshotExpandsLLMToolSurfaceWithoutGlobalInstruction(t *testing.T) {
	snapshot := &astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "node_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1", Kind: astructure.NodeKindLLM},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "node_1#tool",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{
						{ID: "lookup", Description: "Lookup."},
					},
				},
			},
		},
	}
	normalized, err := NormalizeStructureSnapshot(snapshot)
	assert.NoError(t, err)
	require.NotNil(t, normalized)
	require.Len(t, normalized.Surfaces, 1)
	assert.Equal(t, "node_1#tool.lookup", normalized.Surfaces[0].SurfaceID)
}

func TestNormalizeStructureSnapshotDropsEmptyAndRejectsInvalidToolSurfaces(t *testing.T) {
	text := "global"
	snapshot := &astructure.Snapshot{
		StructureID: "structure_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1", Kind: astructure.NodeKindLLM},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "node_1#global_instruction",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeGlobalInstruction,
				Value:     astructure.SurfaceValue{Text: &text},
			},
			{
				SurfaceID: "node_1#tool.empty",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeTool,
			},
		},
	}
	normalized, err := NormalizeStructureSnapshot(snapshot)
	assert.NoError(t, err)
	require.NotNil(t, normalized)
	require.Len(t, normalized.Surfaces, 1)
	assert.Equal(t, "node_1#global_instruction", normalized.Surfaces[0].SurfaceID)
	invalid := "invalid"
	snapshot.Surfaces[1].Value = astructure.SurfaceValue{
		Text:  &invalid,
		Tools: []astructure.ToolRef{{ID: "lookup"}},
	}
	normalized, err = NormalizeStructureSnapshot(snapshot)
	assert.Nil(t, normalized)
	assert.EqualError(t, err, `surface "node_1#tool.empty" is invalid: tool surface value contains non-tool fields`)
}

func TestNewStructureStoresNormalizedSnapshot(t *testing.T) {
	text := "global"
	snapshot := &astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "node_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1", Kind: astructure.NodeKindLLM},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "node_1#global_instruction",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeGlobalInstruction,
				Value:     astructure.SurfaceValue{Text: &text},
			},
			{
				SurfaceID: "node_1#tool",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{
						{ID: "lookup", Description: "Lookup."},
						{ID: "delay", Description: "Delay."},
					},
				},
			},
		},
	}
	structure, err := NewStructure(snapshot)
	require.NoError(t, err)
	require.NotNil(t, structure)
	require.Len(t, structure.Snapshot.Surfaces, 3)
	assert.Equal(t, "node_1#tool.lookup", structure.Snapshot.Surfaces[1].SurfaceID)
	assert.Equal(t, "node_1#tool.delay", structure.Snapshot.Surfaces[2].SurfaceID)
	assert.Contains(t, structure.SurfaceIndex, "node_1#tool.lookup")
	assert.Contains(t, structure.SurfaceIndex, "node_1#tool.delay")
	assert.Contains(t, structure.KnownSurfaceIDs, "node_1#tool")
	assert.Equal(t, "node_1#tool", snapshot.Surfaces[1].SurfaceID)
}

func TestNewStructureAcceptsAggregateToolTraceIDAfterNormalization(t *testing.T) {
	snapshot := &astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "node_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1", Kind: astructure.NodeKindLLM},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "node_1#tool.lookup",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{{ID: "lookup"}},
				},
			},
		},
	}
	structure, err := NewStructure(snapshot)
	require.NoError(t, err)
	require.NotNil(t, structure)
	assert.Contains(t, structure.SurfaceIndex, "node_1#tool.lookup")
	assert.Contains(t, structure.KnownSurfaceIDs, "node_1#tool")
}
