//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graphagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type structureMockTool struct {
	name         string
	description  string
	inputSchema  *tool.Schema
	outputSchema *tool.Schema
}

func (t *structureMockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         t.name,
		Description:  t.description,
		InputSchema:  t.inputSchema,
		OutputSchema: t.outputSchema,
	}
}

type structureMockToolSet struct {
	name  string
	tools []tool.Tool
}

func (s *structureMockToolSet) Tools(context.Context) []tool.Tool { return s.tools }

func (s *structureMockToolSet) Close() error { return nil }

func (s *structureMockToolSet) Name() string { return s.name }

type countingToolSet struct {
	structureMockToolSet
	calls int
}

func (s *countingToolSet) Tools(ctx context.Context) []tool.Tool {
	s.calls++
	return s.structureMockToolSet.Tools(ctx)
}

type structureMockModel struct {
	name string
}

func (m *structureMockModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	close(ch)
	return ch, nil
}

func (m *structureMockModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func TestExport_GraphAgent_HasRootAgentNodeAndEntryEdge(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddNode("work", func(context.Context, graph.State) (any, error) { return nil, nil }).
		SetEntryPoint("work").
		SetFinishPoint("work").
		MustCompile()
	ag, err := New("assistant", compiled)
	require.NoError(t, err)

	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertGraphSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindAgent, Name: "assistant"},
			{NodeID: "assistant/work", Kind: structure.NodeKindFunction, Name: "work"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/work"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_GraphAgent_LLMNodeKindsAndSurfaces(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddLLMNode(
			"llm",
			&structureMockModel{name: "graph-model"},
			"analyze",
			map[string]tool.Tool{
				"beta":  &structureMockTool{name: "beta"},
				"alpha": &structureMockTool{name: "alpha"},
			},
		).
		SetEntryPoint("llm").
		SetFinishPoint("llm").
		MustCompile()
	ag, err := New("assistant", compiled)
	require.NoError(t, err)

	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertGraphSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindAgent, Name: "assistant"},
			{NodeID: "assistant/llm", Kind: structure.NodeKindLLM, Name: "llm"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/llm"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "assistant/llm#instruction",
				NodeID:    "assistant/llm",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: stringPtr("analyze")},
			},
			{
				SurfaceID: "assistant/llm#model",
				NodeID:    "assistant/llm",
				Type:      structure.SurfaceTypeModel,
				Value:     structure.SurfaceValue{Model: &structure.ModelRef{Name: "graph-model"}},
			},
			{
				SurfaceID: "assistant/llm#tool",
				NodeID:    "assistant/llm",
				Type:      structure.SurfaceTypeTool,
				Value:     structure.SurfaceValue{Tools: []structure.ToolRef{{ID: "alpha"}, {ID: "beta"}}},
			},
		},
	})
}

func TestExport_GraphAgent_ToolNodeSurface(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddToolsNode(
			"tools",
			map[string]tool.Tool{
				"echo": &structureMockTool{
					name:        "echo",
					description: "Echo tool.",
					inputSchema: &tool.Schema{
						Type:     "object",
						Required: []string{"message"},
						Properties: map[string]*tool.Schema{
							"message": {Type: "string"},
						},
					},
					outputSchema: &tool.Schema{Type: "string"},
				},
			},
		).
		SetEntryPoint("tools").
		SetFinishPoint("tools").
		MustCompile()
	ag, err := New("assistant", compiled)
	require.NoError(t, err)

	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertGraphSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindAgent, Name: "assistant"},
			{NodeID: "assistant/tools", Kind: structure.NodeKindTool, Name: "tools"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/tools"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "assistant/tools#tool",
				NodeID:    "assistant/tools",
				Type:      structure.SurfaceTypeTool,
				Value: structure.SurfaceValue{
					Tools: []structure.ToolRef{
						{
							ID:          "echo",
							Description: "Echo tool.",
							InputSchema: &tool.Schema{
								Type:     "object",
								Required: []string{"message"},
								Properties: map[string]*tool.Schema{
									"message": {Type: "string"},
								},
							},
							OutputSchema: &tool.Schema{Type: "string"},
						},
					},
				},
			},
		},
	})
}

func TestExport_GraphAgent_AgentNodeRecursesIntoChild(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddAgentNode("researcher").
		SetEntryPoint("researcher").
		SetFinishPoint("researcher").
		MustCompile()
	child := llmagent.New("researcher")
	ag, err := New("assistant", compiled, WithSubAgents([]agent.Agent{child}))
	require.NoError(t, err)

	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertGraphSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindAgent, Name: "assistant"},
			{NodeID: "assistant/researcher", Kind: structure.NodeKindAgent, Name: "researcher"},
			{NodeID: "assistant/researcher/researcher", Kind: structure.NodeKindLLM, Name: "researcher"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/researcher"},
			{FromNodeID: "assistant/researcher", ToNodeID: "assistant/researcher/researcher"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "assistant/researcher/researcher#global_instruction",
				NodeID:    "assistant/researcher/researcher",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: stringPtr("")},
			},
			{
				SurfaceID: "assistant/researcher/researcher#instruction",
				NodeID:    "assistant/researcher/researcher",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: stringPtr("")},
			},
		},
	})
}

func TestExport_GraphAgent_ConditionalEdgesExportPossibleTargets(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddLLMNode("llm", &structureMockModel{name: "graph-model"}, "analyze", nil).
		AddToolsNode("tools", map[string]tool.Tool{
			"echo": &structureMockTool{name: "echo"},
		}).
		AddNode("done", func(context.Context, graph.State) (any, error) { return nil, nil }).
		AddToolsConditionalEdges("llm", "tools", "done").
		SetEntryPoint("llm").
		SetFinishPoint("done").
		MustCompile()
	ag, err := New("assistant", compiled)
	require.NoError(t, err)

	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertGraphSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindAgent, Name: "assistant"},
			{NodeID: "assistant/done", Kind: structure.NodeKindFunction, Name: "done"},
			{NodeID: "assistant/llm", Kind: structure.NodeKindLLM, Name: "llm"},
			{NodeID: "assistant/tools", Kind: structure.NodeKindTool, Name: "tools"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/llm"},
			{FromNodeID: "assistant/llm", ToNodeID: "assistant/done"},
			{FromNodeID: "assistant/llm", ToNodeID: "assistant/tools"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "assistant/llm#instruction",
				NodeID:    "assistant/llm",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: stringPtr("analyze")},
			},
			{
				SurfaceID: "assistant/llm#model",
				NodeID:    "assistant/llm",
				Type:      structure.SurfaceTypeModel,
				Value:     structure.SurfaceValue{Model: &structure.ModelRef{Name: "graph-model"}},
			},
			{
				SurfaceID: "assistant/tools#tool",
				NodeID:    "assistant/tools",
				Type:      structure.SurfaceTypeTool,
				Value:     structure.SurfaceValue{Tools: []structure.ToolRef{{ID: "echo"}}},
			},
		},
	})
}

func TestExport_GraphAgent_ConditionalEdgesIncludeEndsTargets(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddNode(
			"route",
			func(context.Context, graph.State) (any, error) { return nil, nil },
			graph.WithEndsMap(map[string]string{"approved": "done"}),
		).
		AddNode("done", func(context.Context, graph.State) (any, error) { return nil, nil }).
		AddConditionalEdges("route", func(context.Context, graph.State) (string, error) {
			return "approved", nil
		}, nil).
		SetEntryPoint("route").
		SetFinishPoint("done").
		MustCompile()
	ag, err := New("assistant", compiled)
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertGraphSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindAgent, Name: "assistant"},
			{NodeID: "assistant/done", Kind: structure.NodeKindFunction, Name: "done"},
			{NodeID: "assistant/route", Kind: structure.NodeKindFunction, Name: "route"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/route"},
			{FromNodeID: "assistant/route", ToNodeID: "assistant/done"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_GraphAgent_EndsTargetsExportWithoutConditionalEdge(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddNode(
			"route",
			func(context.Context, graph.State) (any, error) { return nil, nil },
			graph.WithEndsMap(map[string]string{
				"approved": "done",
				"retry":    "done",
			}),
		).
		AddNode("done", func(context.Context, graph.State) (any, error) { return nil, nil }).
		SetEntryPoint("route").
		SetFinishPoint("done").
		MustCompile()
	ag, err := New("assistant", compiled)
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertGraphSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindAgent, Name: "assistant"},
			{NodeID: "assistant/done", Kind: structure.NodeKindFunction, Name: "done"},
			{NodeID: "assistant/route", Kind: structure.NodeKindFunction, Name: "route"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/route"},
			{FromNodeID: "assistant/route", ToNodeID: "assistant/done"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_GraphAgent_ComplexGraphCapturesFanOutJoinLoopAndConditionalTargets(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddNode("start", func(context.Context, graph.State) (any, error) { return nil, nil }).
		AddNode("prepare", func(context.Context, graph.State) (any, error) { return nil, nil }).
		AddLLMNode("llm", &structureMockModel{name: "graph-model"}, "analyze", nil).
		AddToolsNode("tools", map[string]tool.Tool{
			"echo": &structureMockTool{name: "echo"},
		}).
		AddNode("branch_a", func(context.Context, graph.State) (any, error) { return nil, nil }).
		AddNode("branch_b", func(context.Context, graph.State) (any, error) { return nil, nil }).
		AddNode(
			"join",
			func(context.Context, graph.State) (any, error) { return nil, nil },
			graph.WithEndsMap(map[string]string{
				"finish": "done",
				"retry":  "start",
			}),
		).
		AddNode("done", func(context.Context, graph.State) (any, error) { return nil, nil }).
		SetEntryPoint("start").
		AddEdge("start", "llm").
		AddEdge("start", "prepare").
		AddToolsConditionalEdges("llm", "tools", "branch_a").
		AddEdge("tools", "llm").
		AddEdge("prepare", "branch_b").
		AddJoinEdge([]string{"branch_a", "branch_b"}, "join").
		AddConditionalEdges("join", func(context.Context, graph.State) (string, error) {
			return "finish", nil
		}, nil).
		SetFinishPoint("done").
		MustCompile()
	ag, err := New("assistant", compiled)
	require.NoError(t, err)

	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertGraphSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindAgent, Name: "assistant"},
			{NodeID: "assistant/branch_a", Kind: structure.NodeKindFunction, Name: "branch_a"},
			{NodeID: "assistant/branch_b", Kind: structure.NodeKindFunction, Name: "branch_b"},
			{NodeID: "assistant/done", Kind: structure.NodeKindFunction, Name: "done"},
			{NodeID: "assistant/join", Kind: structure.NodeKindFunction, Name: "join"},
			{NodeID: "assistant/llm", Kind: structure.NodeKindLLM, Name: "llm"},
			{NodeID: "assistant/prepare", Kind: structure.NodeKindFunction, Name: "prepare"},
			{NodeID: "assistant/start", Kind: structure.NodeKindFunction, Name: "start"},
			{NodeID: "assistant/tools", Kind: structure.NodeKindTool, Name: "tools"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/start"},
			{FromNodeID: "assistant/branch_a", ToNodeID: "assistant/join"},
			{FromNodeID: "assistant/branch_b", ToNodeID: "assistant/join"},
			{FromNodeID: "assistant/join", ToNodeID: "assistant/done"},
			{FromNodeID: "assistant/join", ToNodeID: "assistant/start"},
			{FromNodeID: "assistant/llm", ToNodeID: "assistant/branch_a"},
			{FromNodeID: "assistant/llm", ToNodeID: "assistant/tools"},
			{FromNodeID: "assistant/prepare", ToNodeID: "assistant/branch_b"},
			{FromNodeID: "assistant/start", ToNodeID: "assistant/llm"},
			{FromNodeID: "assistant/start", ToNodeID: "assistant/prepare"},
			{FromNodeID: "assistant/tools", ToNodeID: "assistant/llm"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "assistant/llm#instruction",
				NodeID:    "assistant/llm",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: stringPtr("analyze")},
			},
			{
				SurfaceID: "assistant/llm#model",
				NodeID:    "assistant/llm",
				Type:      structure.SurfaceTypeModel,
				Value:     structure.SurfaceValue{Model: &structure.ModelRef{Name: "graph-model"}},
			},
			{
				SurfaceID: "assistant/tools#tool",
				NodeID:    "assistant/tools",
				Type:      structure.SurfaceTypeTool,
				Value:     structure.SurfaceValue{Tools: []structure.ToolRef{{ID: "echo"}}},
			},
		},
	})
}

func TestExport_GraphAgent_AgentNodeRequiresExistingSubAgent(t *testing.T) {
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddAgentNode("researcher").
		SetEntryPoint("researcher").
		SetFinishPoint("researcher").
		MustCompile()
	ag, err := New("assistant", compiled)
	require.NoError(t, err)
	_, err = structure.Export(context.Background(), ag)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sub-agent")
}

func TestExport_GraphAgent_StaticToolSetSurfaceDoesNotRefreshAtExport(t *testing.T) {
	toolSet := &countingToolSet{
		structureMockToolSet: structureMockToolSet{
			name: "dynamic",
			tools: []tool.Tool{
				&structureMockTool{name: "echo"},
			},
		},
	}
	compiled := graph.NewStateGraph(graph.NewStateSchema()).
		AddLLMNode(
			"llm",
			&structureMockModel{name: "graph-model"},
			"analyze",
			nil,
			graph.WithToolSets([]tool.ToolSet{toolSet}),
		).
		SetEntryPoint("llm").
		SetFinishPoint("llm").
		MustCompile()
	require.Equal(t, 1, toolSet.calls)
	ag, err := New("assistant", compiled)
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	require.Equal(t, 1, toolSet.calls)
	assert.Contains(t, snapshot.Surfaces, structure.Surface{
		SurfaceID: "assistant/llm#tool",
		NodeID:    "assistant/llm",
		Type:      structure.SurfaceTypeTool,
		Value: structure.SurfaceValue{
			Tools: []structure.ToolRef{{ID: "dynamic_echo"}},
		},
	})
}

func assertGraphSnapshotEqual(
	t *testing.T,
	got *structure.Snapshot,
	want *structure.Snapshot,
) {
	t.Helper()
	require.NotNil(t, got)
	require.NotEmpty(t, got.StructureID)
	normalized := *got
	normalized.StructureID = ""
	assert.Equal(t, *want, normalized)
}
