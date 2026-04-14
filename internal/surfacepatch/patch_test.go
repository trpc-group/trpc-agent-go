//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package surfacepatch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type dummyTool struct {
	name string
}

func (d dummyTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: d.name}
}

func (d dummyTool) Call(context.Context, []byte) (any, error) {
	return nil, nil
}

type stubRepo struct{}

func (stubRepo) Summaries() []skill.Summary {
	return []skill.Summary{{Name: "demo"}}
}

func (stubRepo) Get(name string) (*skill.Skill, error) {
	return &skill.Skill{Summary: skill.Summary{Name: name}}, nil
}

func (stubRepo) Path(string) (string, error) {
	return "", nil
}

type stubModel struct {
	name string
}

func (m *stubModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *stubModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func TestWithPatch_MergesBySurfaceTypePerNode(t *testing.T) {
	var first Patch
	first.SetInstruction("first instruction")
	first.SetTools([]tool.Tool{dummyTool{name: "old"}})

	var second Patch
	second.SetGlobalInstruction("global instruction")

	var third Patch
	third.SetInstruction("second instruction")

	cfgs := WithPatch(nil, "root", first)
	cfgs = WithPatch(cfgs, "root", second)
	cfgs = WithPatch(cfgs, "root", third)

	patch, ok := PatchForNode(cfgs, "root")
	require.True(t, ok)

	instruction, ok := patch.Instruction()
	require.True(t, ok)
	require.Equal(t, "second instruction", instruction)

	globalInstruction, ok := patch.GlobalInstruction()
	require.True(t, ok)
	require.Equal(t, "global instruction", globalInstruction)

	tools, ok := patch.Tools()
	require.True(t, ok)
	require.Len(t, tools, 1)
	require.Equal(t, "old", tools[0].Declaration().Name)
}

func TestPatch_TracksExplicitZeroValues(t *testing.T) {
	var patch Patch
	patch.SetInstruction("")
	patch.SetFewShot(nil)
	patch.SetTools(nil)
	patch.SetSkillRepository(nil)

	instruction, ok := patch.Instruction()
	require.True(t, ok)
	require.Empty(t, instruction)

	fewShot, ok := patch.FewShot()
	require.True(t, ok)
	require.Nil(t, fewShot)

	tools, ok := patch.Tools()
	require.True(t, ok)
	require.Nil(t, tools)

	repo, ok := patch.SkillRepository()
	require.True(t, ok)
	require.Nil(t, repo)
}

func TestPatch_IgnoresNilModelOverride(t *testing.T) {
	var patch Patch
	patch.SetModel(nil)

	modelValue, ok := patch.Model()
	require.False(t, ok)
	require.Nil(t, modelValue)
	require.True(t, patch.IsEmpty())
}

func TestPatch_ClonesMutableValues(t *testing.T) {
	modelValue := &stubModel{name: "patched"}
	repo := stubRepo{}
	tools := []tool.Tool{dummyTool{name: "first"}}
	examples := [][]model.Message{{
		model.NewUserMessage("u1"),
		model.NewAssistantMessage("a1"),
	}}

	var patch Patch
	patch.SetFewShot(examples)
	patch.SetModel(modelValue)
	patch.SetTools(tools)
	patch.SetSkillRepository(repo)

	examples[0][0].Content = "changed"
	tools[0] = dummyTool{name: "changed"}

	gotExamples, ok := patch.FewShot()
	require.True(t, ok)
	require.Len(t, gotExamples, 1)
	require.Equal(t, "u1", gotExamples[0][0].Content)

	gotTools, ok := patch.Tools()
	require.True(t, ok)
	require.Len(t, gotTools, 1)
	require.Equal(t, "first", gotTools[0].Declaration().Name)

	gotModel, ok := patch.Model()
	require.True(t, ok)
	require.Equal(t, modelValue, gotModel)

	gotRepo, ok := patch.SkillRepository()
	require.True(t, ok)
	require.NotNil(t, gotRepo)
}

func TestWithPatch_IgnoresEmptyInputs(t *testing.T) {
	cfgs := map[string]any{"keep": "value"}
	require.Equal(t, cfgs, WithPatch(cfgs, "", Patch{}))
	require.Equal(t, cfgs, WithPatch(cfgs, "root", Patch{}))
}

func TestPatchForNode_MissingAndDefensiveCopy(t *testing.T) {
	var patch Patch
	patch.SetInstruction("original")
	cfgs := WithPatch(nil, "root", patch)
	_, ok := PatchForNode(cfgs, "")
	require.False(t, ok)
	_, ok = PatchForNode(cfgs, "missing")
	require.False(t, ok)
	got, ok := PatchForNode(cfgs, "root")
	require.True(t, ok)
	got.SetInstruction("mutated")
	again, ok := PatchForNode(cfgs, "root")
	require.True(t, ok)
	instruction, ok := again.Instruction()
	require.True(t, ok)
	require.Equal(t, "original", instruction)
}

func TestRootNodeID_UsesStoredValueAndFallsBack(t *testing.T) {
	require.Equal(t, "fallback", RootNodeID(nil, "fallback"))
	cfgs := WithRootNodeID(nil, "workflow/root")
	require.Equal(t, "workflow/root", RootNodeID(cfgs, "fallback"))
	require.Nil(t, WithRootNodeID(nil, ""))
	require.Equal(
		t,
		"fallback",
		RootNodeID(map[string]any{rootNodeIDConfigsKey: ""}, "fallback"),
	)
	require.Equal(
		t,
		"fallback",
		RootNodeID(map[string]any{rootNodeIDConfigsKey: 123}, "fallback"),
	)
}

func TestPatchForNode_ReadsMapStringPatchConfigs(t *testing.T) {
	var patch Patch
	patch.SetGlobalInstruction("global")
	cfgs := map[string]any{
		configsKey: map[string]Patch{
			"root": patch,
		},
	}
	got, ok := PatchForNode(cfgs, "root")
	require.True(t, ok)
	globalInstruction, ok := got.GlobalInstruction()
	require.True(t, ok)
	require.Equal(t, "global", globalInstruction)
	got.SetGlobalInstruction("mutated")
	again, ok := PatchForNode(cfgs, "root")
	require.True(t, ok)
	globalInstruction, ok = again.GlobalInstruction()
	require.True(t, ok)
	require.Equal(t, "global", globalInstruction)
}
