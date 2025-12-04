//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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
	"path"
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
	outGlobTxt      = "out/*.txt"
	outATxt         = "out/a.txt"
	outBTxt         = "out/b.txt"
	scriptsDir      = "scripts"
	contentHi       = "hi"
	contentHello    = "hello"
	contentMsg      = "msg"
	echoOK          = "echo ok"
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
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt + "; echo ZZZ",
		OutputFiles: []string{outGlobTxt},
		Timeout:     timeoutSecSmall,
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
	require.Equal(t, outATxt, out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, contentHi)
}

func TestRunTool_SaveAsArtifacts_AndOmitInline(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:   []string{outGlobTxt},
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
	require.Equal(t, outATxt, out.ArtifactFiles[0].Name)
	require.Equal(t, 0, out.ArtifactFiles[0].Version)
	// OmitInline should clear inline file contents.
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
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
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:   []string{outGlobTxt},
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

	args := runInput{Skill: "missing", Command: "echo " + contentHello}
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
	args := runInput{Skill: testSkillName, Command: echoOK}
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
		Command: echoOK,
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
	scripts := filepath.Join(dir, scriptsDir)
	require.NoError(t, os.MkdirAll(scripts, 0o755))
	data := []byte(contentHello + "\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(scripts, "file.txt"), data, 0o644,
	))

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:       testSkillName,
		Command:     "cat " + scriptsDir + "/file.txt > " + outATxt,
		OutputFiles: []string{outATxt},
		Timeout:     timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, outATxt, out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, contentHello)
}

// Test that a relative cwd is resolved under the skill root, not under
// the workspace root.
func TestRunTool_RelativeCWD_SubpathUnderSkillRoot(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	scripts := filepath.Join(dir, scriptsDir)
	require.NoError(t, os.MkdirAll(scripts, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(scripts, "msg.txt"),
		[]byte(contentMsg+"\n"), 0o644,
	))

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:       testSkillName,
		Cwd:         scriptsDir,
		Command:     "cat msg.txt > ../" + outBTxt,
		OutputFiles: []string{outBTxt},
		Timeout:     timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, outBTxt, out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, contentMsg)
}

// Validate Declaration basics and required fields.
func TestRunTool_Declaration(t *testing.T) {
	rt := NewRunTool(nil, nil)
	d := rt.Declaration()
	require.NotNil(t, d)
	require.Equal(t, "skill_run", d.Name)
	require.NotNil(t, d.InputSchema)
	require.Contains(t, d.InputSchema.Required, "skill")
	require.Contains(t, d.InputSchema.Required, "command")
}

// Ensure parseRunArgs rejects invalid JSON and missing fields.
func TestRunTool_parseRunArgs_Validation(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	// Invalid JSON
	_, err = rt.parseRunArgs([]byte("not-json"))
	require.Error(t, err)

	// Missing command
	b, _ := json.Marshal(map[string]any{"skill": testSkillName})
	_, err = rt.parseRunArgs(b)
	require.Error(t, err)
}

// collectFiles should return nil slice and nil error on empty patterns.
func TestRunTool_collectFiles_EmptyPatterns(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())
	eng := rt.ensureEngine()
	ws, err := rt.createWorkspace(context.Background(), eng, "sess")
	require.NoError(t, err)
	files, err := rt.collectFiles(
		context.Background(), eng, ws, nil,
	)
	require.NoError(t, err)
	require.Nil(t, files)
}

// shellQuote should escape single quotes safely.
func TestShellQuote(t *testing.T) {
	got := shellQuote("a'b")
	// Expect: 'a'\''b'
	require.Equal(t, "'a'\\''b'", got)
	require.Equal(t, "''", shellQuote(""))
}

// filepathBase trims trailing slash and returns last element.
func TestFilepathBase(t *testing.T) {
	require.Equal(t, "b", filepathBase("/a/b/"))
	require.Equal(t, "a", filepathBase("a"))
}

// Using Outputs spec with Inline=true maps manifest into OutputFiles
// and does not attach artifact refs when Save=false.
func TestRunTool_OutputsSpec_Inline_NoSave(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		Outputs: &codeexecutor.OutputSpec{
			Globs:  []string{outGlobTxt},
			Inline: true,
			Save:   false,
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
	require.Equal(t, outATxt, out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, contentHi)
	require.Len(t, out.ArtifactFiles, 0)
}

// Using Outputs spec with Save=true and Inline=false should attach
// artifact refs from manifest without inlining file content.
func TestRunTool_OutputsSpec_Save_NoInline(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		Outputs: &codeexecutor.OutputSpec{
			Globs:        []string{outGlobTxt},
			Inline:       false,
			Save:         true,
			NameTemplate: "pref/",
		},
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	// Provide artifact service and session for saving.
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
	require.Len(t, out.OutputFiles, 0)
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, "pref/"+outATxt, out.ArtifactFiles[0].Name)
}

// Verify SaveArtifacts prefixing and OmitInline together on legacy path.
func TestRunTool_SaveArtifacts_PrefixAndOmitInline(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:    []string{outGlobTxt},
		Timeout:        timeoutSecSmall,
		SaveArtifacts:  true,
		OmitInline:     true,
		ArtifactPrefix: "pfx/",
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
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, "pfx/"+outATxt, out.ArtifactFiles[0].Name)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
}

// Cover createWorkspace branch where internal registry is nil.
func TestRunTool_CreateWorkspace_NilRegistry(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())
	// Force nil to exercise initialization path.
	rt.reg = nil

	args := runInput{Skill: testSkillName, Command: echoOK,
		Timeout: timeoutSecSmall}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
}

// StageInputs using a skill:// reference and consume it.
func TestRunTool_StageInputs_FromSkill(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	// Prepare a source file under scripts/ of the skill.
	scripts := filepath.Join(dir, scriptsDir)
	require.NoError(t, os.MkdirAll(scripts, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(scripts, "msg.txt"),
		[]byte(contentMsg+"\n"), 0o644,
	))

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill:   testSkillName,
		Command: "cat work/inputs/m.txt > " + outBTxt,
		Inputs: []codeexecutor.InputSpec{
			{From: "skill://" + testSkillName + "/" + scriptsDir +
				"/msg.txt",
				To:   "work/inputs/m.txt",
				Mode: "copy",
			},
		},
		OutputFiles: []string{outBTxt},
		Timeout:     timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Contains(t, out.OutputFiles[0].Content, contentMsg)
}

func TestRunTool_Call_InvalidInputSpec_Error(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill:   testSkillName,
		Command: echoOK,
		Inputs: []codeexecutor.InputSpec{{
			From: "unknown://abc",
			To:   "work/inputs/x",
			Mode: "copy",
		}},
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

func TestRunTool_stageSkill_EnsureLayoutError(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())
	eng := localexec.New().Engine()

	// Workspace path points to a file; EnsureLayout should fail.
	tmpf := filepath.Join(t.TempDir(), "asfile")
	require.NoError(t, os.WriteFile(tmpf, []byte("x"), 0o644))
	ws := codeexecutor.Workspace{ID: "bad", Path: tmpf}

	err = rt.stageSkill(context.Background(), eng, ws, dir,
		testSkillName,
	)
	require.Error(t, err)
}

func TestRunTool_stageSkill_CreatesInputsDir(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())
	eng := localexec.New().Engine()

	ctx := context.Background()
	ws, err := eng.Manager().CreateWorkspace(
		ctx, "stage-inputs-dir", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	err = rt.stageSkill(ctx, eng, ws, dir, testSkillName)
	require.NoError(t, err)

	inputsPath := filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs",
	)
	info, err := os.Stat(inputsPath)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestResolveCWD_AbsolutePath(t *testing.T) {
	// Absolute cwd should be returned unchanged.
	abs := "/"
	got := resolveCWD(abs, testSkillName)
	require.Equal(t, abs, got)
}

func TestResolveCWD_DefaultAndRelative(t *testing.T) {
	// Default: when cwd is empty, base is skills/<name>.
	base := path.Join(codeexecutor.DirSkills, testSkillName)
	got := resolveCWD("", testSkillName)
	require.Equal(t, base, got)

	// Relative: appended under the skill root.
	got = resolveCWD("sub/dir", testSkillName)
	require.Equal(t, path.Join(base, "sub/dir"), got)
}

// dummyExec implements CodeExecutor but not EngineProvider to cover
// ensureEngine fallback path.
type dummyExec struct{}

func (*dummyExec) ExecuteCode(
	_ context.Context, _ codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (*dummyExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestRunTool_EnsureEngine_Fallback(t *testing.T) {
	// Repo is not used by ensureEngine.
	rt := NewRunTool(&mockRepo{}, &dummyExec{})
	eng := rt.ensureEngine()
	require.NotNil(t, eng)
	// Create a workspace to ensure the engine is usable.
	ws, err := eng.Manager().CreateWorkspace(
		context.Background(), "eng-fallback", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, ws.Path)
}

func TestFilepathBase_Variants(t *testing.T) {
	require.Equal(t, "c", filepathBase("a/b/c"))
	require.Equal(t, "b", filepathBase("a/b/"))
	require.Equal(t, "root", filepathBase("root"))
}

func TestParseRunArgs_InvalidJSON(t *testing.T) {
	rt := NewRunTool(&mockRepo{}, nil)
	_, err := rt.parseRunArgs([]byte("{bad}"))
	require.Error(t, err)
}

func TestMergeManifestArtifactRefs_Appends(t *testing.T) {
	mf := &codeexecutor.OutputManifest{
		Files: []codeexecutor.FileRef{{
			Name:     "out/a.txt",
			SavedAs:  "prefix-out/a.txt",
			Version:  2,
			MIMEType: "text/plain",
		}},
	}
	out := &runOutput{}
	mergeManifestArtifactRefs(mf, out)
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, "prefix-out/a.txt", out.ArtifactFiles[0].Name)
	require.Equal(t, 2, out.ArtifactFiles[0].Version)
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
