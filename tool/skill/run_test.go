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
		Timeout:           timeoutSecSmall,
		SaveAsArtifacts:   true,
		OmitInlineContent: true,
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
	// Inline content omitted.
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
}

func TestRunTool_SaveAsArtifacts_WithPrefix_NoOmit(t *testing.T) {
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
		Timeout:         timeoutSecSmall,
		SaveAsArtifacts: true,
		// keep inline contents; set a prefix
		ArtifactPrefix: "user:",
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

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
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, "user:out/a.txt", out.ArtifactFiles[0].Name)
	// Inline content is kept (no omit flag).
	require.Len(t, out.OutputFiles, 1)
	require.Contains(t, out.OutputFiles[0].Content, "hi")
}

func TestRunTool_SaveAsArtifacts_NoInvocationContext(t *testing.T) {
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
		SaveAsArtifacts: true,
		Timeout:         timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
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
		Timeout:         timeoutSecSmall,
		SaveAsArtifacts: true,
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
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func TestRunTool_Declaration_And_InvalidArgs(t *testing.T) {
	rt := NewRunTool(nil, nil)
	d := rt.Declaration()
	require.Equal(t, "skill_run", d.Name)
	require.NotNil(t, d.InputSchema)
	require.NotNil(t, d.OutputSchema)

	// invalid json
	_, err := rt.Call(context.Background(), []byte("{"))
	require.Error(t, err)

	// missing fields
	b, _ := json.Marshal(map[string]any{"skill": "x"})
	_, err = rt.Call(context.Background(), b)
	require.Error(t, err)
}
