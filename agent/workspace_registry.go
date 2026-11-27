//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

type invocationWorkspaceRegistries struct {
	mu     sync.Mutex
	byBase map[*codeexecutor.WorkspaceRegistry]*codeexecutor.WorkspaceRegistry
}

// GetOrCreateWorkspaceRegistry returns an invocation-local scope of base.
// Repeated calls with the same base, including through invocation views, return
// the same scope. Child invocations created with Clone get independent scopes.
// Distinct base registries remain independent. The invocation owns only the
// cache lifetime; callers retain responsibility for workspace cleanup.
// It is safe to call concurrently. A nil base uses an independent registry;
// a nil invocation returns a fresh scope on each call.
func (inv *Invocation) GetOrCreateWorkspaceRegistry(
	base *codeexecutor.WorkspaceRegistry,
) *codeexecutor.WorkspaceRegistry {
	if inv == nil {
		return base.NewScope()
	}
	registries := inv.workspaceRegistryState()
	registries.mu.Lock()
	defer registries.mu.Unlock()
	if reg := registries.byBase[base]; reg != nil {
		return reg
	}
	if registries.byBase == nil {
		registries.byBase = make(map[*codeexecutor.WorkspaceRegistry]*codeexecutor.WorkspaceRegistry)
	}
	reg := base.NewScope()
	registries.byBase[base] = reg
	return reg
}

func (inv *Invocation) workspaceRegistryState() *invocationWorkspaceRegistries {
	if state := inv.workspaceRegistries.Load(); state != nil {
		return state
	}
	state := &invocationWorkspaceRegistries{}
	if inv.workspaceRegistries.CompareAndSwap(nil, state) {
		return state
	}
	return inv.workspaceRegistries.Load()
}
