//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func TestNewWorkspaceInitExecutor_NoHooksReturnsOriginal(t *testing.T) {
	inner := localexec.New(localexec.WithWorkDir(t.TempDir()))
	out, err := codeexecutor.NewWorkspaceInitExecutor(inner)
	require.NoError(t, err)
	require.Equal(t, inner, out)
}

func TestNewWorkspaceInitExecutor_RunsHookAfterCreate(t *testing.T) {
	tmp := t.TempDir()
	exec, err := codeexecutor.NewWorkspaceInitExecutor(
		localexec.New(localexec.WithWorkDir(tmp)),
		codeexecutor.NewWorkspaceInitHook(codeexecutor.WorkspaceInitSpec{
			Commands: []codeexecutor.WorkspaceInitCommand{
				{
					Name: "touch",
					Cmd:  "bash",
					Args: []string{
						"-lc",
						"mkdir -p work && touch work/.init-marker",
					},
					Timeout: 10 * time.Second,
				},
			},
		}),
	)
	require.NoError(t, err)
	ep, ok := exec.(codeexecutor.EngineProvider)
	require.True(t, ok)
	ctx := context.Background()
	ws, err := ep.Engine().Manager().CreateWorkspace(
		ctx,
		"session-a",
		codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = ep.Engine().Manager().Cleanup(ctx, ws)
	})

	p := filepath.Join(ws.Path, "work", ".init-marker")
	_, err = os.Stat(p)
	require.NoError(t, err, "init hook should create marker under workspace")
}

func TestNewWorkspaceInitExecutor_HookFailureTriggersCleanup(t *testing.T) {
	tmp := t.TempDir()
	exec, err := codeexecutor.NewWorkspaceInitExecutor(
		localexec.New(localexec.WithWorkDir(tmp)),
		codeexecutor.NewWorkspaceInitHook(codeexecutor.WorkspaceInitSpec{
			Commands: []codeexecutor.WorkspaceInitCommand{
				{
					Name:    "boom",
					Cmd:     "bash",
					Args:    []string{"-lc", "exit 7"},
					Timeout: 5 * time.Second,
				},
			},
		}),
	)
	require.NoError(t, err)
	ep := exec.(codeexecutor.EngineProvider)
	ctx := context.Background()
	_, err = ep.Engine().Manager().CreateWorkspace(
		ctx,
		"bad",
		codeexecutor.WorkspacePolicy{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "workspace init hook 0:")
	require.Contains(t, err.Error(), `command "boom"`)
	require.Contains(t, err.Error(), "exited 7")
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, entries, "failed init workspace should be cleaned up")
}
