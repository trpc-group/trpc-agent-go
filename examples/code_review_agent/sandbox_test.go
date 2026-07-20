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
	r, err = newContainerRunner(ReviewOptions{Runtime: "container", DryRun: true}, time.Second, 64)
	require.NoError(t, err)
	engine, ok := r.(*engineRunner)
	require.True(t, ok)
	require.Nil(t, engine.exec)
	require.True(t, engine.dryRun)
	r, err = newE2BRunner(ReviewOptions{Runtime: "e2b", DryRun: true}, time.Second, 64)
	require.NoError(t, err)
	engine, ok = r.(*engineRunner)
	require.True(t, ok)
	require.Nil(t, engine.exec)
	require.True(t, engine.dryRun)
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
	fs := &stubFS{}
	exec := &stubCodeExecutor{eng: stubEngine{runner: runner, fs: fs}}
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
	r.goModCache = "host-mod-cache"
	runs, err = r.Run(context.Background(), "task", []string{skillScriptCommand}, NewCommandGate())
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "completed", runs[0].Status)
	require.Len(t, runner.specs, 2)
	require.Equal(t, "bash", runner.specs[1].Cmd)
	require.Equal(t, []string{"../skills/code-review/scripts/run_go_checks.sh"}, runner.specs[1].Args)
	require.Equal(t, "repo", runner.specs[1].Cwd)
	require.Equal(t, "../gomodcache", runner.specs[1].Env["GOMODCACHE"])
	require.Equal(t, "../gocache", runner.specs[1].Env["GOCACHE"])
	require.Contains(t, fs.stageCalls, stageCall{src: ".", to: "repo"})
	require.Contains(t, fs.stageCalls, stageCall{src: "skills", to: "skills"})
	require.Contains(t, fs.stageCalls, stageCall{src: "host-mod-cache", to: "gomodcache"})

	r.outputLimit = 40
	runner.result.Stdout = `token="AKID1234567890SECRET"`
	runs, err = r.Run(context.Background(), "task", []string{"go test ./..."}, NewCommandGate())
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.NotContains(t, runs[0].Output, "AKID1234567890SECRET")
	require.Contains(t, runs[0].Output, "[REDACTED]")

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
	require.Len(t, limited, 32)

	r = &engineRunner{runtime: "stub", dryRun: true}
	runs, err = r.Run(context.Background(), "task", []string{"go test ./..."}, NewCommandGate())
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "dry_run", runs[0].Status)
}

func TestTotalSandboxLifetimeCoversFullCommandSequence(t *testing.T) {
	timeout := 30 * time.Second
	lifetime := totalSandboxLifetime(timeout, []string{"go test ./...", "go vet ./...", skillScriptCommand})
	require.Greater(t, lifetime, timeout*2)
	require.Equal(t, 160*time.Second, lifetime)
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
	fs     codeexecutor.WorkspaceFS
}

func (s stubEngine) Manager() codeexecutor.WorkspaceManager { return stubManager{} }
func (s stubEngine) FS() codeexecutor.WorkspaceFS {
	if s.fs != nil {
		return s.fs
	}
	return &stubFS{}
}
func (s stubEngine) Runner() codeexecutor.ProgramRunner { return s.runner }
func (s stubEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{SupportsCleanEnv: true}
}

type stubManager struct{}

func (stubManager) CreateWorkspace(context.Context, string, codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: "ws", Path: "."}, nil
}

func (stubManager) Cleanup(context.Context, codeexecutor.Workspace) error { return nil }

type stageCall struct {
	src string
	to  string
}

type stubFS struct {
	stageCalls []stageCall
}

func (*stubFS) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return nil
}
func (s *stubFS) StageDirectory(_ context.Context, _ codeexecutor.Workspace, src string, to string, _ codeexecutor.StageOptions) error {
	s.stageCalls = append(s.stageCalls, stageCall{src: src, to: to})
	return nil
}
func (*stubFS) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return []codeexecutor.File{}, nil
}
func (*stubFS) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return nil
}
func (*stubFS) CollectOutputs(context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
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
