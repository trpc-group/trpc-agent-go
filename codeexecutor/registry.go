//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"context"
	"sync"
)

// WorkspaceRegistry keeps a process-level mapping of logical IDs to
// created workspaces for reuse within a session.
type WorkspaceRegistry struct {
	mu       sync.Mutex
	byID     map[string]Workspace
	inflight map[string]*workspaceCreateCall
}

type workspaceCreateCall struct {
	done chan struct{}
	ws   Workspace
	err  error
}

// NewWorkspaceRegistry creates a new in-memory registry.
func NewWorkspaceRegistry() *WorkspaceRegistry {
	return &WorkspaceRegistry{
		byID:     map[string]Workspace{},
		inflight: map[string]*workspaceCreateCall{},
	}
}

// Acquire creates or returns an existing workspace with the given id.
// Concurrent first-time acquires for the same id coalesce to a single
// CreateWorkspace so init hooks and workspace creation run at most once per id.
func (r *WorkspaceRegistry) Acquire(
	ctx context.Context, m WorkspaceManager, id string,
) (Workspace, error) {
	r.mu.Lock()
	if ws, ok := r.byID[id]; ok {
		r.mu.Unlock()
		return ws, nil
	}
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return Workspace{}, err
	}
	if call, ok := r.inflight[id]; ok {
		r.mu.Unlock()
		return waitWorkspaceCreate(ctx, call)
	}
	if r.inflight == nil {
		r.inflight = map[string]*workspaceCreateCall{}
	}
	call := &workspaceCreateCall{done: make(chan struct{})}
	r.inflight[id] = call
	createCtx := context.WithoutCancel(ctx)
	r.mu.Unlock()

	go r.createWorkspace(createCtx, m, id, call)
	return waitWorkspaceCreate(ctx, call)
}

func (r *WorkspaceRegistry) createWorkspace(
	ctx context.Context,
	m WorkspaceManager,
	id string,
	call *workspaceCreateCall,
) {
	ws, err := m.CreateWorkspace(ctx, id, WorkspacePolicy{})

	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		if r.byID == nil {
			r.byID = map[string]Workspace{}
		}
		r.byID[id] = ws
	}
	call.ws = ws
	call.err = err
	delete(r.inflight, id)
	close(call.done)
}

func waitWorkspaceCreate(ctx context.Context, call *workspaceCreateCall) (Workspace, error) {
	select {
	case <-ctx.Done():
		return Workspace{}, ctx.Err()
	case <-call.done:
		if call.err != nil {
			return Workspace{}, call.err
		}
		return call.ws, nil
	}
}
