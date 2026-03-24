//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package structure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExport_NodeID_IsStableAcrossRepeatedExports(t *testing.T) {
	ag := &testAgent{name: "root/path"}
	first, err := Export(context.Background(), ag)
	require.NoError(t, err)
	second, err := Export(context.Background(), ag)
	require.NoError(t, err)
	assert.Equal(t, first.Nodes[0].NodeID, second.Nodes[0].NodeID)
	assert.Equal(t, "root~1path", first.Nodes[0].NodeID)
}

func TestExport_SurfaceID_IsStableAcrossRepeatedExports(t *testing.T) {
	text := "x"
	ag := &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
			Surfaces: []Surface{
				{NodeID: "root", Type: SurfaceTypeInstruction, Value: SurfaceValue{Text: &text}},
			},
		},
	}
	first, err := Export(context.Background(), ag)
	require.NoError(t, err)
	second, err := Export(context.Background(), ag)
	require.NoError(t, err)
	assert.Equal(t, first.Surfaces[0].SurfaceID, second.Surfaces[0].SurfaceID)
}

func TestExport_StructureID_IsStableAcrossRepeatedExports(t *testing.T) {
	ag := &testAgent{name: "root"}
	first, err := Export(context.Background(), ag)
	require.NoError(t, err)
	second, err := Export(context.Background(), ag)
	require.NoError(t, err)
	assert.Equal(t, first.StructureID, second.StructureID)
}

func TestExport_StructureID_ChangesWhenNodeChanges(t *testing.T) {
	base := &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
		},
	}
	changed := &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
				{NodeID: "child", Kind: NodeKindAgent, Name: "child"},
			},
		},
	}
	baseSnapshot, err := Export(context.Background(), base)
	require.NoError(t, err)
	changedSnapshot, err := Export(context.Background(), changed)
	require.NoError(t, err)
	assert.NotEqual(t, baseSnapshot.StructureID, changedSnapshot.StructureID)
}

func TestExport_StructureID_ChangesWhenEdgeChanges(t *testing.T) {
	base := &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
				{NodeID: "child", Kind: NodeKindAgent, Name: "child"},
			},
		},
	}
	changed := &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
				{NodeID: "child", Kind: NodeKindAgent, Name: "child"},
			},
			Edges: []Edge{
				{FromNodeID: "root", ToNodeID: "child"},
			},
		},
	}
	baseSnapshot, err := Export(context.Background(), base)
	require.NoError(t, err)
	changedSnapshot, err := Export(context.Background(), changed)
	require.NoError(t, err)
	assert.NotEqual(t, baseSnapshot.StructureID, changedSnapshot.StructureID)
}

func TestExport_StructureID_ChangesWhenSurfaceValueChanges(t *testing.T) {
	firstText := "one"
	secondText := "two"
	first := &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
			Surfaces: []Surface{
				{NodeID: "root", Type: SurfaceTypeInstruction, Value: SurfaceValue{Text: &firstText}},
			},
		},
	}
	second := &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
			Surfaces: []Surface{
				{NodeID: "root", Type: SurfaceTypeInstruction, Value: SurfaceValue{Text: &secondText}},
			},
		},
	}
	firstSnapshot, err := Export(context.Background(), first)
	require.NoError(t, err)
	secondSnapshot, err := Export(context.Background(), second)
	require.NoError(t, err)
	assert.NotEqual(t, firstSnapshot.StructureID, secondSnapshot.StructureID)
}
