//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestInvocationWorkspaceRegistry_ScopeAndViews(t *testing.T) {
	inv := agent.NewInvocation()
	view := inv.View()
	base := codeexecutor.NewWorkspaceRegistry()
	want := view.GetOrCreateWorkspaceRegistry(base)
	require.NotSame(t, base, want)
	require.Same(t, want, inv.GetOrCreateWorkspaceRegistry(base))
	require.Same(t, want, inv.View().GetOrCreateWorkspaceRegistry(base))
	require.NotSame(t, want, inv.Clone().GetOrCreateWorkspaceRegistry(base))
	require.NotSame(t, want, agent.NewInvocation().GetOrCreateWorkspaceRegistry(base))
	require.NotSame(t, want, inv.GetOrCreateWorkspaceRegistry(codeexecutor.NewWorkspaceRegistry()))
}

func TestInvocationWorkspaceRegistry_ConcurrentInitialization(t *testing.T) {
	inv := &agent.Invocation{}
	base := codeexecutor.NewWorkspaceRegistry()
	var wg sync.WaitGroup
	results := make(chan *codeexecutor.WorkspaceRegistry, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- inv.View().GetOrCreateWorkspaceRegistry(base)
		}()
	}
	wg.Wait()
	close(results)
	want := inv.GetOrCreateWorkspaceRegistry(base)
	for got := range results {
		require.Same(t, want, got)
	}
}

func TestInvocationWorkspaceRegistry_NilValues(t *testing.T) {
	inv := &agent.Invocation{}
	require.Same(t, inv.GetOrCreateWorkspaceRegistry(nil), inv.GetOrCreateWorkspaceRegistry(nil))
	var missing *agent.Invocation
	require.NotSame(t, missing.GetOrCreateWorkspaceRegistry(nil), missing.GetOrCreateWorkspaceRegistry(nil))
}
