//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestGlobHost_VolumeRoot(t *testing.T) {
	root := t.TempDir()
	path, err := globHost(filepath.VolumeName(root) + string(os.PathSeparator))
	require.NoError(t, err)
	require.NotEmpty(t, path)
}

func TestGlobHost_InvalidPattern(t *testing.T) {
	_, err := globHost(filepath.Join(t.TempDir(), "["))
	require.Error(t, err)
}

func TestRuntimeCollect_InvalidGlob(t *testing.T) {
	rt := NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "rt-collect-invalid-glob", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	_, err = rt.Collect(ctx, ws, []string{"["})
	require.Error(t, err)
}

func TestRuntimeCollectOutputs_InvalidGlob(t *testing.T) {
	rt := NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "rt-collect-outputs-invalid-glob", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	_, err = rt.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{Globs: []string{"["}})
	require.Error(t, err)
}
