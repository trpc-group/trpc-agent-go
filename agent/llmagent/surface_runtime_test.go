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
