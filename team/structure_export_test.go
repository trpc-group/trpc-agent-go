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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExport_TeamCoordinator_RootConnectsOnlyToCoordinator(t *testing.T) {
	coordinator := &testCoordinator{name: "team"}
	member := &testSwarmMember{name: "researcher"}
	tm, err := New(coordinator, []agent.Agent{member})
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), tm)
	require.NoError(t, err)
	assertTeamSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "team",
		Nodes: []structure.Node{
			{NodeID: "team", Kind: structure.NodeKindAgent, Name: "team"},
			{NodeID: "team/coordinator", Kind: structure.NodeKindAgent, Name: "team"},
			{NodeID: "team/researcher", Kind: structure.NodeKindAgent, Name: "researcher"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "team", ToNodeID: "team/coordinator"},
			{FromNodeID: "team/coordinator", ToNodeID: "team/researcher"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_TeamCoordinator_MembersHangFromCoordinatorRoot(t *testing.T) {
	coordinator := llmagent.New("team", llmagent.WithSubAgents([]agent.Agent{
		llmagent.New("delegate"),
	}))
	member := llmagent.New("researcher")
	tm, err := New(coordinator, []agent.Agent{member})
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), tm)
	require.NoError(t, err)
	assertTeamSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "team",
		Nodes: []structure.Node{
			{NodeID: "team", Kind: structure.NodeKindAgent, Name: "team"},
			{NodeID: "team/coordinator", Kind: structure.NodeKindLLM, Name: "team"},
			{NodeID: "team/coordinator/delegate", Kind: structure.NodeKindLLM, Name: "delegate"},
			{NodeID: "team/researcher", Kind: structure.NodeKindLLM, Name: "researcher"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "team", ToNodeID: "team/coordinator"},
			{FromNodeID: "team/coordinator", ToNodeID: "team/coordinator/delegate"},
			{FromNodeID: "team/coordinator", ToNodeID: "team/researcher"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "team/coordinator#global_instruction",
				NodeID:    "team/coordinator",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/coordinator#instruction",
				NodeID:    "team/coordinator",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/coordinator#tool",
				NodeID:    "team/coordinator",
				Type:      structure.SurfaceTypeTool,
				Value: structure.SurfaceValue{
					Tools: []structure.ToolRef{teamMemberToolRef("team-members-team_researcher")},
				},
			},
			{
				SurfaceID: "team/coordinator/delegate#global_instruction",
				NodeID:    "team/coordinator/delegate",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/coordinator/delegate#instruction",
				NodeID:    "team/coordinator/delegate",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/researcher#global_instruction",
				NodeID:    "team/researcher",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/researcher#instruction",
				NodeID:    "team/researcher",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
		},
	})
}

func TestExport_TeamCoordinator_CoordinatorNamespaceAvoidsMemberCollision(t *testing.T) {
	coordinator := llmagent.New("team", llmagent.WithSubAgents([]agent.Agent{
		llmagent.New("researcher"),
	}))
	member := llmagent.New("researcher")
	tm, err := New(coordinator, []agent.Agent{member})
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), tm)
	require.NoError(t, err)
	assertTeamSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "team",
		Nodes: []structure.Node{
			{NodeID: "team", Kind: structure.NodeKindAgent, Name: "team"},
			{NodeID: "team/coordinator", Kind: structure.NodeKindLLM, Name: "team"},
			{NodeID: "team/coordinator/researcher", Kind: structure.NodeKindLLM, Name: "researcher"},
			{NodeID: "team/researcher", Kind: structure.NodeKindLLM, Name: "researcher"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "team", ToNodeID: "team/coordinator"},
			{FromNodeID: "team/coordinator", ToNodeID: "team/coordinator/researcher"},
			{FromNodeID: "team/coordinator", ToNodeID: "team/researcher"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "team/coordinator#global_instruction",
				NodeID:    "team/coordinator",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/coordinator#instruction",
				NodeID:    "team/coordinator",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/coordinator#tool",
				NodeID:    "team/coordinator",
				Type:      structure.SurfaceTypeTool,
				Value: structure.SurfaceValue{
					Tools: []structure.ToolRef{teamMemberToolRef("team-members-team_researcher")},
				},
			},
			{
				SurfaceID: "team/coordinator/researcher#global_instruction",
				NodeID:    "team/coordinator/researcher",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/coordinator/researcher#instruction",
				NodeID:    "team/coordinator/researcher",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/researcher#global_instruction",
				NodeID:    "team/researcher",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "team/researcher#instruction",
				NodeID:    "team/researcher",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
		},
	})
}

func TestExport_TeamSwarm_RootConnectsToEntryMember(t *testing.T) {
	first := &testSwarmMember{name: "alpha"}
	second := &testSwarmMember{name: "beta"}
	tm, err := NewSwarm("swarm", "alpha", []agent.Agent{first, second})
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), tm)
	require.NoError(t, err)
	assertTeamSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "swarm",
		Nodes: []structure.Node{
			{NodeID: "swarm", Kind: structure.NodeKindAgent, Name: "swarm"},
			{NodeID: "swarm/alpha", Kind: structure.NodeKindAgent, Name: "alpha"},
			{NodeID: "swarm/beta", Kind: structure.NodeKindAgent, Name: "beta"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "swarm", ToNodeID: "swarm/alpha"},
			{FromNodeID: "swarm/alpha", ToNodeID: "swarm/beta"},
			{FromNodeID: "swarm/beta", ToNodeID: "swarm/alpha"},
		},
		Surfaces: []structure.Surface{},
	})
}

func TestExport_TeamSwarm_DoesNotRecursivelyExpandMemberRoster(t *testing.T) {
	first := llmagent.New("alpha")
	second := llmagent.New("beta")
	tm, err := NewSwarm("swarm", "alpha", []agent.Agent{first, second})
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), tm)
	require.NoError(t, err)
	assertTeamSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "swarm",
		Nodes: []structure.Node{
			{NodeID: "swarm", Kind: structure.NodeKindAgent, Name: "swarm"},
			{NodeID: "swarm/alpha", Kind: structure.NodeKindLLM, Name: "alpha"},
			{NodeID: "swarm/beta", Kind: structure.NodeKindLLM, Name: "beta"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "swarm", ToNodeID: "swarm/alpha"},
			{FromNodeID: "swarm/alpha", ToNodeID: "swarm/beta"},
			{FromNodeID: "swarm/beta", ToNodeID: "swarm/alpha"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "swarm/alpha#global_instruction",
				NodeID:    "swarm/alpha",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "swarm/alpha#instruction",
				NodeID:    "swarm/alpha",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "swarm/beta#global_instruction",
				NodeID:    "swarm/beta",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "swarm/beta#instruction",
				NodeID:    "swarm/beta",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
		},
	})
}

func TestExport_TeamSwarm_ThreeMembersFormDirectedCompleteGraph(t *testing.T) {
	first := llmagent.New("alpha")
	second := llmagent.New("beta")
	third := llmagent.New("gamma")
	tm, err := NewSwarm("swarm", "alpha", []agent.Agent{first, second, third})
	require.NoError(t, err)
	snapshot, err := structure.Export(context.Background(), tm)
	require.NoError(t, err)
	assertTeamSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "swarm",
		Nodes: []structure.Node{
			{NodeID: "swarm", Kind: structure.NodeKindAgent, Name: "swarm"},
			{NodeID: "swarm/alpha", Kind: structure.NodeKindLLM, Name: "alpha"},
			{NodeID: "swarm/beta", Kind: structure.NodeKindLLM, Name: "beta"},
			{NodeID: "swarm/gamma", Kind: structure.NodeKindLLM, Name: "gamma"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "swarm", ToNodeID: "swarm/alpha"},
			{FromNodeID: "swarm/alpha", ToNodeID: "swarm/beta"},
			{FromNodeID: "swarm/alpha", ToNodeID: "swarm/gamma"},
			{FromNodeID: "swarm/beta", ToNodeID: "swarm/alpha"},
			{FromNodeID: "swarm/beta", ToNodeID: "swarm/gamma"},
			{FromNodeID: "swarm/gamma", ToNodeID: "swarm/alpha"},
			{FromNodeID: "swarm/gamma", ToNodeID: "swarm/beta"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "swarm/alpha#global_instruction",
				NodeID:    "swarm/alpha",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "swarm/alpha#instruction",
				NodeID:    "swarm/alpha",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "swarm/beta#global_instruction",
				NodeID:    "swarm/beta",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "swarm/beta#instruction",
				NodeID:    "swarm/beta",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "swarm/gamma#global_instruction",
				NodeID:    "swarm/gamma",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
			{
				SurfaceID: "swarm/gamma#instruction",
				NodeID:    "swarm/gamma",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: teamTextPtr("")},
			},
		},
	})
}

func assertTeamSnapshotEqual(
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

func teamTextPtr(value string) *string {
	return &value
}

func teamMemberToolRef(id string) structure.ToolRef {
	return structure.ToolRef{
		ID:          id,
		InputSchema: &tool.Schema{Type: "object", Description: "Input for the agent tool", Required: []string{"request"}, Properties: map[string]*tool.Schema{"request": {Type: "string", Description: "The request to send to the agent"}}},
		OutputSchema: &tool.Schema{
			Type:        "string",
			Description: "The response from the agent",
		},
	}
}
