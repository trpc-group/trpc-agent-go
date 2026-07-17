//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestNewSandboxRunnerFallbacks(t *testing.T) {
	_, err := NewSandboxRunner(ReviewOptions{Runtime: "local"})
	require.Error(t, err)
	r, err := NewSandboxRunner(ReviewOptions{Runtime: "local", AllowTrustedLocal: true, DryRun: true})
	require.NoError(t, err)
	require.NoError(t, r.Close())
	r, err = NewSandboxRunner(ReviewOptions{Runtime: "fake", OutputLimit: -1, SandboxTimeout: -1})
	require.NoError(t, err)
	require.NoError(t, r.Close())
	_, err = NewSandboxRunner(ReviewOptions{Runtime: "unknown"})
	require.Error(t, err)
}

func TestEngineRunnerDryRunAndExecution(t *testing.T) {
	runner := &stubRunner{result: codeexecutor.RunResult{
		Stdout:   "stdout",
		Stderr:   "stderr",
		ExitCode: 0,
		Duration: time.Millisecond,
	}}
	exec := &stubCodeExecutor{eng: stubEngine{runner: runner}}
	r := &engineRunner{runtime: "stub", exec: exec, timeout: time.Second, outputLimit: 100, dryRun: true}
	gate := NewCommandGate()
	runs, err := r.Run(context.Background(), "task", []string{"go test ./..."}, gate)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "dry_run", runs[0].Status)

	r.dryRun = false
	runs, err = r.Run(context.Background(), "task", []string{"go test ./..."}, NewCommandGate())
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "completed", runs[0].Status)
	require.Equal(t, "stdoutstderr", runs[0].Output)
	require.Len(t, runner.specs, 1)
	require.Equal(t, "go", runner.specs[0].Cmd)
	require.Equal(t, []string{"test", "./..."}, runner.specs[0].Args)
	require.Equal(t, ".", runner.specs[0].Cwd)

	r.repoPath = "."
	r.skillsRoot = "skills"
	runs, err = r.Run(context.Background(), "task", []string{skillScriptCommand}, NewCommandGate())
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "completed", runs[0].Status)
	require.Len(t, runner.specs, 2)
	require.Equal(t, "bash", runner.specs[1].Cmd)
	require.Equal(t, []string{"../skills/code-review/scripts/run_go_checks.sh"}, runner.specs[1].Args)
	require.Equal(t, "repo", runner.specs[1].Cwd)

	runs, err = r.Run(context.Background(), "task", []string{"curl http://example.com"}, NewCommandGate())
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "deny", runs[0].Status)
}

func TestEngineRunnerErrors(t *testing.T) {
	r := &engineRunner{}
	_, err := r.Run(context.Background(), "task", []string{"go test ./..."}, NewCommandGate())
	require.Error(t, err)

	r = &engineRunner{exec: &stubCodeExecutor{}}
	_, err = r.Run(context.Background(), "task", []string{"go test ./..."}, NewCommandGate())
	require.Error(t, err)

	exec := &stubCodeExecutor{eng: stubEngine{runner: &stubRunner{err: errors.New("boom")}}}
	r = &engineRunner{runtime: "stub", exec: exec, timeout: time.Second, outputLimit: 100}
	runs, err := r.Run(context.Background(), "task", []string{"go test ./..."}, NewCommandGate())
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "failed", runs[0].Status)
	require.NoError(t, r.Close())

	closer := &closeableStubCodeExecutor{stubCodeExecutor: stubCodeExecutor{eng: stubEngine{runner: &stubRunner{}}}}
	r = &engineRunner{runtime: "stub", exec: closer}
	require.NoError(t, r.Close())
	require.True(t, closer.closed)
	require.NoError(t, (*engineRunner)(nil).Close())
	require.Equal(t, "abc", mustLimitOutput(t, "abc", 10))
	require.Equal(t, "abc", mustLimitOutput(t, "abc", 0))
	limited, truncated := limitOutput("abcdefghijklmnopqrstuvwxyz0123456789", 32)
	require.True(t, truncated)
	require.Contains(t, limited, "[output truncated]")
}

func mustLimitOutput(t *testing.T, out string, max int64) string {
	t.Helper()
	limited, truncated := limitOutput(out, max)
	require.False(t, truncated)
	return limited
}

type stubCodeExecutor struct {
	eng codeexecutor.Engine
}

func (s *stubCodeExecutor) ExecuteCode(context.Context, codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (s *stubCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (s *stubCodeExecutor) Engine() codeexecutor.Engine { return s.eng }

type closeableStubCodeExecutor struct {
	stubCodeExecutor
	closed bool
}

func (s *closeableStubCodeExecutor) Close() error {
	s.closed = true
	return nil
}

type stubEngine struct {
	runner codeexecutor.ProgramRunner
}

func (s stubEngine) Manager() codeexecutor.WorkspaceManager { return stubManager{} }
func (s stubEngine) FS() codeexecutor.WorkspaceFS           { return stubFS{} }
func (s stubEngine) Runner() codeexecutor.ProgramRunner     { return s.runner }
func (s stubEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{SupportsCleanEnv: true}
}

type stubManager struct{}

func (stubManager) CreateWorkspace(context.Context, string, codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: "ws", Path: "."}, nil
}

func (stubManager) Cleanup(context.Context, codeexecutor.Workspace) error { return nil }

type stubFS struct{}

func (stubFS) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return nil
}
func (stubFS) StageDirectory(context.Context, codeexecutor.Workspace, string, string, codeexecutor.StageOptions) error {
	return nil
}
func (stubFS) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return []codeexecutor.File{}, nil
}
func (stubFS) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return nil
}
func (stubFS) CollectOutputs(context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type stubRunner struct {
	result codeexecutor.RunResult
	err    error
	specs  []codeexecutor.RunProgramSpec
}

func (s *stubRunner) RunProgram(_ context.Context, _ codeexecutor.Workspace, spec codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error) {
	s.specs = append(s.specs, spec)
	return s.result, s.err
}
