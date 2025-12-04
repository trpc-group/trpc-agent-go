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
