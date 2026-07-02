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

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestIsSupportedType(t *testing.T) {
	assert.True(t, IsSupportedType(astructure.SurfaceTypeInstruction))
	assert.True(t, IsSupportedType(astructure.SurfaceTypeGlobalInstruction))
	assert.True(t, IsSupportedType(astructure.SurfaceTypeFewShot))
	assert.True(t, IsSupportedType(astructure.SurfaceTypeTool))
	assert.False(t, IsSupportedType(astructure.SurfaceTypeModel))
	assert.False(t, IsSupportedType(astructure.SurfaceType("unknown")))
}

func TestValidateValue(t *testing.T) {
	text := "instruction"

	assert.NoError(t, validateValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{Text: &text},
	))
	assert.NoError(t, validateValue(
		astructure.SurfaceTypeGlobalInstruction,
		astructure.SurfaceValue{Text: &text},
	))
	assert.NoError(t, validateValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{FewShot: []astructure.FewShotExample{}},
	))
	assert.NoError(t, validateValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Tools: []astructure.ToolRef{{ID: "lookup"}},
		},
	))
	assert.Error(t, validateValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{},
	))
	assert.Error(t, validateValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{Text: &text},
	))
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{Model: &astructure.ModelRef{}},
	), `surface type "model" is invalid`)
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Model: &astructure.ModelRef{},
			Tools: []astructure.ToolRef{{ID: "lookup"}},
		},
	), "model is not nil")
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{},
	), "tools must contain exactly one tool, got 0")
}

func TestBuildIndex(t *testing.T) {
	text := "instruction"

	index, err := BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &text,
			},
		},
	})

	assert.NoError(t, err)
	assert.Len(t, index, 1)
	assert.Equal(t, "surf_1", index["surf_1"].SurfaceID)
}

func TestBuildIndexRejectsInvalidSurface(t *testing.T) {
	text := "instruction"

	_, err := BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &text,
			},
		},
		{
			SurfaceID: "surf_1",
			NodeID:    "node_2",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &text,
			},
		},
	})

	assert.ErrorContains(t, err, `duplicate surface id "surf_1"`)
}

func TestSanitizeValue(t *testing.T) {
	text := "instruction"

	sanitizedInstruction, err := sanitizeValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text: &text,
		},
	)
	assert.NoError(t, err)
	assert.Equal(t, &text, sanitizedInstruction.Text)
	assert.Nil(t, sanitizedInstruction.Model)
	sanitizedGlobalInstruction, err := sanitizeValue(
		astructure.SurfaceTypeGlobalInstruction,
		astructure.SurfaceValue{
			Text: &text,
		},
	)
	assert.NoError(t, err)
	assert.Equal(t, &text, sanitizedGlobalInstruction.Text)
	assert.Nil(t, sanitizedGlobalInstruction.Model)

	sanitizedFewShot, err := sanitizeValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{
			FewShot: []astructure.FewShotExample{},
		},
	)
	assert.NoError(t, err)
	assert.Nil(t, sanitizedFewShot.Text)
	assert.Nil(t, sanitizedFewShot.Model)
	assert.NotNil(t, sanitizedFewShot.FewShot)

	toolRefs := []astructure.ToolRef{{ID: "lookup", Description: "original"}}
	sanitizedTool, err := sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{Tools: toolRefs},
	)
	assert.NoError(t, err)
	toolRefs[0].Description = "changed"
	assert.Equal(t, "original", sanitizedTool.Tools[0].Description)
	sanitizedTool.Tools[0].Description = "returned"
	assert.Equal(t, "changed", toolRefs[0].Description)
}

func TestSanitizeValueRejectsInvalidInput(t *testing.T) {
	text := "instruction"

	_, err := sanitizeValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{},
	)
	assert.Error(t, err)

	_, err = sanitizeValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{Text: &text},
	)
	assert.EqualError(t, err, "text is not nil")

	_, err = sanitizeValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{Model: &astructure.ModelRef{}},
	)
	assert.EqualError(t, err, `surface type "model" is invalid`)
	emptyText := ""
	_, err = sanitizeValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:         &text,
			PromptSyntax: promptSyntaxPtr(astructure.PromptSyntaxSingleBrace),
		},
	)
	assert.EqualError(t, err, "prompt syntax is not nil")
	_, err = sanitizeValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:  &text,
			Model: &astructure.ModelRef{},
		},
	)
	assert.EqualError(t, err, "model is not nil")
	_, err = sanitizeValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:  &text,
			Tools: []astructure.ToolRef{{ID: "lookup"}},
		},
	)
	assert.EqualError(t, err, "tools are not empty")
	_, err = sanitizeValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:   &text,
			Skills: []astructure.SkillRef{{ID: "skill"}},
		},
	)
	assert.EqualError(t, err, "skills are not empty")
	_, err = sanitizeValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{
			FewShot: []astructure.FewShotExample{},
			Model:   &astructure.ModelRef{},
		},
	)
	assert.EqualError(t, err, "model is not nil")
	_, err = sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Text: &emptyText,
			Tools: []astructure.ToolRef{
				{ID: "lookup"},
			},
		},
	)
	assert.EqualError(t, err, "text is not nil")
	_, err = sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Model: &astructure.ModelRef{},
			Tools: []astructure.ToolRef{
				{ID: "lookup"},
			},
		},
	)
	assert.EqualError(t, err, "model is not nil")
	_, err = sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{},
	)
	assert.EqualError(t, err, "tools must contain exactly one tool, got 0")
	_, err = sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Tools: []astructure.ToolRef{
				{ID: "lookup"},
				{ID: "delay"},
			},
		},
	)
	assert.EqualError(t, err, "tools must contain exactly one tool, got 2")
	_, err = sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Tools: []astructure.ToolRef{{}},
		},
	)
	assert.EqualError(t, err, "tool id is empty")
	_, err = sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Tools: []astructure.ToolRef{
				{ID: "lookup"},
				{ID: "lookup"},
			},
		},
	)
	assert.EqualError(t, err, `duplicate tool id "lookup"`)
}

func TestSanitizePatchValueAllowsToolDescriptionOnlyChanges(t *testing.T) {
	baseline := testToolSurface()
	candidate := astructure.SurfaceValue{
		Tools: []astructure.ToolRef{
			{
				ID:          "lookup",
				Description: "Look up a travel record by key.",
			},
		},
	}
	sanitized, err := SanitizePatchValue(baseline, candidate)
	assert.NoError(t, err)
	assert.Equal(t, "Look up a travel record by key.", sanitized.Tools[0].Description)
	assert.Same(t, baseline.Value.Tools[0].InputSchema, sanitized.Tools[0].InputSchema)
	assert.Same(t, baseline.Value.Tools[0].OutputSchema, sanitized.Tools[0].OutputSchema)
}

func TestSanitizePatchValueRejectsToolSchemaChanges(t *testing.T) {
	tests := []struct {
		name  string
		value astructure.SurfaceValue
	}{
		{
			name: "renamed tool",
			value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
				{ID: "search", Description: "new"},
			}},
		},
		{
			name: "changed required",
			value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
				{
					ID:          "lookup",
					Description: "new",
					InputSchema: &tool.Schema{
						Type:     "object",
						Required: []string{"query", "extra"},
						Properties: map[string]*tool.Schema{
							"query": {Type: "string"},
						},
					},
				},
			}},
		},
		{
			name: "changed property type",
			value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
				{
					ID:          "lookup",
					Description: "new",
					InputSchema: &tool.Schema{
						Type:     "object",
						Required: []string{"query"},
						Properties: map[string]*tool.Schema{
							"query": {Type: "number"},
						},
					},
				},
			}},
		},
		{
			name:  "changed input schema description",
			value: toolPatchWithInputSchema(testLookupInputSchema("changed")),
		},
		{
			name: "changed output schema description",
			value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
				{
					ID:           "lookup",
					Description:  "new",
					OutputSchema: testLookupOutputSchema("changed"),
				},
			}},
		},
		{
			name: "changed output schema shape",
			value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
				{
					ID:          "lookup",
					Description: "new",
					InputSchema: testLookupInputSchema("Lookup request."),
					OutputSchema: &tool.Schema{
						Type: "string",
					},
				},
			}},
		},
		{
			name: "changed enum",
			value: toolPatchWithInputSchema(mutatedLookupInputSchema(func(schema *tool.Schema) {
				schema.Properties["query"].Enum = []any{"A", "B"}
			})),
		},
		{
			name: "changed default",
			value: toolPatchWithInputSchema(mutatedLookupInputSchema(func(schema *tool.Schema) {
				schema.Properties["query"].Default = "A"
			})),
		},
		{
			name: "changed items type",
			value: toolPatchWithInputSchema(mutatedLookupInputSchema(func(schema *tool.Schema) {
				schema.Properties["filters"].Items.Type = "number"
			})),
		},
		{
			name: "changed additional properties",
			value: toolPatchWithInputSchema(mutatedLookupInputSchema(func(schema *tool.Schema) {
				schema.AdditionalProperties = true
			})),
		},
		{
			name: "changed ref",
			value: toolPatchWithInputSchema(mutatedLookupInputSchema(func(schema *tool.Schema) {
				schema.Ref = "#/$defs/record"
			})),
		},
		{
			name: "changed defs type",
			value: toolPatchWithInputSchema(mutatedLookupInputSchema(func(schema *tool.Schema) {
				schema.Defs["record"].Type = "array"
			})),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SanitizePatchValue(testToolSurface(), tt.value)
			assert.Error(t, err)
		})
	}
}

func TestSanitizePatchValueRejectsToolShapeChanges(t *testing.T) {
	_, err := SanitizePatchValue(testToolSurface(), astructure.SurfaceValue{
		Tools: []astructure.ToolRef{},
	})
	assert.EqualError(t, err, "tools must contain exactly one tool, got 0")
	_, err = SanitizePatchValue(testToolSurface(), astructure.SurfaceValue{
		Tools: []astructure.ToolRef{
			{ID: "lookup", Description: "new"},
			{ID: "delay", Description: "new"},
		},
	})
	assert.EqualError(t, err, "tools must contain exactly one tool, got 2")
	_, err = SanitizePatchValue(testToolSurface(), astructure.SurfaceValue{
		Tools:        []astructure.ToolRef{{ID: "lookup", Description: "new"}},
		PromptSyntax: promptSyntaxPtr(astructure.PromptSyntaxSingleBrace),
	})
	assert.EqualError(t, err, "prompt syntax is not nil")
}

func TestSanitizeToolRefsDescriptionOnlyRejectsInvalidBaseline(t *testing.T) {
	tools, err := sanitizeToolRefsDescriptionOnly(
		[]astructure.ToolRef{{}},
		[]astructure.ToolRef{{ID: "lookup"}},
	)
	assert.Nil(t, tools)
	assert.EqualError(t, err, "validate baseline tools: tool id is empty")
	tools, err = sanitizeToolRefsDescriptionOnly(
		[]astructure.ToolRef{{ID: "lookup"}},
		[]astructure.ToolRef{
			{ID: "lookup"},
			{ID: "delay"},
		},
	)
	assert.Nil(t, tools)
	assert.EqualError(t, err, "tool count changed from 1 to 2")
}

func promptSyntaxPtr(value astructure.PromptSyntax) *astructure.PromptSyntax {
	return &value
}

func toolPatchWithInputSchema(schema *tool.Schema) astructure.SurfaceValue {
	return astructure.SurfaceValue{
		Tools: []astructure.ToolRef{
			{
				ID:           "lookup",
				Description:  "new",
				InputSchema:  schema,
				OutputSchema: testLookupOutputSchema("Lookup response."),
			},
		},
	}
}

func mutatedLookupInputSchema(mutate func(*tool.Schema)) *tool.Schema {
	schema := testLookupInputSchema("Lookup request.")
	mutate(schema)
	return schema
}

func TestBuildIndexRejectsSpecificInvalidInputs(t *testing.T) {
	text := "instruction"
	_, err := BuildIndex([]astructure.Surface{
		{
			NodeID: "node_1",
			Type:   astructure.SurfaceTypeInstruction,
			Value:  astructure.SurfaceValue{Text: &text},
		},
	})
	assert.EqualError(t, err, "surface id is empty")
	_, err = BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value:     astructure.SurfaceValue{Text: &text},
		},
	})
	assert.EqualError(t, err, "surface node id is empty")
	_, err = BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceType("unknown"),
			Value:     astructure.SurfaceValue{Text: &text},
		},
	})
	assert.EqualError(t, err, `surface type "unknown" is invalid`)
	_, err = BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
		},
	})
	assert.ErrorContains(t, err, `surface "surf_1" value is invalid`)
}

func testToolSurface() astructure.Surface {
	return astructure.Surface{
		SurfaceID: "node#tool.lookup",
		NodeID:    "node",
		Type:      astructure.SurfaceTypeTool,
		Value: astructure.SurfaceValue{
			Tools: []astructure.ToolRef{
				{
					ID:           "lookup",
					Description:  "Look up a record.",
					InputSchema:  testLookupInputSchema("Lookup request."),
					OutputSchema: testLookupOutputSchema("Lookup response."),
				},
			},
		},
	}
}

func testLookupInputSchema(description string) *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: description,
		Required:    []string{"query"},
		Properties: map[string]*tool.Schema{
			"query": {Type: "string", Description: "Record key."},
			"filters": {
				Type:        "array",
				Description: "Filters.",
				Items:       &tool.Schema{Type: "string", Description: "Filter value."},
			},
		},
		AdditionalProperties: &tool.Schema{Type: "string", Description: "Metadata."},
		Defs: map[string]*tool.Schema{
			"record": {Type: "object", Description: "Record."},
		},
	}
}

func testLookupOutputSchema(description string) *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: description,
		Properties: map[string]*tool.Schema{
			"status": {Type: "string", Description: "Status."},
		},
	}
}

func TestValidateValueRejectsExtraFields(t *testing.T) {
	text := "instruction"
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:    &text,
			FewShot: []astructure.FewShotExample{{}},
		},
	), "messages are not empty")
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:  &text,
			Model: &astructure.ModelRef{Name: "m"},
		},
	), "model is not nil")
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{
			FewShot: []astructure.FewShotExample{},
			Model:   &astructure.ModelRef{Name: "m"},
		},
	), "model is not nil")
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{
			Model: &astructure.ModelRef{Name: "m"},
			Text:  &text,
		},
	), `surface type "model" is invalid`)
}

func TestValidateValueRejectsToolExtraFields(t *testing.T) {
	text := "instruction"
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Text:  &text,
			Tools: []astructure.ToolRef{{ID: "lookup"}},
		},
	), "text is not nil")
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			PromptSyntax: promptSyntaxPtr(astructure.PromptSyntaxSingleBrace),
			Tools:        []astructure.ToolRef{{ID: "lookup"}},
		},
	), "prompt syntax is not nil")
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			FewShot: []astructure.FewShotExample{{}},
			Tools:   []astructure.ToolRef{{ID: "lookup"}},
		},
	), "messages are not empty")
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Skills: []astructure.SkillRef{{ID: "skill"}},
			Tools:  []astructure.ToolRef{{ID: "lookup"}},
		},
	), "skills are not empty")
	assert.EqualError(t, validateValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Tools: []astructure.ToolRef{{}},
		},
	), "tool id is empty")
}

func TestSanitizeValueRejectsToolExtraFields(t *testing.T) {
	_, err := sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			PromptSyntax: promptSyntaxPtr(astructure.PromptSyntaxSingleBrace),
			Tools:        []astructure.ToolRef{{ID: "lookup"}},
		},
	)
	assert.EqualError(t, err, "prompt syntax is not nil")
	_, err = sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			FewShot: []astructure.FewShotExample{{}},
			Tools:   []astructure.ToolRef{{ID: "lookup"}},
		},
	)
	assert.EqualError(t, err, "messages are not empty")
	_, err = sanitizeValue(
		astructure.SurfaceTypeTool,
		astructure.SurfaceValue{
			Skills: []astructure.SkillRef{{ID: "skill"}},
			Tools:  []astructure.ToolRef{{ID: "lookup"}},
		},
	)
	assert.EqualError(t, err, "skills are not empty")
}

func TestSanitizePatchValueReturnsNonToolValue(t *testing.T) {
	text := "patched instruction"
	sanitized, err := SanitizePatchValue(astructure.Surface{
		SurfaceID: "node#instruction",
		NodeID:    "node",
		Type:      astructure.SurfaceTypeInstruction,
	}, astructure.SurfaceValue{
		Text: &text,
	})
	assert.NoError(t, err)
	assert.Equal(t, &text, sanitized.Text)
	assert.Nil(t, sanitized.Model)
	_, err = SanitizePatchValue(astructure.Surface{
		SurfaceID: "node#instruction",
		NodeID:    "node",
		Type:      astructure.SurfaceTypeInstruction,
	}, astructure.SurfaceValue{
		Text:  &text,
		Model: &astructure.ModelRef{},
	})
	assert.EqualError(t, err, "model is not nil")
}
