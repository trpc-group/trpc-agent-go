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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestLLMAgent_SurfacePatch_OverridesInstructionAndSystemPrompt(t *testing.T) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithInstruction("static instruction"),
		WithGlobalInstruction("static system prompt"),
	)

	var patch agent.SurfacePatch
	patch.SetInstruction("patched instruction")
	patch.SetGlobalInstruction("patched system prompt")

	inv := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithInstruction("legacy instruction"),
			agent.WithGlobalInstruction("legacy system prompt"),
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)
	agt.setupInvocation(inv)

	reqProcs := buildRequestProcessorsWithAgent(agt, &agt.option)
	var instrProc any
	for _, proc := range reqProcs {
		if _, ok := proc.(*processor.InstructionRequestProcessor); ok {
			instrProc = proc
			break
		}
	}
	require.NotNil(t, instrProc)

	req := &model.Request{}
	ch := make(chan *event.Event, 1)
	instrProc.(*processor.InstructionRequestProcessor).ProcessRequest(
		context.Background(),
		inv,
		req,
		ch,
	)

	require.NotEmpty(t, req.Messages)
	content := req.Messages[0].Content
	require.Contains(t, content, "patched instruction")
	require.Contains(t, content, "patched system prompt")
	require.NotContains(t, content, "static instruction")
	require.NotContains(t, content, "static system prompt")
	require.NotContains(t, content, "legacy instruction")
	require.NotContains(t, content, "legacy system prompt")
}

func TestLLMAgent_SurfacePatch_ModelOverridesLegacyRunOptions(t *testing.T) {
	defaultModel := &mockModelWithResponse{}
	legacyModel := &mockModelWithResponse{}
	patchedModel := &mockModelWithResponse{}

	agt := New("test-agent", WithModel(defaultModel))

	var patch agent.SurfacePatch
	patch.SetModel(patchedModel)

	inv := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithModel(legacyModel),
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)

	agt.setupInvocation(inv)
	require.Equal(t, patchedModel, inv.Model)
}

func TestLLMAgent_ExecutionTraceAppliedSurfaceIDs(t *testing.T) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithInstruction("static instruction"),
		WithGlobalInstruction("static system prompt"),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("test-agent"),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithExecutionTraceEnabled(true),
		)),
	)
	agt.setupInvocation(inv)
	require.Equal(
		t,
		[]string{
			"test-agent#instruction",
			"test-agent#global_instruction",
			"test-agent#model",
		},
		agt.ExecutionTraceAppliedSurfaceIDs(inv),
	)
}

func TestLLMAgent_ExecutionTraceAppliedSurfaceIDs_UsesFilteredToolSnapshot(t *testing.T) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithTools([]tool.Tool{
			dummyTool{decl: &tool.Declaration{Name: "user_tool"}},
		}),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("test-agent"),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithExecutionTraceEnabled(true),
		)),
	)
	agt.setupInvocation(inv)
	inv.SetState("llmflow:has_filtered_user_tools", false)
	require.NotContains(t, agt.ExecutionTraceAppliedSurfaceIDs(inv), "test-agent#tool")
	inv.SetState("llmflow:has_filtered_user_tools", true)
	require.Contains(t, agt.ExecutionTraceAppliedSurfaceIDs(inv), "test-agent#tool")
}

func TestLLMAgent_SurfaceRuntimeHelpers_CoverPatchAndFallbackBranches(t *testing.T) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithTools([]tool.Tool{
			dummyTool{decl: &tool.Declaration{Name: "static_user_tool"}},
		}),
	)
	emptyPatchInv := agent.NewInvocation()
	_, ok := agt.rootSurfacePatch(nil)
	require.False(t, ok)
	_, ok = agt.rootSurfacePatch(emptyPatchInv)
	require.False(t, ok)
	require.Nil(t, agt.fewShotForInvocation(emptyPatchInv))
	require.Nil(t, agt.skillRepositoryForInvocation(emptyPatchInv))
	_, ok = agt.modelSurfaceForInvocation(emptyPatchInv)
	require.False(t, ok)
	require.Nil(t, agt.ExecutionTraceAppliedSurfaceIDs(emptyPatchInv))
	userTools, userToolNames := agt.userToolsForInvocation(context.Background(), surfacepatch.Patch{})
	require.Len(t, userTools, 1)
	require.True(t, userToolNames["static_user_tool"])
	dynamicAgent := New(
		"dynamic-agent",
		WithModel(newDummyModel()),
		WithToolSets([]tool.ToolSet{dummyToolSet{name: "dynamic-tools"}}),
		WithRefreshToolSetsOnRun(true),
	)
	dynamicTools, dynamicUserToolNames := dynamicAgent.userToolsForInvocation(
		context.Background(),
		surfacepatch.Patch{},
	)
	require.NotEmpty(t, dynamicTools)
	require.NotEmpty(t, dynamicUserToolNames)
	var patch agent.SurfacePatch
	patch.SetFewShot([][]model.Message{{model.NewUserMessage("few-shot user")}})
	patch.SetTools([]tool.Tool{
		dummyTool{decl: &tool.Declaration{Name: "patched_user_tool"}},
	})
	patch.SetSkillRepository(&mockSkillRepository{
		summaries: []skill.Summary{{Name: "skill-a", Description: "desc"}},
	})
	patch.SetModel(newDummyModel())
	patchedInv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("test-agent"),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)
	agt.setupInvocation(patchedInv)
	patchedInv.SetState("llmflow:has_filtered_user_tools", false)
	require.Len(t, agt.fewShotForInvocation(patchedInv), 1)
	require.NotNil(t, agt.skillRepositoryForInvocation(patchedInv))
	m, ok := agt.modelSurfaceForInvocation(patchedInv)
	require.True(t, ok)
	require.NotNil(t, m)
	require.ElementsMatch(
		t,
		[]string{
			"test-agent#few_shot",
			"test-agent#model",
			"test-agent#skill",
		},
		agt.ExecutionTraceAppliedSurfaceIDs(patchedInv),
	)
	rootPatch, ok := agt.rootSurfacePatch(patchedInv)
	require.True(t, ok)
	userTools, userToolNames = agt.userToolsForInvocation(context.Background(), rootPatch)
	require.Len(t, userTools, 1)
	require.Equal(t, "patched_user_tool", userTools[0].Declaration().Name)
	require.True(t, userToolNames["patched_user_tool"])
	unchangedTools, unchangedNames := filterInvocationUserTools(
		context.Background(),
		userTools,
		userToolNames,
		nil,
	)
	require.Equal(t, userTools, unchangedTools)
	require.Equal(t, userToolNames, unchangedNames)
	filteredTools, filteredNames := filterInvocationUserTools(
		context.Background(),
		[]tool.Tool{
			nil,
			dummyTool{decl: nil},
			dummyTool{decl: &tool.Declaration{Name: "keep"}},
			dummyTool{decl: &tool.Declaration{Name: "drop"}},
		},
		map[string]bool{"keep": true, "drop": true},
		func(_ context.Context, tl tool.Tool) bool {
			return tl.Declaration().Name == "keep"
		},
	)
	require.Len(t, filteredTools, 1)
	require.Equal(t, "keep", filteredTools[0].Declaration().Name)
	require.Equal(t, map[string]bool{"keep": true}, filteredNames)
}

func TestLLMAgent_RunOptions_OverrideStaticInstructionAndSystemPrompt(
	t *testing.T,
) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithInstruction("static instruction"),
		WithGlobalInstruction("static system prompt"),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithInstruction("legacy instruction"),
			agent.WithGlobalInstruction("legacy system prompt"),
		)),
	)
	agt.setupInvocation(inv)

	reqProcs := buildRequestProcessorsWithAgent(agt, &agt.option)
	var instrProc any
	for _, proc := range reqProcs {
		if _, ok := proc.(*processor.InstructionRequestProcessor); ok {
			instrProc = proc
			break
		}
	}
	require.NotNil(t, instrProc)

	req := &model.Request{}
	ch := make(chan *event.Event, 1)
	instrProc.(*processor.InstructionRequestProcessor).ProcessRequest(
		context.Background(),
		inv,
		req,
		ch,
	)

	require.NotEmpty(t, req.Messages)
	content := req.Messages[0].Content
	require.Contains(t, content, "legacy instruction")
	require.Contains(t, content, "legacy system prompt")
	require.NotContains(t, content, "static instruction")
	require.NotContains(t, content, "static system prompt")
}

func TestLLMAgent_Run_SurfacePatch_InsertsFewShotBeforeUserMessage(t *testing.T) {
	m := &captureModel{}
	agt := New("test-agent", WithModel(m))

	var patch agent.SurfacePatch
	patch.SetFewShot([][]model.Message{{
		model.NewUserMessage("few-shot user"),
		model.NewAssistantMessage("few-shot assistant"),
	}})

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("actual user")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)

	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range ch {
	}

	require.NotNil(t, m.got)
	require.Len(t, m.got.Messages, 3)
	require.Equal(t, "few-shot user", m.got.Messages[0].Content)
	require.Equal(t, "few-shot assistant", m.got.Messages[1].Content)
	require.Equal(t, "actual user", m.got.Messages[2].Content)
}

func TestLLMAgent_Run_SurfacePatch_InsertsFewShotAfterSystemBlock(
	t *testing.T,
) {
	m := &captureModel{}
	agt := New(
		"test-agent",
		WithModel(m),
		WithInstruction("static instruction"),
	)

	var patch agent.SurfacePatch
	patch.SetFewShot([][]model.Message{{
		model.NewUserMessage("few-shot user"),
		model.NewAssistantMessage("few-shot assistant"),
	}})

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("actual user")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)

	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range ch {
	}

	require.NotNil(t, m.got)
	require.Len(t, m.got.Messages, 4)
	require.Equal(t, model.RoleSystem, m.got.Messages[0].Role)
	require.Contains(t, m.got.Messages[0].Content, "static instruction")
	require.Equal(t, "few-shot user", m.got.Messages[1].Content)
	require.Equal(t, "few-shot assistant", m.got.Messages[2].Content)
	require.Equal(t, "actual user", m.got.Messages[3].Content)
}

func TestLLMAgent_Run_SurfacePatch_ReplacesUserToolsAndPreservesFrameworkTools(t *testing.T) {
	m := &captureModel{}
	agt := New(
		"test-agent",
		WithModel(m),
		WithTools([]tool.Tool{
			dummyTool{decl: &tool.Declaration{Name: "old_user_tool"}},
		}),
		WithSubAgents([]agent.Agent{&mockAgent{name: "child"}}),
	)

	var patch agent.SurfacePatch
	patch.SetTools([]tool.Tool{
		dummyTool{decl: &tool.Declaration{Name: "new_user_tool"}},
	})

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)

	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range ch {
	}

	require.NotNil(t, m.got)
	require.Contains(t, m.got.Tools, "new_user_tool")
	require.NotContains(t, m.got.Tools, "old_user_tool")
	require.Contains(t, m.got.Tools, testTransferToolName)
}

func TestLLMAgent_Run_AgentToolFilterStillAppliesWithInvocationToolSurface(
	t *testing.T,
) {
	m := &captureModel{}
	agt := New(
		"test-agent",
		WithModel(m),
		WithTools([]tool.Tool{
			dummyTool{decl: &tool.Declaration{Name: "allowed_user_tool"}},
			dummyTool{decl: &tool.Declaration{Name: "blocked_user_tool"}},
		}),
		WithSubAgents([]agent.Agent{&mockAgent{name: "child"}}),
		WithToolFilter(func(_ context.Context, tl tool.Tool) bool {
			return tl.Declaration().Name == "allowed_user_tool"
		}),
	)

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)

	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range ch {
	}

	require.NotNil(t, m.got)
	require.Contains(t, m.got.Tools, "allowed_user_tool")
	require.NotContains(t, m.got.Tools, "blocked_user_tool")
	require.Contains(t, m.got.Tools, testTransferToolName)
}

func TestLLMAgent_Run_SurfacePatch_DisablesStaticSkills(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	m := &captureModel{}
	agt := New(
		"test-agent",
		WithModel(m),
		WithSkills(repo),
		WithCodeExecutor(&stubExec{}),
	)

	var patch agent.SurfacePatch
	patch.SetSkillRepository(nil)

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)

	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	ctx := context.Background()
	for evt := range ch {
		if evt != nil && evt.RequiresCompletion {
			key := agent.GetAppendEventNoticeKey(evt.ID)
			_ = inv.AddNoticeChannel(ctx, key)
			_ = inv.NotifyCompletion(ctx, key)
		}
	}

	require.NotNil(t, m.got)
	workspaceGuidance := findSystemMessageContaining(
		m.got,
		workspaceExecGuidanceHeader,
	)
	require.NotEmpty(t, workspaceGuidance)
	require.NotContains(t, workspaceGuidance, "skills/")
	for _, msg := range m.got.Messages {
		require.NotContains(t, msg.Content, skillsOverviewHeader)
	}
	require.NotContains(t, m.got.Tools, "skill_load")
	require.Contains(t, m.got.Tools, "workspace_exec")
}

func TestLLMAgent_Run_SurfacePatch_AddsSkillsWithoutStaticRepository(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	m := &captureModel{}
	agt := New(
		"test-agent",
		WithModel(m),
		WithCodeExecutor(&stubExec{}),
	)

	var patch agent.SurfacePatch
	patch.SetSkillRepository(repo)

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)

	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	ctx := context.Background()
	for evt := range ch {
		if evt != nil && evt.RequiresCompletion {
			key := agent.GetAppendEventNoticeKey(evt.ID)
			_ = inv.AddNoticeChannel(ctx, key)
			_ = inv.NotifyCompletion(ctx, key)
		}
	}

	require.NotNil(t, m.got)
	var sawSkills bool
	for _, msg := range m.got.Messages {
		if msg.Role == model.RoleSystem &&
			strings.Contains(msg.Content, skillsOverviewHeader) {
			sawSkills = true
		}
	}
	require.True(t, sawSkills)
	workspaceGuidance := findSystemMessageContaining(
		m.got,
		workspaceExecGuidanceHeader,
	)
	require.NotEmpty(t, workspaceGuidance)
	require.Contains(t, workspaceGuidance, "skills/")
	require.Contains(t, m.got.Tools, "skill_load")
	require.Contains(t, m.got.Tools, "workspace_exec")
}

func TestLLMAgent_InvocationToolSurface_HidesWorkspaceExecWhenDisabled(
	t *testing.T,
) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithCodeExecutor(&interactiveStubExec{}),
		WithWorkspaceExecSurfaceEnabled(false),
	)

	tools, _ := agt.InvocationToolSurface(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
			agent.WithInvocationSession(&session.Session{}),
		),
	)

	require.Nil(t, findTool(tools, "workspace_exec"))
	require.Nil(t, findTool(tools, "workspace_write_stdin"))
	require.Nil(t, findTool(tools, "workspace_kill_session"))
}

// The workspace file tools are opt-in: asserting the *absence* of
// the surface when the caller leaves the option at its default
// protects against accidentally expanding the default surface in
// future refactors.
func TestLLMAgent_InvocationToolSurface_FileToolsDisabledByDefault(
	t *testing.T,
) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithCodeExecutor(&interactiveStubExec{}),
	)
	tools, _ := agt.InvocationToolSurface(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
			agent.WithInvocationSession(&session.Session{}),
		),
	)
	// workspace_exec is still on (its own default is true) but the
	// file tools must stay off until the caller asks for them.
	require.NotNil(t, findTool(tools, "workspace_exec"))
	for _, name := range []string{
		"workspace_read_file",
		"workspace_list_dir",
		"workspace_search_file",
		"workspace_search_content",
		"workspace_write_file",
		"workspace_replace_content",
	} {
		require.Nil(
			t, findTool(tools, name),
			"file tool %q should be hidden by default", name,
		)
	}
}

// When WithWorkspaceFileToolsEnabled(true) is combined with the
// default exec surface, every file tool must be registered alongside
// workspace_exec so the model can pick the best tool for each job.
func TestLLMAgent_InvocationToolSurface_FileToolsEnabledRegistersAll(
	t *testing.T,
) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithCodeExecutor(&interactiveStubExec{}),
		WithWorkspaceFileToolsEnabled(true),
	)
	tools, _ := agt.InvocationToolSurface(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
			agent.WithInvocationSession(&session.Session{}),
		),
	)
	require.NotNil(t, findTool(tools, "workspace_exec"))
	for _, name := range []string{
		"workspace_read_file",
		"workspace_list_dir",
		"workspace_search_file",
		"workspace_search_content",
		"workspace_write_file",
		"workspace_replace_content",
	} {
		require.NotNil(
			t, findTool(tools, name),
			"file tool %q should be registered", name,
		)
	}
}

// File-tools-only mode exercises the path where exec is disabled but
// file tools are still opted in. This is the third supported
// configuration called out in WithWorkspaceFileToolsEnabled's godoc.
func TestLLMAgent_InvocationToolSurface_FileToolsOnlyWhenExecDisabled(
	t *testing.T,
) {
	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithCodeExecutor(&interactiveStubExec{}),
		WithWorkspaceExecSurfaceEnabled(false),
		WithWorkspaceFileToolsEnabled(true),
	)
	tools, _ := agt.InvocationToolSurface(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
			agent.WithInvocationSession(&session.Session{}),
		),
	)
	require.Nil(t, findTool(tools, "workspace_exec"))
	require.Nil(t, findTool(tools, "workspace_write_stdin"))
	require.NotNil(t, findTool(tools, "workspace_read_file"))
	require.NotNil(t, findTool(tools, "workspace_write_file"))
}

func TestLLMAgent_SurfaceRuntimeHelpers_NilAgentBranches(t *testing.T) {
	var agt *LLMAgent
	require.Nil(t, agt.codeExecutorForInvocation(nil))
	require.False(t, agt.supportsWorkspaceExecForInvocation(nil))
	require.False(t, agt.supportsWorkspaceExecSessionsForInvocation(nil))
	flags := agt.skillToolFlagsForInvocation(nil)
	require.False(t, flags.Load)
	require.False(t, flags.SelectDocs)
	require.False(t, flags.ListDocs)
	require.False(t, flags.Run)
	require.False(t, flags.Exec)
	require.False(t, flags.WriteStdin)
	require.False(t, flags.PollSession)
	require.False(t, flags.KillSession)
}

// supportsWorkspaceFileToolsForInvocation is the resolver the
// workspace request processor reads to decide whether to emit
// file-tools guidance. Guarantee it tracks both the opt-in and the
// executor's ability to host a shared workspace so the guidance
// cannot drift from the actual tool surface.
func TestLLMAgent_SurfaceRuntimeHelpers_FileToolsResolverRespectsOptionAndExecutor(
	t *testing.T,
) {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
	)

	disabled := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithCodeExecutor(&interactiveStubExec{}),
	)
	require.False(t, disabled.supportsWorkspaceFileToolsForInvocation(inv))

	enabled := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithCodeExecutor(&interactiveStubExec{}),
		WithWorkspaceFileToolsEnabled(true),
	)
	require.True(t, enabled.supportsWorkspaceFileToolsForInvocation(inv))

	noExec := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithWorkspaceFileToolsEnabled(true),
	)
	require.False(t, noExec.supportsWorkspaceFileToolsForInvocation(inv))
}

func TestLLMAgent_SurfaceRuntimeHelpers_WorkspaceExecOptionRespected(
	t *testing.T,
) {
	disabled := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithCodeExecutor(&interactiveStubExec{}),
		WithWorkspaceExecSurfaceEnabled(false),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
	)
	require.False(t, disabled.supportsWorkspaceExecForInvocation(inv))
	require.False(t, disabled.supportsWorkspaceExecSessionsForInvocation(inv))

	enabled := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithCodeExecutor(&interactiveStubExec{}),
	)
	require.True(t, enabled.supportsWorkspaceExecForInvocation(inv))
	require.True(t, enabled.supportsWorkspaceExecSessionsForInvocation(inv))
}

func TestLLMAgent_AppendSkillToolsWithRepo_UsesResolvedFlags(t *testing.T) {
	opts := &Options{}
	WithSkillToolProfile(SkillToolProfileKnowledgeOnly)(opts)
	tools := appendSkillToolsWithRepo(
		nil,
		opts,
		&mockSkillRepository{
			summaries: []skill.Summary{{Name: "skill-a", Description: "desc"}},
		},
		nil,
		nil,
	)
	require.NotNil(t, findTool(tools, "skill_load"))
	require.NotNil(t, findTool(tools, "skill_select_docs"))
	require.NotNil(t, findTool(tools, "skill_list_docs"))
	require.Nil(t, findTool(tools, "skill_run"))
}

func TestLLMAgent_SkillToolFlagsAndWorkspaceExecSessionHelpers_NilOptions(
	t *testing.T,
) {
	flags := mustResolveSkillToolFlagsWithExecutor(nil, nil)
	require.False(t, flags.Load)
	require.False(t, flags.SelectDocs)
	require.False(t, flags.ListDocs)
	require.False(t, flags.Run)
	require.False(t, flags.Exec)
	require.False(t, executorSupportsWorkspaceExecSessions(nil))
}
