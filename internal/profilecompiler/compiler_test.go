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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	isurfacepatch "trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCompileProfilePatchesCompilesSupportedSurfaces(t *testing.T) {
	structure := newCompilerTestStructure(t)
	instruction := "patched instruction"
	toolDescription := "Look up a travel record by key."
	normalized, err := structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
			{
				SurfaceID: "node#few_shot",
				Value: astructure.SurfaceValue{FewShot: []astructure.FewShotExample{
					{
						Messages: []astructure.FewShotMessage{
							{Role: string(model.RoleUser), Content: "question"},
							{Role: string(model.RoleAssistant), Content: "answer"},
						},
					},
				}},
			},
			{
				SurfaceID: "node#tool.lookup",
				Value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
					{ID: "lookup", Description: toolDescription},
				}},
			},
		},
	})
	require.NoError(t, err)
	patches, err := compileProfilePatches(normalized)
	require.NoError(t, err)
	require.Len(t, patches, 1)
	patch := patches["node"]
	patchedInstruction, ok := patch.Instruction()
	assert.True(t, ok)
	assert.Equal(t, instruction, patchedInstruction)
	examples, ok := patch.FewShot()
	require.True(t, ok)
	require.Len(t, examples, 1)
	require.Len(t, examples[0], 2)
	assert.Equal(t, model.RoleUser, examples[0][0].Role)
	assert.Equal(t, "question", examples[0][0].Content)
	assert.Equal(t, model.RoleAssistant, examples[0][1].Role)
	declarations, ok := patch.ToolDeclarations()
	require.True(t, ok)
	require.Len(t, declarations, 1)
	assert.Equal(t, "lookup", declarations[0].Name)
	assert.Equal(t, toolDescription, declarations[0].Description)
}

func TestNormalizeProfileFillsAndValidatesSurfaceMetadata(t *testing.T) {
	structure := newCompilerTestStructure(t)
	instruction := "patched instruction"
	normalized, err := structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, normalized.Overrides, 1)
	assert.Equal(t, "node", normalized.Overrides[0].NodeID)
	assert.Equal(t, astructure.SurfaceTypeInstruction, normalized.Overrides[0].Type)
	_, err = structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				NodeID:    "other",
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	})
	assert.EqualError(t, err, `profile override "node#instruction" node id "other" does not match surface node id "node"`)
	_, err = structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				Type:      astructure.SurfaceTypeFewShot,
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	})
	assert.EqualError(t, err, `profile override "node#instruction" type "few_shot" does not match surface type "instruction"`)
}

func TestCompileRunOptionsRejectsInvalidFewShotRole(t *testing.T) {
	structure := newCompilerTestStructure(t)
	normalized, err := structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#few_shot",
				Value: astructure.SurfaceValue{FewShot: []astructure.FewShotExample{
					{
						Messages: []astructure.FewShotMessage{
							{Role: "unknown", Content: "bad"},
						},
					},
				}},
			},
		},
	})
	require.NoError(t, err)
	runOptions, err := CompileRunOptions(normalized, false)
	assert.Nil(t, runOptions)
	assert.EqualError(t, err, `surface "node#few_shot" few-shot value is invalid: example 0 message 0 role "unknown" is invalid`)
}

func TestNormalizeProfileRejectsInvalidToolValue(t *testing.T) {
	structure := newCompilerTestStructure(t)
	normalized, err := structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#tool.lookup",
				Value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
					{ID: "lookup"},
					{ID: "delay"},
				}},
			},
		},
	})
	assert.Nil(t, normalized)
	assert.EqualError(t, err, `sanitize profile override "node#tool.lookup": tools must contain exactly one tool, got 2`)
}

func TestCompileRunOptionsCompilesProfileAndTracing(t *testing.T) {
	structure := newCompilerTestStructure(t)
	instruction := "patched instruction"
	normalized, err := structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	})
	require.NoError(t, err)
	runOptions, err := CompileRunOptions(normalized, true)
	require.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	assert.True(t, opts.ExecutionTraceEnabled)
	assert.True(t, isurfacepatch.ToolSurfaceTracingEnabled(opts.CustomAgentConfigs))
	patch, ok := isurfacepatch.PatchForNode(opts.CustomAgentConfigs, "node")
	require.True(t, ok)
	patchedInstruction, ok := patch.Instruction()
	require.True(t, ok)
	assert.Equal(t, instruction, patchedInstruction)
}

func TestCompileRunOptionsDropsNoopOverrides(t *testing.T) {
	structure := newCompilerTestStructure(t)
	baseInstruction := "base instruction"
	normalized, err := structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				Value:     astructure.SurfaceValue{Text: &baseInstruction},
			},
		},
	})
	require.NoError(t, err)
	runOptions, err := CompileRunOptions(normalized, false)
	require.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	assert.Empty(t, opts.CustomAgentConfigs)
}

func TestNormalizeProfileDropsNoopTextOverrideWithPromptSyntaxBaseline(t *testing.T) {
	baseInstruction := "base instruction"
	structure, err := NewStructure(&astructure.Snapshot{
		StructureID: "structure",
		EntryNodeID: "node",
		Nodes: []astructure.Node{
			{NodeID: "node", Kind: astructure.NodeKindLLM},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "node#instruction",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text:         &baseInstruction,
					PromptSyntax: promptSyntaxPtr(astructure.PromptSyntaxSingleBrace),
				},
			},
		},
	})
	require.NoError(t, err)
	normalized, err := structure.NormalizeProfile(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				Value:     astructure.SurfaceValue{Text: &baseInstruction},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, normalized.Overrides)
}

func TestCompileRunOptionsAllowsTracingWithoutProfile(t *testing.T) {
	runOptions, err := CompileRunOptions(nil, true)
	require.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	assert.True(t, opts.ExecutionTraceEnabled)
	assert.True(t, isurfacepatch.ToolSurfaceTracingEnabled(opts.CustomAgentConfigs))
}

func TestCompileRunOptionsCompilesProfileWithoutStructure(t *testing.T) {
	instruction := "patched instruction"
	runOptions, err := CompileRunOptions(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeInstruction,
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	}, false)
	require.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	assert.Nil(t, ProfileFromRunOptions(opts))
	patch, ok := isurfacepatch.PatchForNode(opts.CustomAgentConfigs, "node")
	require.True(t, ok)
	patchedInstruction, ok := patch.Instruction()
	require.True(t, ok)
	assert.Equal(t, instruction, patchedInstruction)
}

func TestCompileRunOptionsCompilesToolProfileWithoutStructure(t *testing.T) {
	description := "patched lookup"
	inputSchema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"id": {Type: "string"},
		},
	}
	outputSchema := &tool.Schema{Type: "object"}
	runOptions, err := CompileRunOptions(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#tool.lookup",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
					{
						ID:           "lookup",
						Description:  description,
						InputSchema:  inputSchema,
						OutputSchema: outputSchema,
					},
				}},
			},
		},
	}, false)
	require.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	patch, ok := isurfacepatch.PatchForNode(opts.CustomAgentConfigs, "node")
	require.True(t, ok)
	declarations, ok := patch.ToolDeclarations()
	require.True(t, ok)
	require.Len(t, declarations, 1)
	assert.Equal(t, "lookup", declarations[0].Name)
	assert.Equal(t, description, declarations[0].Description)
	assert.Equal(t, inputSchema, declarations[0].InputSchema)
	assert.Equal(t, outputSchema, declarations[0].OutputSchema)
}

func TestProfileRunOptionAttachesProfile(t *testing.T) {
	profile := &Profile{
		StructureID: "structure",
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeInstruction,
			},
		},
	}
	opts := agent.NewRunOptions(WithProfile(profile))
	assert.Equal(t, profile, ProfileFromRunOptions(opts))
}

func TestCompileRunOptionsRejectsIncompleteProfileWithoutStructure(t *testing.T) {
	instruction := "patched instruction"
	runOptions, err := CompileRunOptions(&Profile{
		StructureID: "structure",
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	}, false)
	assert.Nil(t, runOptions)
	assert.EqualError(t, err, `profile override "node#instruction" node id is empty`)
}

func TestCompileRunOptionsDoesNotRequireStructureID(t *testing.T) {
	instruction := "patched instruction"
	runOptions, err := CompileRunOptions(&Profile{
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeInstruction,
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	}, false)
	require.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	patch, ok := isurfacepatch.PatchForNode(opts.CustomAgentConfigs, "node")
	require.True(t, ok)
	patchedInstruction, ok := patch.Instruction()
	require.True(t, ok)
	assert.Equal(t, instruction, patchedInstruction)
}

func TestCompileRunOptionsRejectsNonCanonicalRuntimeValue(t *testing.T) {
	instruction := "patched instruction"
	runOptions, err := CompileRunOptions(&Profile{
		StructureID: "structure",
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#instruction",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text:  &instruction,
					Model: &astructure.ModelRef{},
				},
			},
		},
	}, false)
	assert.Nil(t, runOptions)
	assert.EqualError(t, err, `profile override "node#instruction" value is invalid: model is not nil`)
}

func TestCompileRunOptionsRejectsNonCanonicalToolRuntimeValue(t *testing.T) {
	runOptions, err := CompileRunOptions(&Profile{
		StructureID: "structure",
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#tool.lookup",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
					{ID: "lookup"},
					{ID: "lookup"},
				}},
			},
		},
	}, false)
	assert.Nil(t, runOptions)
	assert.EqualError(t, err, `profile override "node#tool.lookup" value is invalid: tools must contain exactly one tool, got 2`)
}

func TestCompileRunOptionsRejectsMismatchedSurfaceTarget(t *testing.T) {
	instruction := "patched instruction"
	runOptions, err := CompileRunOptions(&Profile{
		StructureID: "structure",
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "other#instruction",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeInstruction,
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	}, false)
	assert.Nil(t, runOptions)
	assert.EqualError(t, err, `profile override "other#instruction" does not match node id "node" and type "instruction"`)
}

func TestCompileRunOptionsRejectsMismatchedToolSurfaceID(t *testing.T) {
	runOptions, err := CompileRunOptions(&Profile{
		StructureID: "structure",
		Overrides: []SurfaceOverride{
			{
				SurfaceID: "node#tool.lookup",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{Tools: []astructure.ToolRef{
					{ID: "search", Description: "search"},
				}},
			},
		},
	}, false)
	assert.Nil(t, runOptions)
	assert.EqualError(t, err, `profile override "node#tool.lookup" does not match node id "node", type "tool", and tool id "search"`)
}

func newCompilerTestStructure(t *testing.T) *Structure {
	t.Helper()
	instruction := "base instruction"
	global := "base global"
	structure, err := NewStructure(&astructure.Snapshot{
		StructureID: "structure",
		EntryNodeID: "node",
		Nodes: []astructure.Node{
			{NodeID: "node", Kind: astructure.NodeKindLLM},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "node#instruction",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeInstruction,
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
			{
				SurfaceID: "node#global_instruction",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeGlobalInstruction,
				Value:     astructure.SurfaceValue{Text: &global},
			},
			{
				SurfaceID: "node#few_shot",
				NodeID:    "node",
				Type:      astructure.SurfaceTypeFewShot,
				Value:     astructure.SurfaceValue{FewShot: []astructure.FewShotExample{}},
			},
			testToolSurface(),
		},
	})
	require.NoError(t, err)
	return structure
}
