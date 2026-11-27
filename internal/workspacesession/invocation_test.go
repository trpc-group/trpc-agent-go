//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspacesession

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type invocationManager struct {
	perInvocation bool
	creates       atomic.Int64
}

func (m *invocationManager) WorkspacePerInvocation() bool { return m.perInvocation }

func (m *invocationManager) CreateWorkspace(
	_ context.Context, id string, _ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: id, Path: fmt.Sprintf("/work/%d", m.creates.Add(1))}, nil
}

func (*invocationManager) Cleanup(context.Context, codeexecutor.Workspace) error { return nil }

func TestResolver_InvocationWorkspaceReuse(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprint(scoped), func(t *testing.T) {
			mgr := &invocationManager{perInvocation: scoped}
			eng := codeexecutor.NewEngine(mgr, &resolverStubFS{}, &resolverStubRunner{})
			base := codeexecutor.NewWorkspaceRegistry()
			first := agent.NewInvocation()
			first.Session = &session.Session{ID: "same-session"}
			viewBeforeAcquire := first.View()
			acquire := func(inv *agent.Invocation) codeexecutor.WorkspaceHandle {
				t.Helper()
				ctx := agent.NewInvocationContext(context.Background(), inv)
				// Tools construct separate resolvers around the shared registry.
				h, err := NewResolver(nil, base).CreateWorkspaceHandle(ctx, eng, "workspace")
				require.NoError(t, err)
				return h
			}
			h1 := acquire(first)
			require.Equal(t, h1, acquire(first))
			require.Equal(t, h1, acquire(viewBeforeAcquire))
			require.Equal(t, h1, acquire(first.View()))
			require.Equal(t, int64(1), mgr.creates.Load())
			second := agent.NewInvocation()
			second.Session = first.Session
			h2 := acquire(second)
			if scoped {
				require.NotEqual(t, h1.Workspace.Path, h2.Workspace.Path)
				require.NotEqual(t, h1.Workspace.Path, acquire(first.Clone()).Workspace.Path)
			} else {
				require.Equal(t, h1, h2)
				require.Equal(t, h1, acquire(first.Clone()))
			}
			require.Equal(t, h1, acquire(first), "another invocation must not replace this cache")
		})
	}
}

func TestResolver_InvocationRegistryInvalidation(t *testing.T) {
	mgr := &invocationManager{perInvocation: true}
	eng := codeexecutor.NewEngine(mgr, &resolverStubFS{}, &resolverStubRunner{})
	base := codeexecutor.NewWorkspaceRegistry()
	inv := agent.NewInvocation()
	inv.Session = &session.Session{ID: "session"}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	r := NewResolver(nil, base)
	stale, err := r.CreateWorkspaceHandle(ctx, eng, "workspace")
	require.NoError(t, err)
	require.False(t, NewResolver(nil, nil).InvalidateWorkspaceHandle(stale), "unrelated resolvers cannot invalidate the entry")
	require.True(t, r.InvalidateWorkspaceHandle(stale))
	current, err := r.CreateWorkspaceHandle(ctx, eng, "workspace")
	require.NoError(t, err)
	require.NotEqual(t, stale.Workspace.Path, current.Workspace.Path)
	require.False(t, r.InvalidateWorkspaceHandle(stale), "old handles must not evict replacements")
	reused, err := r.CreateWorkspaceHandle(ctx, eng, "workspace")
	require.NoError(t, err)
	require.Equal(t, current, reused)
}

func TestResolver_ConcurrentInvocationRegistry(t *testing.T) {
	mgr := &invocationManager{perInvocation: true}
	eng := codeexecutor.NewEngine(mgr, &resolverStubFS{}, &resolverStubRunner{})
	base := codeexecutor.NewWorkspaceRegistry()
	inv := agent.NewInvocation()
	inv.Session = &session.Session{ID: "session"}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	var wg sync.WaitGroup
	results := make(chan codeexecutor.WorkspaceHandle, 24)
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := NewResolver(nil, base).CreateWorkspaceHandle(ctx, eng, "workspace")
			results <- h
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var want codeexecutor.WorkspaceHandle
	for got := range results {
		if want.Workspace.Path == "" {
			want = got
		}
		require.Equal(t, want, got)
	}
	require.Equal(t, int64(1), mgr.creates.Load())
}

func TestResolver_InvocationScopeWithoutContextKeepsManualReuse(t *testing.T) {
	mgr := &invocationManager{perInvocation: true}
	eng := codeexecutor.NewEngine(mgr, &resolverStubFS{}, &resolverStubRunner{})
	r := NewResolver(nil, nil)
	first, err := r.CreateWorkspace(context.Background(), eng, "manual")
	require.NoError(t, err)
	second, err := r.CreateWorkspace(context.Background(), eng, "manual")
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, int64(1), mgr.creates.Load())
}
