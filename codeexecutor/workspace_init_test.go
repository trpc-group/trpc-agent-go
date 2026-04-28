//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewWorkspaceInitExecutor_NilExecutor(t *testing.T) {
	out, err := NewWorkspaceInitExecutor(nil)
	require.NoError(t, err)
	require.Nil(t, out)

	_, err = NewWorkspaceInitExecutor(
		nil,
		NewWorkspaceInitHook(WorkspaceInitSpec{
			Commands: []WorkspaceInitCommand{{Cmd: "true"}},
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exec is nil")
}

func TestNewWorkspaceInitExecutor_NoHooksReturnsOriginal(t *testing.T) {
	var stub CodeExecutor = stubExecutor{}
	out, err := NewWorkspaceInitExecutor(stub)
	require.NoError(t, err)
	require.Equal(t, stub, out)
}

func TestSpecInitHook_RejectsEmptyCmd(t *testing.T) {
	h := NewWorkspaceInitHook(WorkspaceInitSpec{
		Commands: []WorkspaceInitCommand{{Cmd: "   "}},
	})
	err := h(context.Background(), WorkspaceInitEnv{
		Workspace: Workspace{ID: "x", Path: "/"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "command 0: Cmd is empty")
}

func TestSpecInitHook_RequiresFSForInputs(t *testing.T) {
	h := NewWorkspaceInitHook(WorkspaceInitSpec{
		Inputs: []InputSpec{{From: "host:///tmp/input", To: "work/input"}},
	})
	err := h(context.Background(), WorkspaceInitEnv{
		Workspace: Workspace{ID: "x", Path: "/"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "WorkspaceFS is nil")
}

func TestSpecInitHook_RequiresRunnerForCommands(t *testing.T) {
	h := NewWorkspaceInitHook(WorkspaceInitSpec{
		Commands: []WorkspaceInitCommand{{Cmd: "true"}},
	})
	err := h(context.Background(), WorkspaceInitEnv{
		Workspace: Workspace{ID: "x", Path: "/"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ProgramRunner is nil")
}

func TestSpecInitHook_StagesInputsBeforeCommandsAndClonesSpec(t *testing.T) {
	fs := &recordingFS{}
	runner := &recordingRunner{
		t:  t,
		fs: fs,
		check: func(t *testing.T, spec RunProgramSpec) {
			require.True(t, fs.staged, "inputs must be staged before commands run")
			require.Equal(t, []string{"-lc", "echo ok"}, spec.Args)
			require.Equal(t, map[string]string{"A": "B"}, spec.Env)
			spec.Args[0] = "mutated"
			spec.Env["A"] = "mutated"
		},
	}
	args := []string{"-lc", "echo ok"}
	env := map[string]string{"A": "B"}
	h := NewWorkspaceInitHook(WorkspaceInitSpec{
		Inputs: []InputSpec{{From: "host:///tmp/input", To: "work/input"}},
		Commands: []WorkspaceInitCommand{{
			Name:    "echo",
			Cmd:     "bash",
			Args:    args,
			Env:     env,
			Cwd:     "work",
			Stdin:   "stdin",
			Timeout: time.Second,
		}},
	})

	err := h(context.Background(), WorkspaceInitEnv{
		Workspace: Workspace{ID: "x", Path: "/"},
		FS:        fs,
		Runner:    runner,
	})
	require.NoError(t, err)
	require.Equal(t, []InputSpec{{From: "host:///tmp/input", To: "work/input"}}, fs.inputs)
	require.Equal(t, []string{"-lc", "echo ok"}, args, "args should be cloned")
	require.Equal(t, map[string]string{"A": "B"}, env, "env should be cloned")
	require.Equal(t, "work", runner.seen.Cwd)
	require.Equal(t, "stdin", runner.seen.Stdin)
	require.Equal(t, time.Second, runner.seen.Timeout)
}

func TestSpecInitHook_CommandRunnerError(t *testing.T) {
	boom := errors.New("boom")
	h := NewWorkspaceInitHook(WorkspaceInitSpec{
		Commands: []WorkspaceInitCommand{{Name: "named", Cmd: "bash"}},
	})
	err := h(context.Background(), WorkspaceInitEnv{
		Workspace: Workspace{ID: "x", Path: "/"},
		Runner:    &recordingRunner{err: boom},
	})
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), `command "named"`)
}

func TestSpecInitHook_NonZeroExitTruncatesOutput(t *testing.T) {
	h := NewWorkspaceInitHook(WorkspaceInitSpec{
		Commands: []WorkspaceInitCommand{{Cmd: "bash"}},
	})
	long := strings.Repeat("x", workspaceInitErrorOutputMax+10)
	err := h(context.Background(), WorkspaceInitEnv{
		Workspace: Workspace{ID: "x", Path: "/"},
		Runner: &recordingRunner{
			result: RunResult{ExitCode: 9, Stdout: long, Stderr: long},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `command "bash" exited 9`)
	require.Contains(t, err.Error(), "...(truncated)")
}

func TestNewWorkspaceInitExecutor_NotEngineProviderErrors(t *testing.T) {
	stub := stubExecutor{}
	_, err := NewWorkspaceInitExecutor(
		stub,
		NewWorkspaceInitHook(WorkspaceInitSpec{
			Commands: []WorkspaceInitCommand{
				{Cmd: "true"},
			},
		}),
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrWorkspaceInitNeedsEngineProvider))
}

type stubExecutor struct{}

func (stubExecutor) ExecuteCode(context.Context, CodeExecutionInput) (CodeExecutionResult, error) {
	return CodeExecutionResult{}, nil
}

func (stubExecutor) CodeBlockDelimiter() CodeBlockDelimiter {
	return CodeBlockDelimiter{}
}

type recordingFS struct {
	staged bool
	inputs []InputSpec
}

func (f *recordingFS) PutFiles(context.Context, Workspace, []PutFile) error {
	return nil
}

func (f *recordingFS) StageDirectory(
	context.Context, Workspace, string, string, StageOptions,
) error {
	return nil
}

func (f *recordingFS) Collect(context.Context, Workspace, []string) ([]File, error) {
	return nil, nil
}

func (f *recordingFS) StageInputs(
	_ context.Context, _ Workspace, specs []InputSpec,
) error {
	f.staged = true
	f.inputs = append([]InputSpec(nil), specs...)
	return nil
}

func (f *recordingFS) CollectOutputs(
	context.Context, Workspace, OutputSpec,
) (OutputManifest, error) {
	return OutputManifest{}, nil
}

type recordingRunner struct {
	t      *testing.T
	fs     *recordingFS
	check  func(*testing.T, RunProgramSpec)
	seen   RunProgramSpec
	result RunResult
	err    error
}

func (r *recordingRunner) RunProgram(
	_ context.Context, _ Workspace, spec RunProgramSpec,
) (RunResult, error) {
	r.seen = spec
	if r.check != nil {
		r.check(r.t, spec)
	}
	if r.err != nil {
		return RunResult{}, r.err
	}
	return r.result, nil
}

func TestWorkspaceInitEngine_DelegatesCapabilities(t *testing.T) {
	require.Nil(t, newWorkspaceInitEngine(nil, nil))

	fs := &recordingFS{}
	runner := &recordingRunner{}
	inner := NewEngine(&fakeWM{}, fs, runner)
	wrapped := newWorkspaceInitEngine(inner, nil)

	require.Same(t, fs, wrapped.FS())
	require.Same(t, runner, wrapped.Runner())
	require.Equal(t, Capabilities{}, wrapped.Describe())
	require.NotNil(t, wrapped.Manager())
}

func TestWorkspaceInitManager_CreateWorkspaceInnerError(t *testing.T) {
	boom := errors.New("boom")
	inner := NewEngine(&fakeWM{err: boom}, &recordingFS{}, &recordingRunner{})
	mgr := newWorkspaceInitEngine(inner, nil).Manager()
	_, err := mgr.CreateWorkspace(context.Background(), "x", WorkspacePolicy{})
	require.ErrorIs(t, err, boom)
}

func TestWorkspaceInitManager_CleanupFailureIsReported(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	mgr := &workspaceInitManager{
		inner: &cleanupFailWM{cleanupErr: cleanupErr},
		eng:   NewEngine(&fakeWM{}, &recordingFS{}, &recordingRunner{}),
		hooks: []WorkspaceInitHook{
			func(context.Context, WorkspaceInitEnv) error {
				return fmt.Errorf("hook failed")
			},
		},
	}

	_, err := mgr.CreateWorkspace(context.Background(), "x", WorkspacePolicy{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "workspace init hook 0")
	require.Contains(t, err.Error(), "cleanup failed")
}

type cleanupFailWM struct {
	cleanupErr error
}

func (m *cleanupFailWM) CreateWorkspace(
	_ context.Context, id string, _ WorkspacePolicy,
) (Workspace, error) {
	return Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (m *cleanupFailWM) Cleanup(context.Context, Workspace) error {
	return m.cleanupErr
}

func TestWorkspaceRegistry_Acquire_ConcurrentCreatesOnce(t *testing.T) {
	reg := NewWorkspaceRegistry()
	wm := &fakeWM{ws: Workspace{Path: "/tmp/w"}}
	ctx := context.Background()

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := reg.Acquire(ctx, wm, "same-id")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, wm.callCount(),
		"concurrent first acquires must coalesce to one CreateWorkspace")
}
