//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

// Cover NewEngine and simple wrappers to improve package coverage.
func TestNewEngine_And_Wrappers(t *testing.T) {
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	require.NotNil(t, eng)
	require.NotNil(t, eng.Manager())
	require.NotNil(t, eng.FS())
	require.NotNil(t, eng.Runner())
	// Describe returns capabilities; zero value is acceptable.
	_ = eng.Describe()

	ctx := context.Background()
	ws, err := eng.Manager().CreateWorkspace(
		ctx, "eng-ws", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = eng.Manager().Cleanup(ctx, ws) })

	err = eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    filepath.Join(codeexecutor.DirWork, "a.txt"),
		Content: []byte("x"),
		Mode:    0o644,
	}})
	require.NoError(t, err)
	files, err := eng.FS().Collect(
		ctx, ws, []string{"work/*.txt"},
	)
	require.NoError(t, err)
	if len(files) > 0 {
		require.Equal(t, "work/a.txt", files[0].Name)
	}
}

type nopWM struct{}

func (nopWM) CreateWorkspace(context.Context, string, codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: "x", Path: "/tmp/x"}, nil
}
func (nopWM) Cleanup(context.Context, codeexecutor.Workspace) error { return nil }

type nopFS struct{}

func (nopFS) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return nil
}
func (nopFS) PutDirectory(context.Context, codeexecutor.Workspace, string, string) error {
	return nil
}
func (nopFS) StageDirectory(context.Context, codeexecutor.Workspace, string, string, codeexecutor.StageOptions) error {
	return nil
}
func (nopFS) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return nil, nil
}
func (nopFS) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return codeexecutor.ErrDeclarativeIONotSupported
}
func (nopFS) CollectOutputs(context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, codeexecutor.ErrDeclarativeIONotSupported
}

type nopRunner struct{}

func (nopRunner) RunProgram(context.Context, codeexecutor.Workspace, codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{}, nil
}

func TestInvariant_Capability_ImmutableSentinels(t *testing.T) {
	p1 := codeexecutor.SupportsDeclarativeIOFalse()
	p2 := codeexecutor.SupportsDeclarativeIOFalse()
	require.False(t, *p1)
	require.False(t, *p2)
	require.NotSame(t, p1, p2, "each call must allocate a fresh *bool")

	eng := codeexecutor.NewEngineWithCapabilities(nopWM{}, nopFS{}, nopRunner{}, codeexecutor.Capabilities{
		SupportsDeclarativeIO: p1,
	})
	*p1 = true
	d := eng.Describe()
	require.NotNil(t, d.SupportsDeclarativeIO)
	require.False(t, *d.SupportsDeclarativeIO, "engine must keep construction-time value")

	*d.SupportsDeclarativeIO = true
	d2 := eng.Describe()
	require.False(t, *d2.SupportsDeclarativeIO)

	_, err := eng.FS().CollectOutputs(context.Background(), codeexecutor.Workspace{}, codeexecutor.OutputSpec{})
	require.ErrorIs(t, err, codeexecutor.ErrDeclarativeIONotSupported)
}
