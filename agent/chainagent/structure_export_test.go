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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExport_ChainAgent_RootConnectsToFirstChild(t *testing.T) {
	chain := New("root", WithSubAgents([]agent.Agent{
		&mockAgent{name: "first"},
		&mockAgent{name: "second"},
	}))
	snapshot, err := structure.Export(context.Background(), chain)
	require.NoError(t, err)
	assertChainSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/first", Kind: structure.NodeKindAgent, Name: "first"},
			{NodeID: "root/second", Kind: structure.NodeKindAgent, Name: "second"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/first"},
			{FromNodeID: "root/first", ToNodeID: "root/second"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_ChainAgent_CyclicChildProducesNoContinuationEdge(t *testing.T) {
	loop := cyclicExportAgent{name: "loop"}
	tail := &mockAgent{name: "tail"}
	chain := New("root", WithSubAgents([]agent.Agent{loop, tail}))
	snapshot, err := structure.Export(context.Background(), chain)
	require.NoError(t, err)
	assertChainSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/loop", Kind: structure.NodeKindAgent, Name: "loop"},
			{NodeID: "root/loop/a", Kind: structure.NodeKindFunction, Name: "a"},
			{NodeID: "root/loop/b", Kind: structure.NodeKindFunction, Name: "b"},
			{NodeID: "root/tail", Kind: structure.NodeKindAgent, Name: "tail"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/loop"},
			{FromNodeID: "root/loop", ToNodeID: "root/loop/a"},
			{FromNodeID: "root/loop/a", ToNodeID: "root/loop/b"},
			{FromNodeID: "root/loop/b", ToNodeID: "root/loop/a"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_ChainAgent_CyclicChildWithExitConnectsToTail(t *testing.T) {
	loop := cyclicExportAgent{name: "loop", withExit: true}
	tail := &mockAgent{name: "tail"}
	chain := New("root", WithSubAgents([]agent.Agent{loop, tail}))
	snapshot, err := structure.Export(context.Background(), chain)
	require.NoError(t, err)
	assertChainSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/loop", Kind: structure.NodeKindAgent, Name: "loop"},
			{NodeID: "root/loop/a", Kind: structure.NodeKindFunction, Name: "a"},
			{NodeID: "root/loop/b", Kind: structure.NodeKindFunction, Name: "b"},
			{NodeID: "root/loop/done", Kind: structure.NodeKindFunction, Name: "done"},
			{NodeID: "root/tail", Kind: structure.NodeKindAgent, Name: "tail"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/loop"},
			{FromNodeID: "root/loop", ToNodeID: "root/loop/a"},
			{FromNodeID: "root/loop/a", ToNodeID: "root/loop/b"},
			{FromNodeID: "root/loop/b", ToNodeID: "root/loop/a"},
			{FromNodeID: "root/loop/b", ToNodeID: "root/loop/done"},
			{FromNodeID: "root/loop/done", ToNodeID: "root/tail"},
		},
		Surfaces: []structure.Surface{},
	})
}

type cyclicExportAgent struct {
	name     string
	withExit bool
}

func (a cyclicExportAgent) Info() agent.Info { return agent.Info{Name: a.name} }

func (a cyclicExportAgent) Run(
	context.Context,
	*agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a cyclicExportAgent) Tools() []tool.Tool { return nil }

func (a cyclicExportAgent) SubAgents() []agent.Agent { return nil }

func (a cyclicExportAgent) FindSubAgent(string) agent.Agent { return nil }

func (a cyclicExportAgent) Export(
	context.Context,
	structure.ChildExporter,
) (*structure.Snapshot, error) {
	snapshot := &structure.Snapshot{
		EntryNodeID: a.name,
		Nodes: []structure.Node{
			{NodeID: a.name, Kind: structure.NodeKindAgent, Name: a.name},
			{NodeID: a.name + "/a", Kind: structure.NodeKindFunction, Name: "a"},
			{NodeID: a.name + "/b", Kind: structure.NodeKindFunction, Name: "b"},
		},
		Edges: []structure.Edge{
			{FromNodeID: a.name, ToNodeID: a.name + "/a"},
			{FromNodeID: a.name + "/a", ToNodeID: a.name + "/b"},
			{FromNodeID: a.name + "/b", ToNodeID: a.name + "/a"},
		},
	}
	if a.withExit {
		snapshot.Nodes = append(snapshot.Nodes, structure.Node{
			NodeID: a.name + "/done",
			Kind:   structure.NodeKindFunction,
			Name:   "done",
		})
		snapshot.Edges = append(snapshot.Edges, structure.Edge{
			FromNodeID: a.name + "/b",
			ToNodeID:   a.name + "/done",
		})
	}
	return snapshot, nil
}

func assertChainSnapshotEqual(
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
