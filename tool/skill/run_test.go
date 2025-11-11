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

package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	testSkillName   = "demo"
	skillFileName   = "SKILL.md"
	timeoutSecSmall = 5
)

// writeSkill creates a minimal skill folder.
func writeSkill(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	data := "---\nname: " + name + "\n" +
		"description: test skill\n---\nbody\n"
	err := os.WriteFile(filepath.Join(dir, skillFileName),
		[]byte(data), 0o644)
	require.NoError(t, err)
	return dir
}

func TestRunTool_ExecutesAndCollectsOutputFiles(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	// Use local runtime. No special options needed.
	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: "mkdir -p out; echo hi > out/a.txt; echo ZZZ",
		OutputFiles: []string{
			"out/*.txt",
		},
		Timeout: timeoutSecSmall,
	}

	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Contains(t, out.Stdout, "ZZZ")
	require.False(t, out.TimedOut)
	require.NotEmpty(t, out.Duration)

	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "out/a.txt", out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, "hi")
}

func TestRunTool_SaveAsArtifacts_AndOmitInline(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: "mkdir -p out; echo hi > out/a.txt",
		OutputFiles: []string{
			"out/*.txt",
		},
		Timeout:       timeoutSecSmall,
		SaveArtifacts: true,
		OmitInline:    true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	// Build invocation with artifact service and session info.
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "s1",
			State: session.StateMap{},
		}),
		agent.WithInvocationArtifactService(inmemory.NewService()),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, "out/a.txt", out.ArtifactFiles[0].Name)
	require.Equal(t, 0, out.ArtifactFiles[0].Version)
}

// errArtifactService always fails on save to cover error path.
type errArtifactService struct{}

func (e *errArtifactService) SaveArtifact(
	ctx context.Context, sessionInfo artifact.SessionInfo,
	filename string, a *artifact.Artifact,
) (int, error) {
	return 0, fmt.Errorf("forced-error")
}
func (e *errArtifactService) LoadArtifact(
	ctx context.Context, sessionInfo artifact.SessionInfo,
	filename string, version *int,
) (*artifact.Artifact, error) {
	return nil, nil
}
func (e *errArtifactService) ListArtifactKeys(
	ctx context.Context, sessionInfo artifact.SessionInfo,
) ([]string, error) {
	return nil, nil
}
func (e *errArtifactService) DeleteArtifact(
	ctx context.Context, sessionInfo artifact.SessionInfo,
	filename string,
) error {
	return nil
}
func (e *errArtifactService) ListVersions(
	ctx context.Context, sessionInfo artifact.SessionInfo,
	filename string,
) ([]int, error) {
	return nil, nil
}

func TestRunTool_SaveAsArtifacts_SaveError(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: "mkdir -p out; echo hi > out/a.txt",
		OutputFiles: []string{
			"out/*.txt",
		},
		Timeout:       timeoutSecSmall,
		SaveArtifacts: true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "s1",
			State: session.StateMap{},
		}),
		agent.WithInvocationArtifactService(&errArtifactService{}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err = rt.Call(ctx, enc)
	require.Error(t, err)
}

func TestRunTool_ErrorOnMissingSkill(t *testing.T) {
	root := t.TempDir()
	// No skill written.
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{Skill: "missing", Command: "echo hello"}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

func TestRunTool_ErrorOnNilExecutor(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	rt := NewRunTool(repo, nil)
	args := runInput{Skill: testSkillName, Command: "echo ok"}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

// jsonMarshal is a tiny wrapper to keep tests tidy.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// stubCE implements minimal CodeExecutor to trigger engine fallback.
type stubCE struct{}

func (s *stubCE) ExecuteCode(
	ctx context.Context, in codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{Output: ""}, nil
}
func (s *stubCE) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestRunTool_FallbackEngine_NoEngineProvider(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	// Use a CodeExecutor without Engine() to trigger fallback.
	exec := &stubCE{}
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: "echo ok",
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
}

// Test that when no cwd is provided, the working directory defaults to
// the staged skill root so relative paths in skill docs work.
func TestRunTool_DefaultCWD_UsesSkillRoot(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	// Place a file under scripts/ inside the skill.
	scripts := filepath.Join(dir, "scripts")
	require.NoError(t, os.MkdirAll(scripts, 0o755))
	data := []byte("hello\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(scripts, "file.txt"), data, 0o644,
	))

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: "cat scripts/file.txt > out/a.txt",
		OutputFiles: []string{
			"out/a.txt",
		},
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "out/a.txt", out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, "hello")
}

// Test that a relative cwd is resolved under the skill root, not under
// the workspace root.
func TestRunTool_RelativeCWD_SubpathUnderSkillRoot(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	scripts := filepath.Join(dir, "scripts")
	require.NoError(t, os.MkdirAll(scripts, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(scripts, "msg.txt"), []byte("msg\n"), 0o644,
	))

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Cwd:     "scripts",
		Command: "cat msg.txt > ../out/b.txt",
		OutputFiles: []string{
			"out/b.txt",
		},
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "out/b.txt", out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, "msg")
}

// Test that workspace persists across calls within the same session,
// so files written earlier can be collected later.
func TestRunTool_WorkspacePersistsAcrossCalls(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "sess-1",
			State: session.StateMap{},
		}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	// First call: create a file under out/.
	a1 := runInput{
		Skill:   testSkillName,
		Command: "mkdir -p out; echo hi > out/p.txt",
		OutputFiles: []string{
			"out/p.txt",
		},
		Timeout: timeoutSecSmall,
	}
	b1, err := jsonMarshal(a1)
	require.NoError(t, err)
	r1, err := rt.Call(ctx, b1)
	require.NoError(t, err)
	o1 := r1.(runOutput)
	require.Equal(t, 0, o1.ExitCode)
	require.Len(t, o1.OutputFiles, 1)
	require.Contains(t, o1.OutputFiles[0].Content, "hi")

	// Second call: do not write; just collect the same file.
	a2 := runInput{
		Skill:   testSkillName,
		Command: "echo ok",
		OutputFiles: []string{
			"out/p.txt",
		},
		Timeout: timeoutSecSmall,
	}
	b2, err := jsonMarshal(a2)
	require.NoError(t, err)
	r2, err := rt.Call(ctx, b2)
	require.NoError(t, err)
	o2 := r2.(runOutput)
	require.Equal(t, 0, o2.ExitCode)
	require.Len(t, o2.OutputFiles, 1)
	require.Contains(t, o2.OutputFiles[0].Content, "hi")
}
