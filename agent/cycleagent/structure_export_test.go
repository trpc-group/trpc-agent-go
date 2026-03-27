//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package cycleagent

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

func TestExport_CycleAgent_RootConnectsToFirstChild(t *testing.T) {
	cycle := New("root", WithSubAgents([]agent.Agent{
		&mockAgent{name: "first"},
		&mockAgent{name: "second"},
	}))
	snapshot, err := structure.Export(context.Background(), cycle)
	require.NoError(t, err)
	assertCycleSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/first", Kind: structure.NodeKindAgent, Name: "first"},
			{NodeID: "root/second", Kind: structure.NodeKindAgent, Name: "second"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/first"},
			{FromNodeID: "root/first", ToNodeID: "root/second"},
			{FromNodeID: "root/second", ToNodeID: "root"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_CycleAgent_MultiTerminalChildFansIntoNextChildAndBackToRoot(t *testing.T) {
	cycle := New("root", WithSubAgents([]agent.Agent{
		branchingExportAgent{name: "planner"},
		&mockAgent{name: "executor"},
	}))
	snapshot, err := structure.Export(context.Background(), cycle)
	require.NoError(t, err)
	assertCycleSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/executor", Kind: structure.NodeKindAgent, Name: "executor"},
			{NodeID: "root/planner", Kind: structure.NodeKindAgent, Name: "planner"},
			{NodeID: "root/planner/left", Kind: structure.NodeKindFunction, Name: "left"},
			{NodeID: "root/planner/right", Kind: structure.NodeKindFunction, Name: "right"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/planner"},
			{FromNodeID: "root/executor", ToNodeID: "root"},
			{FromNodeID: "root/planner", ToNodeID: "root/planner/left"},
			{FromNodeID: "root/planner", ToNodeID: "root/planner/right"},
			{FromNodeID: "root/planner/left", ToNodeID: "root/executor"},
			{FromNodeID: "root/planner/right", ToNodeID: "root/executor"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "root/planner#instruction",
				NodeID:    "root/planner",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: cycleTextPtr("plan")},
			},
		},
	})
}

func TestExport_CycleAgent_DuplicateChildNamesUseStableMountedPaths(t *testing.T) {
	cycle := New("root", WithSubAgents([]agent.Agent{
		&mockAgent{name: "worker"},
		&mockAgent{name: "worker"},
	}))
	snapshot, err := structure.Export(context.Background(), cycle)
	require.NoError(t, err)
	assertCycleSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/worker", Kind: structure.NodeKindAgent, Name: "worker"},
			{NodeID: "root/worker~2", Kind: structure.NodeKindAgent, Name: "worker"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/worker"},
			{FromNodeID: "root/worker", ToNodeID: "root/worker~2"},
			{FromNodeID: "root/worker~2", ToNodeID: "root"},
		},
		Surfaces: []structure.Surface{},
	})
}

type branchingExportAgent struct {
	name string
}

func (a branchingExportAgent) Info() agent.Info { return agent.Info{Name: a.name} }

func (a branchingExportAgent) Run(
	context.Context,
	*agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a branchingExportAgent) Tools() []tool.Tool { return nil }

func (a branchingExportAgent) SubAgents() []agent.Agent { return nil }

func (a branchingExportAgent) FindSubAgent(string) agent.Agent { return nil }

func (a branchingExportAgent) Export(
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
				Value:  structure.SurfaceValue{Text: cycleTextPtr("plan")},
			},
		},
	}, nil
}

func cycleTextPtr(value string) *string {
	return &value
}

func assertCycleSnapshotEqual(
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
