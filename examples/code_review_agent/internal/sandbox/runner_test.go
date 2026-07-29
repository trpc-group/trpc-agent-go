//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestCoordinatorRunsFixedChecksWithIsolationAndOfflineEnvironment(t *testing.T) {
	backend := &fakeBackend{
		capabilities: codeexecutor.Capabilities{SupportsCleanEnv: true},
		results: []fakeRunResult{
			{result: codeexecutor.RunResult{
				Stderr:   "x.go:2:3: password=sk-test-super-secret-value-123456\n",
				ExitCode: 1,
				Duration: time.Second,
			}},
			{result: codeexecutor.RunResult{ExitCode: 0, Duration: 2 * time.Second}},
			{err: errors.New("executable file not found")},
		},
	}
	coordinator, err := New(backend.engine(), Config{
		EnableStaticcheck: true,
		Timeout:           30 * time.Second,
		CleanupTimeout:    time.Second,
		MaxOutputBytes:    4096,
	})
	require.NoError(t, err)

	result, err := coordinator.Run(context.Background(), Request{
		TaskID: "task-1",
		Diff:   oneAddedLineDiff(t),
		Files: []codeexecutor.PutFile{{
			Path: "x.go", Content: []byte("package x\nvar X = 1\n"), Mode: 0o644,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, codeexecutor.WorkspacePolicy{
		Isolated: true, MaxDiskBytes: defaultMaxDiskBytes,
	}, backend.policy)
	require.True(t, backend.cleaned)
	require.Len(t, backend.staged, 1)
	require.Equal(t, uint32(0o444), backend.staged[0].Mode)
	require.Len(t, backend.specs, 3)
	require.Equal(t, "go", backend.specs[0].Cmd)
	require.Equal(t, []string{"test", "./..."}, backend.specs[0].Args)
	for _, spec := range backend.specs {
		require.True(t, spec.CleanEnv)
		require.Equal(t, "off", spec.Env["GOPROXY"])
		require.Equal(t, "off", spec.Env["GOSUMDB"])
		require.Empty(t, spec.Stdin)
		require.Equal(t, ".", spec.Cwd)
	}
	require.Equal(t, []review.SandboxStatus{
		review.SandboxStatusFailed,
		review.SandboxStatusCompleted,
		review.SandboxStatusUnavailable,
	}, []review.SandboxStatus{result.Runs[0].Status, result.Runs[1].Status, result.Runs[2].Status})
	require.Len(t, result.Candidates, 1)
	require.NotContains(t, result.Runs[0].Stderr, "sk-test-super-secret-value-123456")
	require.NotContains(t, result.Candidates[0].Evidence, "sk-test-super-secret-value-123456")
}

func TestNewFailsClosedWithoutCleanEnvironmentOrWithNetwork(t *testing.T) {
	for _, capabilities := range []codeexecutor.Capabilities{
		{},
		{SupportsCleanEnv: true, NetworkAllowed: true},
	} {
		backend := &fakeBackend{capabilities: capabilities}
		_, err := New(backend.engine(), Config{})
		require.Error(t, err)
	}
}

func TestCoordinatorRecordsTimeoutTruncatesOutputAndCleansUp(t *testing.T) {
	backend := &fakeBackend{
		capabilities: codeexecutor.Capabilities{SupportsCleanEnv: true},
		results: []fakeRunResult{{result: codeexecutor.RunResult{
			Stdout: "authorization: Bearer sk-test-super-secret-value-123456\n" +
				strings.Repeat("x", 128),
			Duration: time.Second,
			TimedOut: true,
		}}},
	}
	coordinator, err := New(backend.engine(), Config{
		Checks:         []Check{CheckGoTest},
		MaxOutputBytes: 48,
	})
	require.NoError(t, err)
	result, err := coordinator.Run(context.Background(), Request{
		TaskID: "task-1", Diff: oneAddedLineDiff(t),
	})
	require.NoError(t, err)
	require.True(t, backend.cleaned)
	require.Equal(t, review.SandboxStatusTimedOut, result.Runs[0].Status)
	require.True(t, result.Runs[0].TimedOut)
	require.True(t, result.Runs[0].Truncated)
	require.NotContains(t, result.Runs[0].Stdout, "sk-test-super-secret-value-123456")
	require.NoError(t, result.Runs[0].Validate())
}

func oneAddedLineDiff(t *testing.T) input.Diff {
	t.Helper()
	diff, err := input.Parse(strings.NewReader("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1 +1,2 @@\n package x\n+var X = 1\n"))
	require.NoError(t, err)
	return diff
}

type fakeRunResult struct {
	result codeexecutor.RunResult
	err    error
}

type fakeBackend struct {
	capabilities codeexecutor.Capabilities
	policy       codeexecutor.WorkspacePolicy
	staged       []codeexecutor.PutFile
	specs        []codeexecutor.RunProgramSpec
	results      []fakeRunResult
	cleaned      bool
}

func (f *fakeBackend) engine() codeexecutor.Engine {
	return codeexecutor.NewEngineWithCapabilities(f, f, f, f.capabilities)
}

func (f *fakeBackend) CreateWorkspace(
	_ context.Context, id string, policy codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	f.policy = policy
	return codeexecutor.Workspace{ID: id, Path: "/workspace"}, nil
}

func (f *fakeBackend) Cleanup(context.Context, codeexecutor.Workspace) error {
	f.cleaned = true
	return nil
}

func (f *fakeBackend) PutFiles(
	_ context.Context, _ codeexecutor.Workspace, files []codeexecutor.PutFile,
) error {
	f.staged = append([]codeexecutor.PutFile(nil), files...)
	return nil
}

func (*fakeBackend) StageDirectory(
	context.Context, codeexecutor.Workspace, string, string, codeexecutor.StageOptions,
) error {
	return nil
}

func (*fakeBackend) Collect(
	context.Context, codeexecutor.Workspace, []string,
) ([]codeexecutor.File, error) {
	return nil, nil
}

func (*fakeBackend) StageInputs(
	context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec,
) error {
	return nil
}

func (*fakeBackend) CollectOutputs(
	context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

func (f *fakeBackend) RunProgram(
	_ context.Context, _ codeexecutor.Workspace, spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	f.specs = append(f.specs, spec)
	if len(f.results) == 0 {
		return codeexecutor.RunResult{}, errors.New("unexpected run")
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result.result, result.err
}
