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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testAgent struct {
	name string
}

func (a *testAgent) Info() agent.Info { return agent.Info{Name: a.name} }

func (a *testAgent) Run(
	context.Context,
	*agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a *testAgent) Tools() []tool.Tool { return nil }

func (a *testAgent) SubAgents() []agent.Agent { return nil }

func (a *testAgent) FindSubAgent(string) agent.Agent { return nil }

type customExporterAgent struct {
	*testAgent
	snapshot *Snapshot
	err      error
}

func (a *customExporterAgent) Export(
	context.Context,
	ChildExporter,
) (*Snapshot, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.snapshot, nil
}

type valueExporterAgent struct {
	name         string
	child        agent.Agent
	withSurface  bool
	localNodeID  string
	surfaceValue string
}

func (a valueExporterAgent) Info() agent.Info { return agent.Info{Name: a.name} }

func (a valueExporterAgent) Run(
	context.Context,
	*agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a valueExporterAgent) Tools() []tool.Tool { return nil }

func (a valueExporterAgent) SubAgents() []agent.Agent { return nil }

func (a valueExporterAgent) FindSubAgent(string) agent.Agent { return nil }

func (a valueExporterAgent) Export(
	ctx context.Context,
	exportChild ChildExporter,
) (*Snapshot, error) {
	rootNodeID := escapeNodeIDSegment(a.name)
	if a.localNodeID != "" {
		rootNodeID = a.localNodeID
	}
	snapshot := &Snapshot{
		EntryNodeID: rootNodeID,
		Nodes: []Node{
			{NodeID: rootNodeID, Kind: NodeKindAgent, Name: a.name},
		},
	}
	if a.withSurface {
		text := a.surfaceValue
		snapshot.Surfaces = append(snapshot.Surfaces, Surface{
			NodeID: rootNodeID,
			Type:   SurfaceTypeInstruction,
			Value:  SurfaceValue{Text: &text},
		})
	}
	if a.child == nil {
		return snapshot, nil
	}
	childSnapshot, err := exportChild(ctx, a.child)
	if err != nil {
		return nil, err
	}
	mountPath := joinNodeIDForTest(rootNodeID, a.child.Info().Name)
	rebased, err := rebaseSnapshotForTest(childSnapshot, mountPath)
	if err != nil {
		return nil, err
	}
	snapshot.Nodes = append(snapshot.Nodes, rebased.Nodes...)
	snapshot.Edges = append(snapshot.Edges, rebased.Edges...)
	snapshot.Surfaces = append(snapshot.Surfaces, rebased.Surfaces...)
	snapshot.Edges = append(snapshot.Edges, Edge{
		FromNodeID: rootNodeID,
		ToNodeID:   rebased.EntryNodeID,
	})
	return snapshot, nil
}

func TestExport_NilAgent_ReturnsError(t *testing.T) {
	_, err := Export(context.Background(), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, errNilAgent)
}

func TestExport_TypedNilAgent_ReturnsError(t *testing.T) {
	var typedNil *testAgent
	_, err := Export(context.Background(), typedNil)
	require.Error(t, err)
	assert.ErrorIs(t, err, errNilAgent)
}

func TestExport_CustomExporter_UsesExporter(t *testing.T) {
	raw := &Snapshot{
		EntryNodeID: "root",
		Nodes: []Node{
			{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
		},
	}
	snapshot, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot:  raw,
	})
	require.NoError(t, err)
	assert.Equal(t, "root", snapshot.EntryNodeID)
	assert.Len(t, snapshot.Nodes, 1)
	assert.NotEmpty(t, snapshot.StructureID)
}

func TestExport_CustomAgent_FallsBackToOpaqueLeaf(t *testing.T) {
	snapshot, err := Export(context.Background(), &testAgent{name: "root"})
	require.NoError(t, err)
	require.Len(t, snapshot.Nodes, 1)
	assert.Equal(t, "root", snapshot.EntryNodeID)
	assert.Equal(t, NodeKindAgent, snapshot.Nodes[0].Kind)
	assert.Equal(t, "root", snapshot.Nodes[0].NodeID)
}

func TestExport_NormalizesAndSortsSnapshot(t *testing.T) {
	text := "instruction"
	snapshot, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
				{NodeID: "b", Kind: NodeKindFunction, Name: "b"},
				{NodeID: "a", Kind: NodeKindFunction, Name: "a"},
			},
			Edges: []Edge{
				{FromNodeID: "b", ToNodeID: "a"},
				{FromNodeID: "b", ToNodeID: "a"},
				{FromNodeID: "root", ToNodeID: "b"},
			},
			Surfaces: []Surface{
				{
					NodeID: "root",
					Type:   SurfaceTypeTool,
					Value: SurfaceValue{
						Tools: []ToolRef{{ID: "b"}, {ID: "a"}, {ID: "a"}},
					},
				},
				{
					NodeID: "root",
					Type:   SurfaceTypeSkill,
					Value: SurfaceValue{
						Skills: []SkillRef{
							{ID: "writer", Description: "Write responses."},
							{ID: "planner", Description: "Plan tasks."},
							{ID: "planner", Description: "Plan tasks."},
						},
					},
				},
				{
					NodeID: "root",
					Type:   SurfaceTypeInstruction,
					Value:  SurfaceValue{Text: &text},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, snapshot.Nodes, 3)
	assert.Equal(t, []Node{
		{NodeID: "a", Kind: NodeKindFunction, Name: "a"},
		{NodeID: "b", Kind: NodeKindFunction, Name: "b"},
		{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
	}, snapshot.Nodes)
	assert.Equal(t, []Edge{
		{FromNodeID: "b", ToNodeID: "a"},
		{FromNodeID: "root", ToNodeID: "b"},
	}, snapshot.Edges)
	require.Len(t, snapshot.Surfaces, 3)
	assert.Equal(t, "root#instruction", snapshot.Surfaces[0].SurfaceID)
	assert.Equal(t, "root#skill", snapshot.Surfaces[1].SurfaceID)
	assert.Equal(t, []SkillRef{
		{ID: "planner", Description: "Plan tasks."},
		{ID: "writer", Description: "Write responses."},
	}, snapshot.Surfaces[1].Value.Skills)
	assert.Equal(t, "root#tool", snapshot.Surfaces[2].SurfaceID)
	assert.Equal(t, []ToolRef{{ID: "a"}, {ID: "b"}}, snapshot.Surfaces[2].Value.Tools)
}

func TestExport_RejectsDuplicateNodeID(t *testing.T) {
	_, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
				{NodeID: "root", Kind: NodeKindFunction, Name: "duplicate"},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate node id")
}

func TestExport_NormalizesEdgesWithoutStringKeyCollisions(t *testing.T) {
	snapshot, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
				{NodeID: "a", Kind: NodeKindFunction, Name: "a"},
				{NodeID: "b->c", Kind: NodeKindFunction, Name: "b->c"},
				{NodeID: "a->b", Kind: NodeKindFunction, Name: "a->b"},
				{NodeID: "c", Kind: NodeKindFunction, Name: "c"},
			},
			Edges: []Edge{
				{FromNodeID: "a", ToNodeID: "b->c"},
				{FromNodeID: "a->b", ToNodeID: "c"},
				{FromNodeID: "a", ToNodeID: "b->c"},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []Edge{
		{FromNodeID: "a", ToNodeID: "b->c"},
		{FromNodeID: "a->b", ToNodeID: "c"},
	}, snapshot.Edges)
}

func TestExport_RejectsMissingEntryNode(t *testing.T) {
	_, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "missing",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entry node")
}

func TestExport_RejectsMissingEdgeEndpoint(t *testing.T) {
	_, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
			Edges: []Edge{
				{FromNodeID: "root", ToNodeID: "missing"},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "edge to node")
}

func TestExport_RejectsMissingSurfaceNode(t *testing.T) {
	_, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
			Surfaces: []Surface{
				{NodeID: "missing", Type: SurfaceTypeInstruction},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "surface node")
}

func TestExport_RejectsDuplicateSurfaceTypeOnSameNode(t *testing.T) {
	_, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
			Surfaces: []Surface{
				{NodeID: "root", Type: SurfaceTypeInstruction},
				{NodeID: "root", Type: SurfaceTypeInstruction},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate surface type")
}

func TestExport_RejectsInvalidSurfaceUnionValue(t *testing.T) {
	text := "instruction"
	_, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot: &Snapshot{
			EntryNodeID: "root",
			Nodes: []Node{
				{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
			},
			Surfaces: []Surface{
				{
					NodeID: "root",
					Type:   SurfaceTypeInstruction,
					Value: SurfaceValue{
						Text:  &text,
						Model: &ModelRef{Name: "gpt"},
					},
				},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid surface")
}

func TestValidateSurfaceValue_CoversAdditionalBranches(t *testing.T) {
	fewShot := []FewShotExample{
		{
			Messages: []FewShotMessage{
				{Role: "user", Content: "hello"},
			},
		},
	}
	require.NoError(t, validateSurfaceValue(SurfaceTypeFewShot, SurfaceValue{FewShot: fewShot}))
	require.Error(t, validateSurfaceValue(SurfaceTypeFewShot, SurfaceValue{
		Text:    stringPtr("invalid"),
		FewShot: fewShot,
	}))
	require.NoError(t, validateSurfaceValue(SurfaceTypeModel, SurfaceValue{
		Model: &ModelRef{Name: "gpt"},
	}))
	require.Error(t, validateSurfaceValue(SurfaceTypeModel, SurfaceValue{
		Model: &ModelRef{Name: "gpt"},
		Tools: []ToolRef{{ID: "echo"}},
	}))
	require.NoError(t, validateSurfaceValue(SurfaceTypeTool, SurfaceValue{
		Tools: []ToolRef{{ID: "echo"}},
	}))
	require.Error(t, validateSurfaceValue(SurfaceTypeTool, SurfaceValue{
		Tools:  []ToolRef{{ID: "echo"}},
		Skills: []SkillRef{{ID: "writer"}},
	}))
	require.NoError(t, validateSurfaceValue(SurfaceTypeSkill, SurfaceValue{
		Skills: []SkillRef{{ID: "writer"}},
	}))
	require.Error(t, validateSurfaceValue(SurfaceTypeSkill, SurfaceValue{
		Skills: []SkillRef{{ID: "writer"}},
		Tools:  []ToolRef{{ID: "echo"}},
	}))
	require.Error(t, validateSurfaceValue(SurfaceType("unknown"), SurfaceValue{}))
}

func TestExport_ClonesToolSchemas(t *testing.T) {
	inputSchema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"name": {Type: "string"},
		},
		AdditionalProperties: &tool.Schema{Type: "string"},
		Default: map[string]any{
			"enabled": true,
		},
		Defs: map[string]*tool.Schema{
			"shared": {Type: "number"},
		},
	}
	outputSchema := &tool.Schema{
		Type:  "array",
		Items: &tool.Schema{Type: "string"},
	}
	raw := &Snapshot{
		EntryNodeID: "root",
		Nodes: []Node{
			{NodeID: "root", Kind: NodeKindAgent, Name: "root"},
		},
		Surfaces: []Surface{
			{
				NodeID: "root",
				Type:   SurfaceTypeTool,
				Value: SurfaceValue{
					Tools: []ToolRef{
						{
							ID:           "echo",
							InputSchema:  inputSchema,
							OutputSchema: outputSchema,
						},
					},
				},
			},
		},
	}
	snapshot, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot:  raw,
	})
	require.NoError(t, err)
	exported := snapshot.Surfaces[0].Value.Tools[0]
	require.NotSame(t, inputSchema, exported.InputSchema)
	require.NotSame(t, outputSchema, exported.OutputSchema)
	require.NotNil(t, exported.InputSchema.Properties["name"])
	require.NotNil(t, exported.OutputSchema.Items)
	inputSchema.Properties["name"].Type = "integer"
	assert.Equal(t, "string", exported.InputSchema.Properties["name"].Type)
	rawAdditional := inputSchema.AdditionalProperties.(*tool.Schema)
	exportedAdditional := exported.InputSchema.AdditionalProperties.(*tool.Schema)
	rawAdditional.Type = "boolean"
	assert.Equal(t, "string", exportedAdditional.Type)
	rawDefault := inputSchema.Default.(map[string]any)
	exportedDefault := exported.InputSchema.Default.(map[string]any)
	rawDefault["enabled"] = false
	assert.Equal(t, true, exportedDefault["enabled"])
	exported.InputSchema.Defs["shared"].Type = "string"
	assert.Equal(t, "number", inputSchema.Defs["shared"].Type)
	outputSchema.Items.Type = "number"
	assert.Equal(t, "string", exported.OutputSchema.Items.Type)
}

func TestCloneHelpers_CloneFewShotAndSchemaValues(t *testing.T) {
	originalFewShot := []FewShotExample{
		{
			Messages: []FewShotMessage{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "world"},
			},
		},
	}
	clonedFewShot := cloneFewShot(originalFewShot)
	require.Len(t, clonedFewShot, 1)
	originalFewShot[0].Messages[0].Content = "changed"
	assert.Equal(t, "hello", clonedFewShot[0].Messages[0].Content)
	schemaValues := []any{
		[]byte("abc"),
		[]any{"nested"},
		map[string]any{"enabled": true},
	}
	clonedValues := cloneSchemaValues(schemaValues)
	require.Len(t, clonedValues, 3)
	schemaValues[0].([]byte)[0] = 'z'
	schemaValues[1].([]any)[0] = "updated"
	schemaValues[2].(map[string]any)["enabled"] = false
	assert.Equal(t, []byte("abc"), clonedValues[0].([]byte))
	assert.Equal(t, "nested", clonedValues[1].([]any)[0])
	assert.Equal(t, true, clonedValues[2].(map[string]any)["enabled"])
}

func TestEscapeNodeIDSegment_EscapesReservedCharacters(t *testing.T) {
	assert.Equal(t, "_", escapeNodeIDSegment(""))
	assert.Equal(t, "a~1b~0c", escapeNodeIDSegment("a/b~c"))
}

func TestExport_RejectsNilSnapshot(t *testing.T) {
	_, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		snapshot:  nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil snapshot")
}

type recursiveExporterAgent struct {
	*testAgent
	child agent.Agent
}

func (a *recursiveExporterAgent) Export(
	ctx context.Context,
	exportChild ChildExporter,
) (*Snapshot, error) {
	snapshot := &Snapshot{
		EntryNodeID: a.name,
		Nodes: []Node{
			{NodeID: a.name, Kind: NodeKindAgent, Name: a.name},
		},
	}
	if a.child == nil {
		return snapshot, nil
	}
	childSnapshot, err := exportChild(ctx, a.child)
	if err != nil {
		return nil, err
	}
	rebased, err := rebaseSnapshotForTest(childSnapshot, joinNodeIDForTest(a.name, a.child.Info().Name))
	if err != nil {
		return nil, err
	}
	snapshot.Nodes = append(snapshot.Nodes, rebased.Nodes...)
	snapshot.Edges = append(snapshot.Edges, Edge{
		FromNodeID: a.name,
		ToNodeID:   rebased.EntryNodeID,
	})
	return snapshot, nil
}

func TestExport_RecursivePointerChildFallsBackToOpaqueLeaf(t *testing.T) {
	root := &recursiveExporterAgent{testAgent: &testAgent{name: "root"}}
	root.child = root
	snapshot, err := Export(context.Background(), root)
	require.NoError(t, err)
	assert.Contains(t, snapshot.Nodes, Node{
		NodeID: "root/root",
		Kind:   NodeKindAgent,
		Name:   "root",
	})
	assert.Contains(t, snapshot.Edges, Edge{
		FromNodeID: "root",
		ToNodeID:   "root/root",
	})
}

func TestPointerIdentityHelpers_CoverFalseBranches(t *testing.T) {
	var nilAgent *testAgent
	assert.False(t, samePointerAgentInstance(valueExporterAgent{name: "a"}, valueExporterAgent{name: "a"}))
	assert.False(t, samePointerAgentInstance(&testAgent{name: "a"}, &customExporterAgent{
		testAgent: &testAgent{name: "a"},
	}))
	assert.False(t, samePointerAgentInstance(nilAgent, &testAgent{name: "a"}))
	state := &exportState{}
	state.pop()
	assert.False(t, state.containsRecursiveAgentInstance(&testAgent{name: "a"}))
}

func TestExport_ValueAgentsDoNotTriggerFalseRecursionFallback(t *testing.T) {
	child := valueExporterAgent{
		name:         "same",
		withSurface:  true,
		surfaceValue: "child",
	}
	parent := valueExporterAgent{
		name:         "same",
		child:        child,
		withSurface:  true,
		surfaceValue: "parent",
	}
	snapshot, err := Export(context.Background(), parent)
	require.NoError(t, err)
	assert.Contains(t, snapshot.Nodes, Node{
		NodeID: "same/same",
		Kind:   NodeKindAgent,
		Name:   "same",
	})
	assert.Contains(t, snapshot.Surfaces, Surface{
		SurfaceID: "same/same#instruction",
		NodeID:    "same/same",
		Type:      SurfaceTypeInstruction,
		Value:     SurfaceValue{Text: stringPtr("child")},
	})
}

func TestExport_PropagatesExporterError(t *testing.T) {
	want := errors.New("boom")
	_, err := Export(context.Background(), &customExporterAgent{
		testAgent: &testAgent{name: "root"},
		err:       want,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}

func stringPtr(value string) *string {
	return &value
}

func joinNodeIDForTest(parent string, localName string) string {
	return parent + "/" + escapeNodeIDSegment(localName)
}

func rebaseSnapshotForTest(snapshot *Snapshot, newRootNodeID string) (*Snapshot, error) {
	rebased := &Snapshot{
		EntryNodeID: newRootNodeID,
		Nodes:       make([]Node, 0, len(snapshot.Nodes)),
		Edges:       make([]Edge, 0, len(snapshot.Edges)),
		Surfaces:    make([]Surface, 0, len(snapshot.Surfaces)),
	}
	for _, node := range snapshot.Nodes {
		node.NodeID = rebaseNodeIDForTest(node.NodeID, snapshot.EntryNodeID, newRootNodeID)
		rebased.Nodes = append(rebased.Nodes, node)
	}
	for _, edge := range snapshot.Edges {
		rebased.Edges = append(rebased.Edges, Edge{
			FromNodeID: rebaseNodeIDForTest(edge.FromNodeID, snapshot.EntryNodeID, newRootNodeID),
			ToNodeID:   rebaseNodeIDForTest(edge.ToNodeID, snapshot.EntryNodeID, newRootNodeID),
		})
	}
	for _, surface := range snapshot.Surfaces {
		surface.NodeID = rebaseNodeIDForTest(surface.NodeID, snapshot.EntryNodeID, newRootNodeID)
		rebased.Surfaces = append(rebased.Surfaces, surface)
	}
	return rebased, nil
}

func rebaseNodeIDForTest(nodeID string, oldRoot string, newRoot string) string {
	if nodeID == oldRoot {
		return newRoot
	}
	return newRoot + strings.TrimPrefix(nodeID, oldRoot)
}
