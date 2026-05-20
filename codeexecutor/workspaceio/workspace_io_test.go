//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspaceio

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func newHarness(t *testing.T) (*Workspace, context.Context, *agent.Invocation, *inmemory.Service) {
	t.Helper()
	exec := localexec.New()
	reg := codeexecutor.NewWorkspaceRegistry()
	svc := inmemory.NewService()
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-wsio",
			AppName: "app",
			UserID:  "user",
		}),
		agent.WithInvocationArtifactService(svc),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	return New(exec, reg), ctx, inv, svc
}

func TestNew_NilExecReturnsNil(t *testing.T) {
	require.Nil(t, New(nil, nil))
}

func TestPutAndCollect(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	require.NotNil(t, ws)

	require.NoError(t, ws.PutFiles(ctx, codeexecutor.PutFile{
		Path:    "work/notes.txt",
		Content: []byte("hello world"),
	}))

	got, err := ws.Collect(ctx, "work/notes.txt")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "work/notes.txt", got[0].Path)
	require.Equal(t, []byte("hello world"), got[0].Data)
	require.False(t, got[0].Truncated)
	require.EqualValues(t, len("hello world"), got[0].SizeBytes)
}

// TestCollect_ForwardsTruncated verifies that the facade reports the
// backend's Truncated flag verbatim — it is the caller's responsibility
// to decide whether a truncated read is acceptable.
func TestCollect_ForwardsTruncated(t *testing.T) {
	got := toFile(codeexecutor.File{
		Name:      "skills/echoer/SKILL.md",
		Content:   "abc",
		SizeBytes: 8 * 1024 * 1024,
		Truncated: true,
	})
	require.True(t, got.Truncated)
	require.Equal(t, []byte("abc"), got.Data)
	require.EqualValues(t, 8*1024*1024, got.SizeBytes)
}

func TestCollect_ReturnsMatchingFiles(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	require.NoError(t, ws.PutFiles(ctx,
		codeexecutor.PutFile{Path: "skills/echoer/SKILL.md", Content: []byte("# Echoer")},
		codeexecutor.PutFile{Path: "skills/greeter/SKILL.md", Content: []byte("# Greeter")},
		codeexecutor.PutFile{Path: "skills/scratch/notes.txt", Content: []byte("ignored")},
	))

	got, err := ws.Collect(ctx, "skills/*/SKILL.md")
	require.NoError(t, err)
	require.Len(t, got, 2)

	paths := []string{got[0].Path, got[1].Path}
	sort.Strings(paths)
	require.Equal(t, []string{
		"skills/echoer/SKILL.md",
		"skills/greeter/SKILL.md",
	}, paths)
}

// TestCollect_EmptyPatternsReturnsEmptySlice pins the documented contract
// in workspace_io.go: "An empty pattern list returns an empty slice."
// Returning nil would force callers to special-case the no-pattern path
// and disagrees with the godoc.
func TestCollect_EmptyPatternsReturnsEmptySlice(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	got, err := ws.Collect(ctx)
	require.NoError(t, err)
	require.NotNil(t, got, "Collect must return a non-nil empty slice for empty patterns")
	require.Empty(t, got)
}

func TestPutFiles_BatchWrite(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)

	require.NoError(t, ws.PutFiles(ctx,
		codeexecutor.PutFile{Path: "work/a.txt", Content: []byte("alpha")},
		codeexecutor.PutFile{Path: "work/b.txt", Content: []byte("beta")},
	))

	got, err := ws.Collect(ctx, "work/a.txt", "work/b.txt")
	require.NoError(t, err)
	require.Len(t, got, 2)

	byPath := map[string][]byte{}
	for _, f := range got {
		byPath[f.Path] = f.Data
	}
	require.Equal(t, []byte("alpha"), byPath["work/a.txt"])
	require.Equal(t, []byte("beta"), byPath["work/b.txt"])
}

func TestPutFiles_EmptyIsNoOp(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	require.NoError(t, ws.PutFiles(ctx))
}

func TestSaveArtifact_PersistsExistingFile(t *testing.T) {
	ws, ctx, _, svc := newHarness(t)
	data := []byte("artifact-payload")
	require.NoError(t, ws.PutFiles(ctx, codeexecutor.PutFile{
		Path:    "out/site.zip",
		Content: data,
	}))

	ref, err := ws.SaveArtifact(ctx, "out/site.zip")
	require.NoError(t, err)
	require.Equal(t, "out/site.zip", ref.SavedAs)
	require.Equal(t, "out/site.zip", ref.Path)
	require.Equal(t, 0, ref.Version)
	require.Equal(t, "artifact://out/site.zip@0", ref.Ref)
	require.EqualValues(t, len(data), ref.SizeBytes)

	loaded, err := svc.LoadArtifact(ctx, artifact.SessionInfo{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-wsio",
	}, "out/site.zip", nil)
	require.NoError(t, err)
	require.Equal(t, data, loaded.Data)
}

func TestSaveArtifact_RejectsSkillsPath(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	_, err := ws.SaveArtifact(ctx, "skills/demo/SKILL.md")
	require.Error(t, err)
	require.Contains(t, err.Error(), "supported artifact roots")
}

func TestSaveArtifact_RequiresArtifactService(t *testing.T) {
	exec := localexec.New()
	ws := New(exec, nil)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "sess", AppName: "app", UserID: "user",
		}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	_, err := ws.SaveArtifact(ctx, "out/site.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "artifact service")
}

func TestStageInputs_HostScheme(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(srcDir, "a.txt"), []byte("from-host"), 0o644,
	))

	err := ws.StageInputs(ctx, []codeexecutor.InputSpec{{
		From: "host://" + srcDir,
		To:   "work/inputs/a.txt",
	}})
	require.NoError(t, err)

	got, err := ws.Collect(ctx, "work/inputs/a.txt")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, []byte("from-host"), got[0].Data)
}

// TestRunProgram_EchoesStdout verifies that RunProgram forwards a
// successful command's stdout and zero exit code through the facade.
func TestRunProgram_EchoesStdout(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)

	res, err := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{
		Cmd:  "sh",
		Args: []string{"-c", "printf hello"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Equal(t, "hello", res.Stdout)
	require.False(t, res.TimedOut)
}

// TestRunProgram_NonZeroExitIsNotError verifies the documented contract:
// non-zero exit codes are signalled via RunResult, not via error.
func TestRunProgram_NonZeroExitIsNotError(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)

	res, err := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{
		Cmd:  "sh",
		Args: []string{"-c", "exit 7"},
	})
	require.NoError(t, err, "non-zero exit must not be a framework error")
	require.Equal(t, 7, res.ExitCode)
	require.False(t, res.TimedOut)
}

func TestContextRoundTrip(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)

	got, ok := WorkspaceFromContext(ctx)
	require.False(t, ok)
	require.Nil(t, got)

	bound := WithWorkspace(ctx, ws)
	got, ok = WorkspaceFromContext(bound)
	require.True(t, ok)
	require.Same(t, ws, got)
}

func TestContextNilNoOp(t *testing.T) {
	ctx := context.Background()
	out := WithWorkspace(ctx, nil)
	got, ok := WorkspaceFromContext(out)
	require.False(t, ok)
	require.Nil(t, got)
}

// TestContextNilContextHandled covers the (ctx == nil) guard on both
// helpers — defensive but cheap.
func TestContextNilContextHandled(t *testing.T) {
	require.Nil(t, WithWorkspace(nil, &Workspace{}))
	got, ok := WorkspaceFromContext(nil)
	require.False(t, ok)
	require.Nil(t, got)
}

// TestNilReceiver_AllMethodsErrorOnNilFacade verifies that every
// public method on *Workspace tolerates a nil receiver and returns a
// uniform "workspace is nil" error rather than panicking.
func TestNilReceiver_AllMethodsErrorOnNilFacade(t *testing.T) {
	var w *Workspace
	ctx := context.Background()

	_, err := w.Collect(ctx, "x")
	require.ErrorContains(t, err, "workspace is nil")

	require.ErrorContains(t,
		w.PutFiles(ctx, codeexecutor.PutFile{
			Path:    "a.txt",
			Content: []byte("x"),
		}),
		"workspace is nil")

	_, err = w.SaveArtifact(ctx, "out/x")
	require.ErrorContains(t, err, "workspace is nil")

	require.ErrorContains(t,
		w.StageInputs(ctx, []codeexecutor.InputSpec{{
			From: "host:///nope", To: "x",
		}}),
		"workspace is nil")

	_, err = w.RunProgram(ctx, codeexecutor.RunProgramSpec{Cmd: "true"})
	require.ErrorContains(t, err, "workspace is nil")
}

// TestUninitialized_BindWorkspace covers the (resolver == nil) guard
// in bindWorkspace via a zero-value *Workspace. Methods reach the
// guard rather than touching any backend.
func TestUninitialized_BindWorkspace(t *testing.T) {
	w := &Workspace{} // resolver is nil
	ctx := context.Background()

	_, err := w.Collect(ctx, "x")
	require.ErrorContains(t, err, "workspace is not initialized")

	require.ErrorContains(t,
		w.PutFiles(ctx, codeexecutor.PutFile{
			Path:    "a.txt",
			Content: []byte("x"),
		}),
		"workspace is not initialized")

	require.ErrorContains(t,
		w.StageInputs(ctx, []codeexecutor.InputSpec{{
			From: "host:///nope", To: "x",
		}}),
		"workspace is not initialized")

	_, err = w.RunProgram(ctx, codeexecutor.RunProgramSpec{Cmd: "true"})
	require.ErrorContains(t, err, "workspace is not initialized")
}

// TestSaveArtifact_NoInvocationInContext exercises the
// SaveReasonNoInvocation branch — a freshly constructed *Workspace
// with no invocation on ctx.
func TestSaveArtifact_NoInvocationInContext(t *testing.T) {
	exec := localexec.New()
	ws := New(exec, nil)
	_, err := ws.SaveArtifact(context.Background(), "out/missing.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invocation is missing")
}

// TestSaveArtifact_FileNotFound exercises the manifest.Files == 0
// branch — the path is publish-allowed but the file does not exist.
func TestSaveArtifact_FileNotFound(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	_, err := ws.SaveArtifact(ctx, "out/never-written.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "artifact file not found")
}

// TestSaveArtifact_MaxBytesZeroFallsBackToDefault verifies that
// WithSaveArtifactMaxBytes(0) is treated as "use the framework
// default" rather than rejecting the call outright.
func TestSaveArtifact_MaxBytesZeroFallsBackToDefault(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	require.NoError(t, ws.PutFiles(ctx, codeexecutor.PutFile{
		Path:    "out/small.txt",
		Content: []byte("ok"),
	}))

	ref, err := ws.SaveArtifact(ctx, "out/small.txt",
		WithSaveArtifactMaxBytes(0))
	require.NoError(t, err)
	require.Equal(t, "out/small.txt", ref.SavedAs)
}

// nilRunnerExec implements codeexecutor.CodeExecutor and
// EngineProvider, exposing an engine whose Runner() is nil. It is
// the minimum surface needed to drive Workspace.RunProgram into its
// "no program runner" branch without leaning on any production
// backend's behavior.
type nilRunnerExec struct {
	eng codeexecutor.Engine
}

func (f *nilRunnerExec) ExecuteCode(
	context.Context, codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (f *nilRunnerExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

func (f *nilRunnerExec) Engine() codeexecutor.Engine { return f.eng }

// TestRunProgram_RunnerNil covers the "executor does not expose a
// program runner" branch. We wire a real local FS+Manager so
// bindWorkspace succeeds, then drop the Runner.
func TestRunProgram_RunnerNil(t *testing.T) {
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, nil) // runner = nil
	exec := &nilRunnerExec{eng: eng}

	ws := New(exec, codeexecutor.NewWorkspaceRegistry())
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "sess", AppName: "app", UserID: "user",
		}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{Cmd: "true"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not expose a program runner")
}

// stubFSExec implements codeexecutor.CodeExecutor + EngineProvider with
// a caller-supplied Engine. Used to inject FS doubles below.
type stubFSExec struct {
	eng codeexecutor.Engine
}

func (s *stubFSExec) ExecuteCode(
	context.Context, codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (s *stubFSExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

func (s *stubFSExec) Engine() codeexecutor.Engine { return s.eng }

// partialCommitFS layers ErrPartialOutputCommit on top of a real
// CollectOutputs response so the SaveArtifact "ignore partial commit
// when artifact landed" branch can be exercised without reaching for a
// backend that emits the error organically.
type partialCommitFS struct {
	codeexecutor.WorkspaceFS
}

func (p *partialCommitFS) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	m, err := p.WorkspaceFS.CollectOutputs(ctx, ws, spec)
	if err != nil {
		return m, err
	}
	return m, codeexecutor.ErrPartialOutputCommit
}

// TestSaveArtifact_PartialCommitStillReturnsRef pins the contract that
// ErrPartialOutputCommit is non-fatal: the artifact has already been
// persisted and callers must still receive the ref. Mirrors the same
// concession in tool/workspaceexec.SaveArtifactTool.Call.
func TestSaveArtifact_PartialCommitStillReturnsRef(t *testing.T) {
	rt := localexec.NewRuntime("")
	fs := &partialCommitFS{WorkspaceFS: rt}
	eng := codeexecutor.NewEngine(rt, fs, rt)
	exec := &stubFSExec{eng: eng}

	ws := New(exec, codeexecutor.NewWorkspaceRegistry())
	svc := inmemory.NewService()
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "sess-partial", AppName: "app", UserID: "user",
		}),
		agent.WithInvocationArtifactService(svc),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	require.NoError(t, ws.PutFiles(ctx, codeexecutor.PutFile{
		Path: "out/done.txt", Content: []byte("ok"),
	}))

	ref, err := ws.SaveArtifact(ctx, "out/done.txt")
	require.NoError(t, err,
		"ErrPartialOutputCommit must not bubble up when the artifact has landed")
	require.NotNil(t, ref)
	require.Equal(t, "out/done.txt", ref.Path)
	require.NotEmpty(t, ref.SavedAs)
}

// captureCtxStageFS snapshots the ctx that StageInputs receives without
// touching the inner backend, so the test can pin "artifact service /
// session info is forwarded to the backend".
type captureCtxStageFS struct {
	codeexecutor.WorkspaceFS
	gotCtx context.Context
}

func (c *captureCtxStageFS) StageInputs(
	ctx context.Context,
	_ codeexecutor.Workspace,
	_ []codeexecutor.InputSpec,
) error {
	c.gotCtx = ctx
	return nil
}

// TestStageInputs_ForwardsArtifactContext pins the contract that
// StageInputs prepares ctx with the invocation's artifact service /
// session info, matching what SaveArtifact already does. Without this,
// `artifact://` inputs can't resolve even though workspace acquisition
// silently used them earlier in the same call.
func TestStageInputs_ForwardsArtifactContext(t *testing.T) {
	rt := localexec.NewRuntime("")
	fs := &captureCtxStageFS{WorkspaceFS: rt}
	eng := codeexecutor.NewEngine(rt, fs, rt)
	exec := &stubFSExec{eng: eng}

	ws := New(exec, codeexecutor.NewWorkspaceRegistry())
	svc := inmemory.NewService()
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "sess-stage", AppName: "app", UserID: "user",
		}),
		agent.WithInvocationArtifactService(svc),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	require.NoError(t, ws.StageInputs(ctx, []codeexecutor.InputSpec{
		{From: "artifact://placeholder@1", To: "in/placeholder"},
	}))
	require.NotNil(t, fs.gotCtx, "FS.StageInputs must have been called")
	gotSvc, ok := codeexecutor.ArtifactServiceFromContext(fs.gotCtx)
	require.True(t, ok,
		"ctx forwarded to backend StageInputs must carry artifact service")
	require.Same(t, artifact.Service(svc), gotSvc)
}

// TestRunProgram_RejectsCwdEscape pins the godoc claim that spec.Cwd
// cannot escape the workspace. The local runtime in particular joins
// ws.Path with filepath.Clean(spec.Cwd) verbatim and would otherwise
// honor "../..".
func TestRunProgram_RejectsCwdEscape(t *testing.T) {
	ws, ctx, _, _ := newHarness(t)
	_, err := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{
		Cmd: "true",
		Cwd: "../etc",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cwd must stay within the workspace")
}

// TestWithSaveArtifactMaxBytes_Applies verifies the option is wired
// to SaveArtifactOptions.MaxBytes. The option is otherwise applied
// inside an internal flow; checking the struct directly is the
// cheapest way to pin the contract.
func TestWithSaveArtifactMaxBytes_Applies(t *testing.T) {
	cfg := SaveArtifactOptions{}
	WithSaveArtifactMaxBytes(1234)(&cfg)
	require.EqualValues(t, 1234, cfg.MaxBytes)
}

// TestToFile_SizeFallback covers the size<=0 fallback in toFile that
// re-derives SizeBytes from Content when the backend left it unset.
func TestToFile_SizeFallback(t *testing.T) {
	got := toFile(codeexecutor.File{
		Name:    "work/a.txt",
		Content: "abc",
	})
	require.EqualValues(t, 3, got.SizeBytes)
	require.Equal(t, []byte("abc"), got.Data)
	require.False(t, got.Truncated)
}
