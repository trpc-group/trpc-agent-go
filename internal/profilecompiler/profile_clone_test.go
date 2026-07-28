//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package profilecompiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

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
