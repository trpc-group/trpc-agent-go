//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
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

	"github.com/stretchr/testify/require"

	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
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

	// Minimal front matter and body.
	data := "---\nname: echoer\n" +
		"description: simple echo skill\n---\nbody\n"
	err := os.WriteFile(filepath.Join(sdir, "SKILL.md"),
		[]byte(data), 0o644)
	require.NoError(t, err)
	return dir
}

// findTool finds a tool by name in a list.
func findTool(ts []tool.Tool, name string) tool.Tool {
	for _, t := range ts {
		if t.Declaration() != nil &&
			t.Declaration().Name == name {
			return t
		}
	}
	return nil
}

func TestLLMAgent_SkillRunToolRegistered(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	// Do not set executor to trigger local fallback.
	a := New("tester", WithSkills(repo))

	// Tool list should include both loader and runner.
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

	// Local runtime fallback should be used.
	a := New("tester", WithSkills(repo))
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)

	args := map[string]any{
		"skill":   testSkillName,
		"command": "echo hello",
	}
	b, err := json.Marshal(args)
	require.NoError(t, err)

	// Call tool; expect zero exit code and stdout.
	res, err := tl.(tool.CallableTool).
		Call(context.Background(), b)
	require.NoError(t, err)

	// Convert to JSON then to a generic map for assertions that
	// do not depend on the concrete struct type.
	jb, err := json.Marshal(res)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(jb, &m))
	// Exit code should be 0; Stdout should contain "hello".
	require.Equal(t, float64(0), m["exit_code"]) // json -> float64
	out, _ := m["stdout"].(string)
	require.Contains(t, out, "hello")
}

// stubExec implements CodeExecutor to verify injection via
// WithCodeExecutor. It records whether RunProgram was called.
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

func (s *stubExec) CreateWorkspace(
	ctx context.Context, id string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: id, Path: "/tmp/x"}, nil
}

func (s *stubExec) Cleanup(ctx context.Context,
	ws codeexecutor.Workspace) error {
	return nil
}

func (s *stubExec) PutFiles(ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile) error {
	return nil
}

func (s *stubExec) PutDirectory(ctx context.Context,
	ws codeexecutor.Workspace,
	hostPath, to string) error {
	return nil
}

func (s *stubExec) PutSkill(ctx context.Context,
	ws codeexecutor.Workspace,
	skillRoot, to string) error {
	return nil
}

func (s *stubExec) RunProgram(ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	s.ran = true
	return codeexecutor.RunResult{
		Stdout:   "ok",
		ExitCode: 0,
		Duration: time.Millisecond,
	}, nil
}

func (s *stubExec) Collect(ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string) ([]codeexecutor.File, error) {
	return nil, nil
}

func (s *stubExec) ExecuteInline(ctx context.Context,
	id string, blocks []codeexecutor.CodeBlock,
	timeout time.Duration,
) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{}, nil
}

func TestLLMAgent_SkillRun_UsesInjectedExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	se := &stubExec{}
	a := New("tester",
		WithSkills(repo), WithCodeExecutor(se))
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)

	args := map[string]any{
		"skill":   testSkillName,
		"command": "echo ok",
	}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), b)
	require.NoError(t, err)
	require.True(t, se.ran)
}
