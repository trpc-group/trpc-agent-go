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
	assert.NotSame(t, profile.Overrides[0].Value.Text, cloned.Overrides[0].Value.Text)
	assert.NotSame(t, profile.Overrides[0].Value.Model, cloned.Overrides[0].Value.Model)
	assert.NotSame(t, profile.Overrides[0].Value.Tools[0].InputSchema,
		cloned.Overrides[0].Value.Tools[0].InputSchema)

	profile.Overrides[0].Value.Model.Headers["X-Test"] = "mutated"
	profile.Overrides[0].Value.Tools[0].InputSchema.Required[0] = "mutated"
	profile.Overrides[0].Value.Tools[0].InputSchema.Properties["query"].Description = "mutated"
	profile.Overrides[0].Value.Tools[0].InputSchema.Items.Description = "mutated"
	profile.Overrides[0].Value.Tools[0].InputSchema.Defs["result"].Description = "mutated"

	clonedValue := cloned.Overrides[0].Value
	assert.Equal(t, "original", clonedValue.Model.Headers["X-Test"])
	assert.Equal(t, "query", clonedValue.Tools[0].InputSchema.Required[0])
	assert.Equal(t, "query value", clonedValue.Tools[0].InputSchema.Properties["query"].Description)
	assert.Equal(t, "item", clonedValue.Tools[0].InputSchema.Items.Description)
	assert.Equal(t, "definition", clonedValue.Tools[0].InputSchema.Defs["result"].Description)
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
