//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type mockSkillRepository struct {
	summaries []skill.Summary
}

func (r *mockSkillRepository) Summaries() []skill.Summary {
	out := make([]skill.Summary, len(r.summaries))
	copy(out, r.summaries)
	return out
}

func (r *mockSkillRepository) Get(string) (*skill.Skill, error) { return nil, nil }

func (r *mockSkillRepository) Path(string) (string, error) { return "", nil }

func TestExport_LLMAgent_BasicSnapshot(t *testing.T) {
	ag := New("assistant")
	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertLLMSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindLLM, Name: "assistant"},
		},
		Edges: []structure.Edge{},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "assistant#global_instruction",
				NodeID:    "assistant",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: textPtr("")},
			},
			{
				SurfaceID: "assistant#instruction",
				NodeID:    "assistant",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: textPtr("")},
			},
		},
	})
}

func TestExport_LLMAgent_ConfiguredSnapshot(t *testing.T) {
	subOne := New("sub")
	subTwo := &mockAgent{name: "sub"}
	ag := New(
		"assistant",
		WithInstruction("solve"),
		WithGlobalInstruction("system"),
		WithModel(&mockModelWithResponse{}),
		WithTools([]tool.Tool{
			&mockTool{name: "beta"},
			&mockTool{
				name:        "alpha",
				description: "Alpha tool.",
				inputSchema: &tool.Schema{
					Type:     "object",
					Required: []string{"query"},
					Properties: map[string]*tool.Schema{
						"query": {Type: "string"},
					},
				},
				outputSchema: &tool.Schema{Type: "string"},
			},
		}),
		WithSkills(&mockSkillRepository{
			summaries: []skill.Summary{
				{Name: "writer", Description: "Write polished responses."},
				{Name: "planner", Description: "Plan the next steps."},
			},
		}),
		WithSubAgents([]agent.Agent{subOne, subTwo}),
	)
	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	assertLLMSnapshotEqual(t, snapshot, &structure.Snapshot{
		EntryNodeID: "assistant",
		Nodes: []structure.Node{
			{NodeID: "assistant", Kind: structure.NodeKindLLM, Name: "assistant"},
			{NodeID: "assistant/sub", Kind: structure.NodeKindLLM, Name: "sub"},
			{NodeID: "assistant/sub~2", Kind: structure.NodeKindAgent, Name: "sub"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "assistant", ToNodeID: "assistant/sub"},
			{FromNodeID: "assistant", ToNodeID: "assistant/sub~2"},
		},
		Surfaces: []structure.Surface{
			{
				SurfaceID: "assistant#global_instruction",
				NodeID:    "assistant",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: textPtr("system")},
			},
			{
				SurfaceID: "assistant#instruction",
				NodeID:    "assistant",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: textPtr("solve")},
			},
			{
				SurfaceID: "assistant#model",
				NodeID:    "assistant",
				Type:      structure.SurfaceTypeModel,
				Value:     structure.SurfaceValue{Model: &structure.ModelRef{Name: "mock-model"}},
			},
			{
				SurfaceID: "assistant#skill",
				NodeID:    "assistant",
				Type:      structure.SurfaceTypeSkill,
				Value: structure.SurfaceValue{
					Skills: []structure.SkillRef{
						{ID: "planner", Description: "Plan the next steps."},
						{ID: "writer", Description: "Write polished responses."},
					},
				},
			},
			{
				SurfaceID: "assistant#tool",
				NodeID:    "assistant",
				Type:      structure.SurfaceTypeTool,
				Value: structure.SurfaceValue{
					Tools: []structure.ToolRef{
						{
							ID:          "alpha",
							Description: "Alpha tool.",
							InputSchema: &tool.Schema{
								Type:     "object",
								Required: []string{"query"},
								Properties: map[string]*tool.Schema{
									"query": {Type: "string"},
								},
							},
							OutputSchema: &tool.Schema{Type: "string"},
						},
						{ID: "beta"},
					},
				},
			},
			{
				SurfaceID: "assistant/sub#global_instruction",
				NodeID:    "assistant/sub",
				Type:      structure.SurfaceTypeGlobalInstruction,
				Value:     structure.SurfaceValue{Text: textPtr("")},
			},
			{
				SurfaceID: "assistant/sub#instruction",
				NodeID:    "assistant/sub",
				Type:      structure.SurfaceTypeInstruction,
				Value:     structure.SurfaceValue{Text: textPtr("")},
			},
		},
	})
}

func assertLLMSnapshotEqual(
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

func textPtr(value string) *string {
	return &value
}
