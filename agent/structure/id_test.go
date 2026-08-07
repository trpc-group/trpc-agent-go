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

	"trpc.group/trpc-go/trpc-agent-go/tool"
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

func TestSurfaceIDWithParts(t *testing.T) {
	assert.Equal(t, "root#tool.lookup_record", SurfaceID("root", SurfaceTypeTool, "lookup_record"))
	assert.Equal(t, "root#tool.lookup_record.input_schema", SurfaceID("root", SurfaceTypeTool, "lookup_record", "input_schema"))
}

func TestExport_StructureID_IsStableAcrossRepeatedExports(t *testing.T) {
	ag := &testAgent{name: "root"}
	first, err := Export(context.Background(), ag)
	require.NoError(t, err)
	second, err := Export(context.Background(), ag)
	require.NoError(t, err)
	assert.Equal(t, first.StructureID, second.StructureID)
}

func TestExport_StructureID_CanonicalizesEmptyCollections(t *testing.T) {
	nilCollections := structureIDAgent(t, SurfaceValue{
		Tools: []ToolRef{{ID: "lookup", InputSchema: &tool.Schema{
			Default: map[string]any{
				"nested_map":  map[string]any(nil),
				"nested_list": []any(nil),
			},
			AdditionalProperties: []byte(nil),
		}}},
	})
	emptyCollections := structureIDAgent(t, SurfaceValue{
		FewShot: []FewShotExample{}, Skills: []SkillRef{},
		Tools: []ToolRef{{ID: "lookup", InputSchema: &tool.Schema{
			Required: []string{}, Properties: map[string]*tool.Schema{},
			Default: map[string]any{
				"nested_map":  map[string]any{},
				"nested_list": []any{},
			},
			Enum: []any{}, Defs: map[string]*tool.Schema{},
			AdditionalProperties: []byte{},
		}}},
	})

	assert.Equal(t, nilCollections.StructureID, emptyCollections.StructureID)
	assert.Equal(t,
		"struct_a4862209a83ac6204e1b59c32cf17a4a390f9823fe7b40210e8cc3ee5daf6e5b",
		emptyCollections.StructureID,
	)
}

func structureIDAgent(t *testing.T, toolValue SurfaceValue) *Snapshot {
	t.Helper()
	text := "instruction"
	snapshot, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes:       []Node{{NodeID: "root", Kind: NodeKindAgent, Name: "root"}},
			Surfaces: []Surface{
				{NodeID: "root", Type: SurfaceTypeInstruction, Value: SurfaceValue{Text: &text}},
				{NodeID: "root", Type: SurfaceTypeFewShot, Value: SurfaceValue{FewShot: []FewShotExample{}}},
				{NodeID: "root", Type: SurfaceTypeTool, Value: toolValue},
			},
		},
	})
	require.NoError(t, err)
	return snapshot
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
