//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type fakeRunner struct {
	lastSpec RunProgramSpec
}

func (r *fakeRunner) RunProgram(
	_ context.Context, _ Workspace, spec RunProgramSpec,
) (RunResult, error) {
	r.lastSpec = spec
	return RunResult{Stdout: "ok"}, nil
}

type fakeInteractiveRunner struct {
	fakeRunner
	lastInteractiveSpec InteractiveProgramSpec
}

func (r *fakeInteractiveRunner) StartProgram(
	_ context.Context, _ Workspace, spec InteractiveProgramSpec,
) (ProgramSession, error) {
	r.lastInteractiveSpec = spec
	return nil, nil
}

type fakeEngine struct {
	runner ProgramRunner
}

func (e *fakeEngine) Manager() WorkspaceManager { return nil }
func (e *fakeEngine) FS() WorkspaceFS           { return nil }
func (e *fakeEngine) Runner() ProgramRunner     { return e.runner }
func (e *fakeEngine) Describe() Capabilities    { return Capabilities{} }

// --- tests ---

func TestMergeProviderEnv_InjectsNewKeys(t *testing.T) {
	spec := RunProgramSpec{
		Cmd: "echo",
		Env: map[string]string{"EXISTING": "val"},
	}
	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		return map[string]string{
			"TOKEN":    "secret",
			"EXISTING": "should-not-override",
		}
	})
	mergeProviderEnv(context.Background(), provider, &spec)

	assert.Equal(t, "secret", spec.Env["TOKEN"])
	assert.Equal(t, "val", spec.Env["EXISTING"])
}

func TestMergeProviderEnv_NilProvider(t *testing.T) {
	spec := RunProgramSpec{Cmd: "echo"}
	mergeProviderEnv(context.Background(), nil, &spec)
	assert.Nil(t, spec.Env)
}

func TestMergeProviderEnv_NilSpecEnv(t *testing.T) {
	spec := RunProgramSpec{Cmd: "echo"}
	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		return map[string]string{"TOKEN": "abc"}
	})
	mergeProviderEnv(context.Background(), provider, &spec)

	require.NotNil(t, spec.Env)
	assert.Equal(t, "abc", spec.Env["TOKEN"])
}

func TestMergeProviderEnv_EmptyReturn(t *testing.T) {
	spec := RunProgramSpec{Cmd: "echo"}
	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		return nil
	})
	mergeProviderEnv(context.Background(), provider, &spec)
	assert.Nil(t, spec.Env)
}

func TestMergeProviderEnv_DoesNotMutateCallerEnv(t *testing.T) {
	type ctxKey struct{}
	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		if v, ok := ctx.Value(ctxKey{}).(string); ok {
			return map[string]string{"TOKEN": v}
		}
		return nil
	})

	shared := map[string]string{"BASE": "1"}

	specA := RunProgramSpec{Cmd: "echo", Env: shared}
	mergeProviderEnv(context.WithValue(context.Background(), ctxKey{}, "user-a"), provider, &specA)
	assert.Equal(t, "user-a", specA.Env["TOKEN"])
	_, leaked := shared["TOKEN"]
	assert.False(t, leaked, "original caller env map must not be mutated")

	specB := RunProgramSpec{Cmd: "echo", Env: shared}
	mergeProviderEnv(context.WithValue(context.Background(), ctxKey{}, "user-b"), provider, &specB)
	assert.Equal(t, "user-b", specB.Env["TOKEN"], "second run must get its own token")
}

func TestNewEnvInjectingEngine_NilArgs(t *testing.T) {
	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		return map[string]string{"K": "V"}
	})
	assert.Nil(t, NewEnvInjectingEngine(nil, provider))

	inner := &fakeEngine{runner: &fakeRunner{}}
	assert.Same(t, inner, NewEnvInjectingEngine(inner, nil))
}

func TestEnvEngine_RunProgram(t *testing.T) {
	fr := &fakeRunner{}
	inner := &fakeEngine{runner: fr}

	type ctxKey struct{}
	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		if v, ok := ctx.Value(ctxKey{}).(string); ok {
			return map[string]string{"USER_TOKEN": v}
		}
		return nil
	})

	eng := NewEnvInjectingEngine(inner, provider)

	ctx := context.WithValue(context.Background(), ctxKey{}, "tok-123")
	ws := Workspace{ID: "ws", Path: "/tmp/ws"}
	spec := RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-c", "echo hello"},
		Timeout: 5 * time.Second,
	}

	rr, err := eng.Runner().RunProgram(ctx, ws, spec)
	require.NoError(t, err)
	assert.Equal(t, "ok", rr.Stdout)
	assert.Equal(t, "tok-123", fr.lastSpec.Env["USER_TOKEN"])
}

func TestEnvEngine_RunProgram_NoOverride(t *testing.T) {
	fr := &fakeRunner{}
	inner := &fakeEngine{runner: fr}

	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		return map[string]string{"TOKEN": "from-provider"}
	})
	eng := NewEnvInjectingEngine(inner, provider)

	ws := Workspace{ID: "ws", Path: "/tmp/ws"}
	spec := RunProgramSpec{
		Cmd: "bash",
		Env: map[string]string{"TOKEN": "from-tool"},
	}

	_, err := eng.Runner().RunProgram(context.Background(), ws, spec)
	require.NoError(t, err)
	assert.Equal(t, "from-tool", fr.lastSpec.Env["TOKEN"])
}

func TestEnvEngine_InteractiveRunner(t *testing.T) {
	fir := &fakeInteractiveRunner{}
	inner := &fakeEngine{runner: fir}

	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		return map[string]string{"SECRET": "abc"}
	})
	eng := NewEnvInjectingEngine(inner, provider)

	runner := eng.Runner()

	ir, ok := runner.(InteractiveProgramRunner)
	require.True(t, ok, "wrapped runner should implement InteractiveProgramRunner")

	ws := Workspace{ID: "ws", Path: "/tmp/ws"}
	spec := InteractiveProgramSpec{
		RunProgramSpec: RunProgramSpec{Cmd: "sh"},
		TTY:            true,
	}
	_, _ = ir.StartProgram(context.Background(), ws, spec)

	assert.Equal(t, "abc", fir.lastInteractiveSpec.Env["SECRET"])
}

func TestEnvEngine_NonInteractiveRunner(t *testing.T) {
	fr := &fakeRunner{}
	inner := &fakeEngine{runner: fr}

	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		return map[string]string{"K": "V"}
	})
	eng := NewEnvInjectingEngine(inner, provider)

	runner := eng.Runner()
	_, ok := runner.(InteractiveProgramRunner)
	assert.False(t, ok, "non-interactive inner should not expose InteractiveProgramRunner")
}

func TestEnvEngine_DelegatesManagerFSDescribe(t *testing.T) {
	inner := &fakeEngine{runner: &fakeRunner{}}
	eng := NewEnvInjectingEngine(inner, func(ctx context.Context) map[string]string {
		return nil
	})

	assert.Nil(t, eng.Manager())
	assert.Nil(t, eng.FS())
	assert.Equal(t, Capabilities{}, eng.Describe())
}

func TestEnvEngine_NilRunner(t *testing.T) {
	inner := &fakeEngine{runner: nil}
	eng := NewEnvInjectingEngine(inner, func(ctx context.Context) map[string]string {
		return map[string]string{"K": "V"}
	})
	assert.Nil(t, eng.Runner())
}

// --- CodeExecutor wrapper tests ---

type fakeCodeExecutor struct {
	eng Engine
}

func (e *fakeCodeExecutor) ExecuteCode(
	_ context.Context, _ CodeExecutionInput,
) (CodeExecutionResult, error) {
	return CodeExecutionResult{Output: "exec"}, nil
}
func (e *fakeCodeExecutor) CodeBlockDelimiter() CodeBlockDelimiter {
	return CodeBlockDelimiter{Start: "```", End: "```"}
}
func (e *fakeCodeExecutor) Engine() Engine { return e.eng }

func TestNewEnvInjectingCodeExecutor_WrapsEngine(t *testing.T) {
	fr := &fakeRunner{}
	inner := &fakeCodeExecutor{eng: &fakeEngine{runner: fr}}

	provider := RunEnvProvider(func(ctx context.Context) map[string]string {
		return map[string]string{"INJECTED": "yes"}
	})
	wrapped := NewEnvInjectingCodeExecutor(inner, provider)

	ep, ok := wrapped.(EngineProvider)
	require.True(t, ok)

	eng := ep.Engine()
	require.NotNil(t, eng)

	ws := Workspace{ID: "ws", Path: "/tmp/ws"}
	_, err := eng.Runner().RunProgram(context.Background(), ws, RunProgramSpec{Cmd: "echo"})
	require.NoError(t, err)
	assert.Equal(t, "yes", fr.lastSpec.Env["INJECTED"])
}

func TestNewEnvInjectingCodeExecutor_PreservesCodeExecutor(t *testing.T) {
	fr := &fakeRunner{}
	inner := &fakeCodeExecutor{eng: &fakeEngine{runner: fr}}

	wrapped := NewEnvInjectingCodeExecutor(inner, func(ctx context.Context) map[string]string {
		return nil
	})

	result, err := wrapped.ExecuteCode(context.Background(), CodeExecutionInput{})
	require.NoError(t, err)
	assert.Equal(t, "exec", result.Output)
	assert.Equal(t, "```", wrapped.CodeBlockDelimiter().Start)
}

func TestNewEnvInjectingCodeExecutor_NilArgs(t *testing.T) {
	inner := &fakeCodeExecutor{eng: &fakeEngine{runner: &fakeRunner{}}}
	assert.Nil(t, NewEnvInjectingCodeExecutor(nil, func(ctx context.Context) map[string]string { return nil }))
	assert.Same(t, inner, NewEnvInjectingCodeExecutor(inner, nil))
}

type noEngineExecutor struct{}

func (e *noEngineExecutor) ExecuteCode(
	_ context.Context, _ CodeExecutionInput,
) (CodeExecutionResult, error) {
	return CodeExecutionResult{}, nil
}
func (e *noEngineExecutor) CodeBlockDelimiter() CodeBlockDelimiter {
	return CodeBlockDelimiter{}
}

func TestNewEnvInjectingCodeExecutor_NoEngineProvider(t *testing.T) {
	inner := &noEngineExecutor{}
	wrapped := NewEnvInjectingCodeExecutor(inner, func(ctx context.Context) map[string]string {
		return map[string]string{"K": "V"}
	})
	assert.Same(t, inner, wrapped)
}
