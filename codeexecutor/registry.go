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
	"errors"
	"sync"
)

var errWorkspaceInstanceIDEmpty = errors.New(
	"codeexecutor: WorkspaceInstanceProvider returned an empty instance ID",
)

// WorkspaceRegistry keeps a process-level mapping of logical IDs to
// created workspaces for reuse within a session.
type WorkspaceRegistry struct {
	mu       sync.Mutex
	byID     map[string]workspaceRegistryEntry
	inflight map[string]*workspaceCreateCall
}

type workspaceRegistryEntry struct {
	ws         Workspace
	instanceID WorkspaceInstanceID
}

type workspaceCreateCall struct {
	done  chan struct{}
	entry workspaceRegistryEntry
	err   error
}

// NewWorkspaceRegistry creates a new in-memory registry.
func NewWorkspaceRegistry() *WorkspaceRegistry {
	return &WorkspaceRegistry{
		byID:     map[string]workspaceRegistryEntry{},
		inflight: map[string]*workspaceCreateCall{},
	}
}

// Acquire creates or returns an existing workspace with the given id.
// Concurrent first-time acquires for the same id coalesce to a single
// CreateWorkspace so init hooks and workspace creation run at most once per id.
//
// Managers that do not implement [WorkspaceInstanceProvider] retain the legacy
// cache behavior. For an instance-aware manager, a cache hit checks the current
// instance ID outside the registry lock. When the ID changes, concurrent
// callers coalesce to one CreateWorkspace call and the successful result
// atomically replaces the old entry. Validation and recreation failures leave
// the old entry cached so a later call can retry.
func (r *WorkspaceRegistry) Acquire(
	ctx context.Context, m WorkspaceManager, id string,
) (Workspace, error) {
	ws, _, err := r.AcquireWithInstanceID(ctx, m, id)
	return ws, err
}

// AcquireWithInstanceID is the instance-aware form of Acquire. It returns the
// instance ID stored atomically with the workspace entry. Legacy managers
// return an empty instance ID.
func (r *WorkspaceRegistry) AcquireWithInstanceID(
	ctx context.Context,
	m WorkspaceManager,
	id string,
) (Workspace, WorkspaceInstanceID, error) {
	provider, _ := m.(WorkspaceInstanceProvider)
	entry, err := r.acquire(ctx, m, provider, id)
	if err != nil {
		return Workspace{}, "", err
	}
	return entry.ws, entry.instanceID, nil
}

func (r *WorkspaceRegistry) acquire(
	ctx context.Context,
	m WorkspaceManager,
	provider WorkspaceInstanceProvider,
	id string,
) (workspaceRegistryEntry, error) {
	for {
		r.mu.Lock()
		entry, cached := r.byID[id]
		if cached && provider == nil {
			r.mu.Unlock()
			return entry, nil
		}
		if err := ctx.Err(); err != nil {
			r.mu.Unlock()
			return workspaceRegistryEntry{}, err
		}
		if call, ok := r.inflight[id]; ok {
			r.mu.Unlock()
			return waitWorkspaceCreate(ctx, call)
		}
		if !cached {
			call := r.newCreateCallLocked(id)
			createCtx := context.WithoutCancel(ctx)
			r.mu.Unlock()

			go r.createWorkspace(createCtx, m, provider, id, call)
			return waitWorkspaceCreate(ctx, call)
		}
		r.mu.Unlock()

		currentID, err := provider.WorkspaceInstanceID(ctx, entry.ws)
		if err != nil {
			return workspaceRegistryEntry{}, err
		}
		if currentID == "" {
			return workspaceRegistryEntry{}, errWorkspaceInstanceIDEmpty
		}

		r.mu.Lock()
		if call, ok := r.inflight[id]; ok {
			r.mu.Unlock()
			return waitWorkspaceCreate(ctx, call)
		}
		latest, ok := r.byID[id]
		if !ok || latest != entry {
			r.mu.Unlock()
			continue
		}
		if currentID == entry.instanceID {
			r.mu.Unlock()
			return entry, nil
		}
		call := r.newCreateCallLocked(id)
		createCtx := context.WithoutCancel(ctx)
		r.mu.Unlock()

		go r.createWorkspace(createCtx, m, provider, id, call)
		return waitWorkspaceCreate(ctx, call)
	}
}

func (r *WorkspaceRegistry) newCreateCallLocked(id string) *workspaceCreateCall {
	if r.inflight == nil {
		r.inflight = map[string]*workspaceCreateCall{}
	}
	call := &workspaceCreateCall{done: make(chan struct{})}
	r.inflight[id] = call
	return call
}

func (r *WorkspaceRegistry) createWorkspace(
	ctx context.Context,
	m WorkspaceManager,
	provider WorkspaceInstanceProvider,
	id string,
	call *workspaceCreateCall,
) {
	ws, err := m.CreateWorkspace(ctx, id, WorkspacePolicy{})
	entry := workspaceRegistryEntry{ws: ws}
	if err == nil && provider != nil {
		entry.instanceID, err = provider.WorkspaceInstanceID(ctx, ws)
		if err == nil && entry.instanceID == "" {
			err = errWorkspaceInstanceIDEmpty
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		if r.byID == nil {
			r.byID = map[string]workspaceRegistryEntry{}
		}
		r.byID[id] = entry
	}
	call.entry = entry
	call.err = err
	delete(r.inflight, id)
	close(call.done)
}

func waitWorkspaceCreate(
	ctx context.Context,
	call *workspaceCreateCall,
) (workspaceRegistryEntry, error) {
	select {
	case <-ctx.Done():
		return workspaceRegistryEntry{}, ctx.Err()
	case <-call.done:
		if call.err != nil {
			return workspaceRegistryEntry{}, call.err
		}
		return call.entry, nil
	}
}

// InvalidateIf removes id only when both its cached workspace handle and
// instance ID still match. It never calls [WorkspaceManager.Cleanup], because a
// deterministic workspace path may already belong to a newer physical
// instance by the time stale work is reported.
//
// The conditional comparison prevents a late stale report from evicting a
// workspace that another caller has already refreshed.
func (r *WorkspaceRegistry) InvalidateIf(
	id string,
	ws Workspace,
	instanceID WorkspaceInstanceID,
) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.byID[id]
	if !ok || entry.ws != ws || entry.instanceID != instanceID {
		return false
	}
	delete(r.byID, id)
	return true
}
