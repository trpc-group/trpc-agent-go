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
	"sync"
	"testing"

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
	require.True(t, errors.Is(err, ErrWorkspaceInitNeedsEngineProvider))
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

func TestWorkspaceRegistry_Acquire_ConcurrentCreatesOnce(t *testing.T) {
	reg := NewWorkspaceRegistry()
	wm := &fakeWM{ws: Workspace{Path: "/tmp/w"}}
	ctx := context.Background()

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := reg.Acquire(ctx, wm, "same-id")
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	require.Equal(t, 1, wm.calls,
		"concurrent first acquires must coalesce to one CreateWorkspace")
}
