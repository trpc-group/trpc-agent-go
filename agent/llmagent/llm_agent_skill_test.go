//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	testSkillName = "echoer"
)

// createTestSkill makes a minimal skill folder with SKILL.md.
func createTestSkill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sdir := filepath.Join(dir, testSkillName)
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	data := "---\nname: echoer\n" +
		"description: simple echo skill\n---\nbody\n"
	err := os.WriteFile(filepath.Join(sdir, "SKILL.md"), []byte(data), 0o644)
	require.NoError(t, err)
	return dir
}

// findTool finds a tool by name in a list.
func findTool(ts []tool.Tool, name string) tool.Tool {
	for _, t := range ts {
		if t.Declaration() != nil && t.Declaration().Name == name {
			return t
		}
	}
	return nil
}

func TestLLMAgent_SkillRunToolRegistered(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	a := New("tester", WithSkills(repo))
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		d := tl.Declaration()
		if d != nil {
			names[d.Name] = true
		}
	}
	require.True(t, names["skill_load"]) // existed before
	require.True(t, names["skill_run"])  // new runner tool
}

func TestLLMAgent_SkillRunToolExecutes(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	a := New("tester", WithSkills(repo))
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)
	args := map[string]any{"skill": testSkillName, "command": "echo hello"}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	res, err := tl.(tool.CallableTool).Call(context.Background(), b)
	require.NoError(t, err)
	jb, err := json.Marshal(res)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(jb, &m))
	require.Equal(t, float64(0), m["exit_code"]) // json numbers
	out, _ := m["stdout"].(string)
	require.Contains(t, out, "hello")
}

// stubExec implements CodeExecutor and exposes an Engine
// whose runner marks ran=true on use.
type stubExec struct{ ran bool }

func (s *stubExec) ExecuteCode(
	ctx context.Context,
	in codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}
func (s *stubExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (s *stubExec) Engine() codeexecutor.Engine {
	mgr := &stubMgr{}
	fs := &stubFS{}
	rr := &stubRunner{s: s}
	return codeexecutor.NewEngine(mgr, fs, rr)
}

type stubMgr struct{}

func (m *stubMgr) CreateWorkspace(
	ctx context.Context, id string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: id, Path: "/tmp/x"}, nil
}
func (m *stubMgr) Cleanup(ctx context.Context,
	ws codeexecutor.Workspace) error {
	return nil
}

type stubFS struct{}

func (f *stubFS) PutFiles(ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile) error {
	return nil
}
func (f *stubFS) StageDirectory(ctx context.Context,
	ws codeexecutor.Workspace,
	src, to string, opt codeexecutor.StageOptions) error {
	return nil
}
func (f *stubFS) Collect(ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string) ([]codeexecutor.File, error) {
	return nil, nil
}

func (f *stubFS) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	return nil
}

func (f *stubFS) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type stubRunner struct{ s *stubExec }

func (r *stubRunner) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.s.ran = true
	return codeexecutor.RunResult{
		Stdout:   "ok",
		ExitCode: 0,
		Duration: time.Millisecond,
	}, nil
}

func TestLLMAgent_SkillRun_UsesInjectedExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	se := &stubExec{}
	a := New("tester", WithSkills(repo), WithCodeExecutor(se))
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)
	args := map[string]any{"skill": testSkillName, "command": "echo ok"}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), b)
	require.NoError(t, err)
	require.True(t, se.ran)
}

// captureModel records the last request passed to GenerateContent.
type captureModel struct{ got *model.Request }

func (m *captureModel) GenerateContent(
	ctx context.Context, req *model.Request,
) (<-chan *model.Response, error) {
	m.got = req
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "ok",
			},
		}},
		Done:      true,
		IsPartial: false,
	}
	close(ch)
	return ch, nil
}

func (m *captureModel) Info() model.Info {
	return model.Info{Name: "capture"}
}

func TestLLMAgent_WithSkills_InsertsOverview(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	m := &captureModel{}
	agt := New("tester", WithModel(m), WithSkills(repo))
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{}),
	)
	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	// Drain events and notify completion when required.
	ctx := context.Background()
	for evt := range ch {
		if evt != nil && evt.RequiresCompletion {
			key := agent.GetAppendEventNoticeKey(evt.ID)
			_ = inv.AddNoticeChannel(ctx, key)
			_ = inv.NotifyCompletion(ctx, key)
		}
	}
	require.NotNil(t, m.got)
	var sys string
	for _, msg := range m.got.Messages {
		if msg.Role == model.RoleSystem {
			sys = msg.Content
			break
		}
	}
	require.NotEmpty(t, sys)
	require.Contains(t, sys, "Available skills:")
	require.Contains(t, sys, "echoer")
}
