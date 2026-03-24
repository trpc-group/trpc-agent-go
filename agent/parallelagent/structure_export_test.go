//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package parallelagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExport_ParallelAgent_RootConnectsToAllChildren(t *testing.T) {
	parallel := New("root", WithSubAgents([]agent.Agent{
		&mockAgent{name: "first"},
		&mockAgent{name: "second"},
	}))
	snapshot, err := structure.Export(context.Background(), parallel)
	require.NoError(t, err)
	assertParallelSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/first", Kind: structure.NodeKindAgent, Name: "first"},
			{NodeID: "root/second", Kind: structure.NodeKindAgent, Name: "second"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/first"},
			{FromNodeID: "root", ToNodeID: "root/second"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_ParallelAgent_ComplexChildKeepsInternalStructure(t *testing.T) {
	parallel := New("root", WithSubAgents([]agent.Agent{
		parallelStructuredAgent{name: "planner"},
		&mockAgent{name: "reviewer"},
	}))
	snapshot, err := structure.Export(context.Background(), parallel)
	require.NoError(t, err)
	assertParallelSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/planner", Kind: structure.NodeKindAgent, Name: "planner"},
			{NodeID: "root/planner/left", Kind: structure.NodeKindFunction, Name: "left"},
			{NodeID: "root/planner/right", Kind: structure.NodeKindFunction, Name: "right"},
			{NodeID: "root/reviewer", Kind: structure.NodeKindAgent, Name: "reviewer"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/planner"},
			{FromNodeID: "root", ToNodeID: "root/reviewer"},
			{FromNodeID: "root/planner", ToNodeID: "root/planner/left"},
			{FromNodeID: "root/planner", ToNodeID: "root/planner/right"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "root/planner#instruction",
				NodeID:    "root/planner",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: parallelTextPtr("plan")},
			},
		},
	})
}

func TestExport_ParallelAgent_DuplicateChildNamesUseStableMountedPaths(t *testing.T) {
	parallel := New("root", WithSubAgents([]agent.Agent{
		parallelStructuredAgent{name: "worker"},
		&mockAgent{name: "worker"},
	}))
	snapshot, err := structure.Export(context.Background(), parallel)
	require.NoError(t, err)
	assertParallelSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/worker", Kind: structure.NodeKindAgent, Name: "worker"},
			{NodeID: "root/worker/left", Kind: structure.NodeKindFunction, Name: "left"},
			{NodeID: "root/worker/right", Kind: structure.NodeKindFunction, Name: "right"},
			{NodeID: "root/worker~2", Kind: structure.NodeKindAgent, Name: "worker"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/worker"},
			{FromNodeID: "root", ToNodeID: "root/worker~2"},
			{FromNodeID: "root/worker", ToNodeID: "root/worker/left"},
			{FromNodeID: "root/worker", ToNodeID: "root/worker/right"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "root/worker#instruction",
				NodeID:    "root/worker",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: parallelTextPtr("plan")},
			},
		},
	})
}

type parallelStructuredAgent struct {
	name string
}

func (a parallelStructuredAgent) Info() agent.Info { return agent.Info{Name: a.name} }

func (a parallelStructuredAgent) Run(
	context.Context,
	*agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a parallelStructuredAgent) Tools() []tool.Tool { return nil }

func (a parallelStructuredAgent) SubAgents() []agent.Agent { return nil }

func (a parallelStructuredAgent) FindSubAgent(string) agent.Agent { return nil }

func (a parallelStructuredAgent) Export(
	context.Context,
	structure.ChildExporter,
) (*structure.Snapshot, error) {
	return &structure.Snapshot{
		EntryNodeID: a.name,
		Nodes: []structure.Node{
			{NodeID: a.name, Kind: structure.NodeKindAgent, Name: a.name},
			{NodeID: a.name + "/left", Kind: structure.NodeKindFunction, Name: "left"},
			{NodeID: a.name + "/right", Kind: structure.NodeKindFunction, Name: "right"},
		},
		Edges: []structure.Edge{
			{FromNodeID: a.name, ToNodeID: a.name + "/left"},
			{FromNodeID: a.name, ToNodeID: a.name + "/right"},
		},
		Surfaces: []structure.Surface{
			{
				NodeID: a.name,
				Type:   structure.SurfaceTypeInstruction,
				Value:  structure.SurfaceValue{Text: parallelTextPtr("plan")},
			},
		},
	}, nil
}

func parallelTextPtr(value string) *string {
	return &value
}

func assertParallelSnapshotEqual(
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
