//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package profilecompiler

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCloneHelpersPreserveNilValues(t *testing.T) {
	assert.Nil(t, CloneProfile(nil))
	assert.Nil(t, CloneSnapshot(nil))
	assert.Nil(t, cloneProfileToolRefs(nil))
	assert.Nil(t, cloneToolSchema(nil))
	assert.Nil(t, cloneToolSchemaMap(nil))
	assert.Nil(t, cloneSchemaValues(nil))
	assert.Nil(t, cloneSchemaValue(nil))
}

func TestCloneProfilePreservesCompleteSurfaceValue(t *testing.T) {
	text := "candidate"
	syntax := astructure.PromptSyntaxDoubleBrace
	profile := &Profile{
		StructureID: "structure-1",
		Overrides: []SurfaceOverride{{
			SurfaceID: "node#tool.search",
			NodeID:    "node",
			Type:      astructure.SurfaceTypeTool,
			Value: astructure.SurfaceValue{
				Text:         &text,
				PromptSyntax: &syntax,
				FewShot: []astructure.FewShotExample{{Messages: []astructure.FewShotMessage{{
					Role: "user", Content: "example",
				}}}},
				Model: &astructure.ModelRef{
					Provider: "provider", Name: "model", Variant: "variant",
					BaseURL: "https://example.com", APIKey: "secret",
					Headers: map[string]string{"X-Test": "original"},
				},
				Tools: []astructure.ToolRef{{
					ID: "search", Description: "search tool",
					InputSchema:  completeCloneTestSchema(),
					OutputSchema: &tool.Schema{Type: "string", Description: "result"},
				}},
				Skills: []astructure.SkillRef{{ID: "skill", Description: "skill description"}},
			},
		}},
	}

	cloned := CloneProfile(profile)

	require.NotNil(t, cloned)
	assert.Equal(t, profile, cloned)
	profileValue := &profile.Overrides[0].Value
	clonedValue := cloned.Overrides[0].Value
	inputSchema := profileValue.Tools[0].InputSchema
	clonedInputSchema := clonedValue.Tools[0].InputSchema
	outputSchema := profileValue.Tools[0].OutputSchema
	clonedOutputSchema := clonedValue.Tools[0].OutputSchema
	assert.NotSame(t, profileValue.Text, clonedValue.Text)
	assert.NotSame(t, profileValue.Model, clonedValue.Model)
	assert.NotSame(t, &profileValue.FewShot[0], &clonedValue.FewShot[0])
	assert.NotSame(t, &profileValue.FewShot[0].Messages[0],
		&clonedValue.FewShot[0].Messages[0])
	assert.NotSame(t, &profileValue.Skills[0], &clonedValue.Skills[0])
	assert.NotSame(t, inputSchema, clonedInputSchema)
	assert.NotSame(t, outputSchema, clonedOutputSchema)

	profileValue.Model.Headers["X-Test"] = "mutated"
	profileValue.FewShot[0].Messages[0].Content = "mutated"
	profileValue.Skills[0].Description = "mutated"
	inputSchema.Required[0] = "mutated"
	inputSchema.Properties["query"].Description = "mutated"
	inputSchema.Items.Description = "mutated"
	inputSchema.AdditionalProperties.(map[string]any)["allowed"] = false
	inputSchema.Default.(map[string]any)["query"] = "mutated"
	inputSchema.Enum[0] = "mutated"
	inputSchema.Enum[1].(map[string]any)["value"] = "mutated"
	inputSchema.Defs["result"].Description = "mutated"
	outputSchema.Description = "mutated"

	assert.Equal(t, "original", clonedValue.Model.Headers["X-Test"])
	assert.Equal(t, "example", clonedValue.FewShot[0].Messages[0].Content)
	assert.Equal(t, "skill description", clonedValue.Skills[0].Description)
	assert.Equal(t, "query", clonedInputSchema.Required[0])
	assert.Equal(t, "query value", clonedInputSchema.Properties["query"].Description)
	assert.Equal(t, "item", clonedInputSchema.Items.Description)
	assert.Equal(t, true, clonedInputSchema.AdditionalProperties.(map[string]any)["allowed"])
	assert.Equal(t, "default", clonedInputSchema.Default.(map[string]any)["query"])
	assert.Equal(t, "one", clonedInputSchema.Enum[0])
	assert.Equal(t, "two", clonedInputSchema.Enum[1].(map[string]any)["value"])
	assert.Equal(t, "definition", clonedInputSchema.Defs["result"].Description)
	assert.Equal(t, "result", clonedOutputSchema.Description)
}

func TestCloneSnapshotDoesNotAliasMutableValues(t *testing.T) {
	text := "snapshot prompt"
	snapshot := &astructure.Snapshot{
		StructureID: "structure-1",
		EntryNodeID: "node",
		Nodes:       []astructure.Node{{NodeID: "node", Kind: astructure.NodeKindLLM, Name: "node"}},
		Edges:       []astructure.Edge{{FromNodeID: "node", ToNodeID: "next"}},
		Surfaces: []astructure.Surface{{
			SurfaceID: "node#instruction",
			NodeID:    "node",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &text,
				Tools: []astructure.ToolRef{{
					ID:          "search",
					InputSchema: completeCloneTestSchema(),
				}},
			},
		}},
	}

	cloned := CloneSnapshot(snapshot)

	require.NotNil(t, cloned)
	assert.Equal(t, snapshot, cloned)
	assert.NotSame(t, &snapshot.Nodes[0], &cloned.Nodes[0])
	assert.NotSame(t, &snapshot.Edges[0], &cloned.Edges[0])
	assert.NotSame(t, &snapshot.Surfaces[0], &cloned.Surfaces[0])
	assert.NotSame(t, snapshot.Surfaces[0].Value.Text, cloned.Surfaces[0].Value.Text)
	assert.NotSame(t, snapshot.Surfaces[0].Value.Tools[0].InputSchema,
		cloned.Surfaces[0].Value.Tools[0].InputSchema)

	snapshot.Nodes[0].Name = "mutated"
	snapshot.Edges[0].ToNodeID = "mutated"
	*snapshot.Surfaces[0].Value.Text = "mutated"
	snapshot.Surfaces[0].Value.Tools[0].InputSchema.Description = "mutated"

	assert.Equal(t, "node", cloned.Nodes[0].Name)
	assert.Equal(t, "next", cloned.Edges[0].ToNodeID)
	assert.Equal(t, "snapshot prompt", *cloned.Surfaces[0].Value.Text)
	assert.Equal(t, "request", cloned.Surfaces[0].Value.Tools[0].InputSchema.Description)
}

func TestCloneSchemaValuePreservesConcreteTypesAndOwnership(t *testing.T) {
	original := &cloneDynamicFixture{
		Interface: []string{"interface"},
		Pointer:   &cloneDynamicLeaf{Labels: []string{"pointer"}},
		Map:       map[string][]string{"map": {"value"}},
		Slice:     []map[string]string{{"slice": "value"}},
		Array:     [1][]string{{"array"}},
		Schema:    &tool.Schema{Type: "string", Enum: []any{"schema"}},
		private:   []string{"private"},
	}

	clonedValue := cloneSchemaValue(original)
	cloned, ok := clonedValue.(*cloneDynamicFixture)
	require.True(t, ok)
	assert.Equal(t, original, cloned)
	assert.NotSame(t, original, cloned)
	assert.NotSame(t, original.Pointer, cloned.Pointer)
	assert.NotSame(t, original.Schema, cloned.Schema)

	original.Interface.([]string)[0] = "mutated"
	original.Pointer.Labels[0] = "mutated"
	original.Map["map"][0] = "mutated"
	original.Slice[0]["slice"] = "mutated"
	original.Array[0][0] = "mutated"
	original.Schema.Enum[0] = "mutated"

	assert.Equal(t, "interface", cloned.Interface.([]string)[0])
	assert.Equal(t, "pointer", cloned.Pointer.Labels[0])
	assert.Equal(t, "value", cloned.Map["map"][0])
	assert.Equal(t, "value", cloned.Slice[0]["slice"])
	assert.Equal(t, "array", cloned.Array[0][0])
	assert.Equal(t, "schema", cloned.Schema.Enum[0])
}

func TestCloneDynamicValuePreservesTypedNilValues(t *testing.T) {
	assert.False(t, cloneDynamicValue(reflect.Value{}).IsValid())

	var interfaceValue any
	assert.True(t, cloneDynamicValue(reflect.ValueOf(&interfaceValue).Elem()).IsNil())

	var pointerValue *cloneDynamicLeaf
	assert.True(t, cloneDynamicValue(reflect.ValueOf(pointerValue)).IsNil())

	var schemaValue *tool.Schema
	assert.True(t, cloneDynamicValue(reflect.ValueOf(schemaValue)).IsNil())

	var mapValue map[string]string
	assert.True(t, cloneDynamicValue(reflect.ValueOf(mapValue)).IsNil())

	var sliceValue []string
	assert.True(t, cloneDynamicValue(reflect.ValueOf(sliceValue)).IsNil())
}

type cloneDynamicFixture struct {
	Interface any
	Pointer   *cloneDynamicLeaf
	Map       map[string][]string
	Slice     []map[string]string
	Array     [1][]string
	Schema    *tool.Schema
	private   []string
}

type cloneDynamicLeaf struct {
	Labels []string
}

func completeCloneTestSchema() *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: "request",
		Pattern:     "^request$",
		Required:    []string{"query"},
		Properties: map[string]*tool.Schema{
			"query": {Type: "string", Description: "query value"},
		},
		Items:                &tool.Schema{Type: "string", Description: "item"},
		AdditionalProperties: map[string]any{"allowed": true},
		Default:              map[string]any{"query": "default"},
		Enum:                 []any{"one", map[string]any{"value": "two"}},
		Ref:                  "#/$defs/result",
		Defs: map[string]*tool.Schema{
			"result": {Type: "string", Description: "definition"},
		},
	}
}
