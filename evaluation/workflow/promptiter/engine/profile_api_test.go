//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCompileProfileNormalizesWithoutMutatingCallerProfile(t *testing.T) {
	baseline := "baseline"
	candidate := "candidate"
	snapshot := profileAPISnapshot(&baseline)
	input := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: "node#instruction",
		Value:     astructure.SurfaceValue{Text: &candidate},
	}}}

	normalized, runOptions, err := engine.CompileProfile(snapshot, input)

	require.NoError(t, err)
	require.Len(t, normalized.Overrides, 1)
	assert.Equal(t, "structure-1", normalized.StructureID)
	assert.Equal(t, "candidate", *normalized.Overrides[0].Value.Text)
	assert.Empty(t, input.StructureID)
	runConfig := agent.NewRunOptions(runOptions...)
	assert.True(t, runConfig.ExecutionTraceEnabled)
	attached := profilecompiler.ProfileFromRunOptions(runConfig)
	require.NotNil(t, attached)
	require.Len(t, attached.Overrides, 1)
	assert.Equal(t, "candidate", *attached.Overrides[0].Value.Text)

	*input.Overrides[0].Value.Text = "caller mutation"
	assert.Equal(t, "candidate", *normalized.Overrides[0].Value.Text)
}

func TestCompileProfileDoesNotAliasReturnedOrSnapshotToolSchemas(t *testing.T) {
	snapshot := profileAPIToolSnapshot()
	profile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: "node#tool.search",
		Value: astructure.SurfaceValue{Tools: []astructure.ToolRef{{
			ID: "search", Description: "candidate description",
		}}},
	}}}

	normalized, runOptions, err := engine.CompileProfile(snapshot, profile)
	require.NoError(t, err)
	attached := profilecompiler.ProfileFromRunOptions(agent.NewRunOptions(runOptions...))
	require.NotNil(t, attached)
	require.Len(t, normalized.Overrides, 1)
	require.Len(t, attached.Overrides, 1)

	normalized.Overrides[0].Value.Tools[0].InputSchema.Properties["query"].Description =
		"returned mutation"
	returnedSchema := normalized.Overrides[0].Value.Tools[0].InputSchema
	returnedSchema.Default.(map[string]string)["mode"] = "returned mutation"
	snapshotSchema := snapshot.Surfaces[0].Value.Tools[0].InputSchema
	snapshotSchema.Properties["query"].Description = "snapshot mutation"
	snapshotSchema.AdditionalProperties.([]string)[0] = "snapshot mutation"
	snapshotSchema.Enum[0].([]string)[0] = "snapshot mutation"
	snapshotSchema.Enum[1].(json.RawMessage)[0] = 'X'
	snapshotSchema.Enum[2].(*profileAPISchemaValue).Labels[0] = "snapshot mutation"
	snapshotSchema.Enum[2].(*profileAPISchemaValue).Metadata["mode"] = "snapshot mutation"

	attachedSchema := attached.Overrides[0].Value.Tools[0].InputSchema
	assert.Equal(t, "original query", attachedSchema.Properties["query"].Description)
	assert.Equal(t, "original", attachedSchema.Default.(map[string]string)["mode"])
	assert.Equal(t, "original", attachedSchema.AdditionalProperties.([]string)[0])
	assert.Equal(t, "original", attachedSchema.Enum[0].([]string)[0])
	assert.JSONEq(t, `{"mode":"original"}`, string(attachedSchema.Enum[1].(json.RawMessage)))
	assert.Equal(t, "original", attachedSchema.Enum[2].(*profileAPISchemaValue).Labels[0])
	assert.Equal(t, "original", attachedSchema.Enum[2].(*profileAPISchemaValue).Metadata["mode"])
}

func TestCompileProfileAcceptsNilAndEmptyProfiles(t *testing.T) {
	baseline := "baseline"
	for _, tt := range []struct {
		name    string
		profile *promptiter.Profile
	}{
		{name: "nil"},
		{name: "empty", profile: &promptiter.Profile{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			normalized, runOptions, err := engine.CompileProfile(
				profileAPISnapshot(&baseline), tt.profile,
			)

			require.NoError(t, err)
			assert.Equal(t, "structure-1", normalized.StructureID)
			assert.Empty(t, normalized.Overrides)
			assert.True(t, agent.NewRunOptions(runOptions...).ExecutionTraceEnabled)
		})
	}
}

func TestCompileProfileRejectsInvalidInputs(t *testing.T) {
	baseline := "baseline"
	tests := []struct {
		name     string
		snapshot *astructure.Snapshot
		profile  *promptiter.Profile
		want     string
	}{
		{name: "nil snapshot", want: "structure snapshot is nil"},
		{
			name: "structure mismatch", snapshot: profileAPISnapshot(&baseline),
			profile: &promptiter.Profile{StructureID: "other"},
			want:    `profile structure id "other" does not match structure id "structure-1"`,
		},
		{
			name: "duplicate override", snapshot: profileAPISnapshot(&baseline),
			profile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
				{SurfaceID: "node#instruction", Value: astructure.SurfaceValue{Text: stringPointer("one")}},
				{SurfaceID: "node#instruction", Value: astructure.SurfaceValue{Text: stringPointer("two")}},
			}},
			want: `duplicate profile override surface id "node#instruction"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, runOptions, err := engine.CompileProfile(tt.snapshot, tt.profile)

			assert.Nil(t, normalized)
			assert.Nil(t, runOptions)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func profileAPISnapshot(baseline *string) *astructure.Snapshot {
	return &astructure.Snapshot{
		StructureID: "structure-1",
		EntryNodeID: "node",
		Nodes:       []astructure.Node{{NodeID: "node", Kind: astructure.NodeKindLLM}},
		Surfaces: []astructure.Surface{{
			SurfaceID: "node#instruction",
			NodeID:    "node",
			Type:      astructure.SurfaceTypeInstruction,
			Value:     astructure.SurfaceValue{Text: baseline},
		}},
	}
}

func profileAPIToolSnapshot() *astructure.Snapshot {
	return &astructure.Snapshot{
		StructureID: "structure-1",
		EntryNodeID: "node",
		Nodes:       []astructure.Node{{NodeID: "node", Kind: astructure.NodeKindLLM}},
		Surfaces: []astructure.Surface{{
			SurfaceID: "node#tool.search",
			NodeID:    "node",
			Type:      astructure.SurfaceTypeTool,
			Value: astructure.SurfaceValue{Tools: []astructure.ToolRef{{
				ID:          "search",
				Description: "original description",
				InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{
					"query": {Type: "string", Description: "original query"},
				},
					Default:              map[string]string{"mode": "original"},
					AdditionalProperties: []string{"original"},
					Enum: []any{
						[]string{"original"},
						json.RawMessage(`{"mode":"original"}`),
						&profileAPISchemaValue{
							Labels:   []string{"original"},
							Metadata: map[string]string{"mode": "original"},
						},
					},
				},
			}}},
		}},
	}
}

type profileAPISchemaValue struct {
	Labels   []string          `json:"labels"`
	Metadata map[string]string `json:"metadata"`
}

func stringPointer(value string) *string {
	return &value
}
