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

var errWorkspaceRegistryTokenExhausted = errors.New(
	"codeexecutor: workspace registry entry token exhausted",
)

// WorkspaceRegistry keeps a process-level mapping of logical IDs to
// created workspaces for reuse within a session.
type WorkspaceRegistry struct {
	mu        sync.Mutex
	byID      map[string]workspaceRegistryEntry
	inflight  map[string]*workspaceCreateCall
	nextToken uint64
}

type workspaceRegistryEntry struct {
	ws                Workspace
	backendInstanceID WorkspaceInstanceID
	entryToken        uint64
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
	handle, err := r.AcquireHandle(ctx, m, id)
	return handle.Workspace, err
}

// AcquireHandle is the cache-entry-aware form of Acquire. The returned handle
// carries a registry-owned token that remains non-zero even for legacy
// managers, allowing stale work to invalidate only the exact entry it used.
func (r *WorkspaceRegistry) AcquireHandle(
	ctx context.Context,
	m WorkspaceManager,
	id string,
) (WorkspaceHandle, error) {
	provider, _ := m.(WorkspaceInstanceProvider)
	entry, err := r.acquire(ctx, m, provider, id)
	if err != nil {
		return WorkspaceHandle{}, err
	}
	return r.handle(id, entry), nil
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

		currentID, err := provider.InstanceID(ctx)
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
		if currentID == entry.backendInstanceID {
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
	var before WorkspaceInstanceID
	var err error
	if provider != nil {
		before, err = provider.InstanceID(ctx)
		if err == nil && before == "" {
			err = errWorkspaceInstanceIDEmpty
		}
	}
	var ws Workspace
	if err == nil {
		ws, err = m.CreateWorkspace(ctx, id, WorkspacePolicy{})
	}
	entry := workspaceRegistryEntry{
		ws:                ws,
		backendInstanceID: before,
	}
	if err == nil && provider != nil {
		after, probeErr := provider.InstanceID(ctx)
		switch {
		case probeErr != nil:
			err = probeErr
		case after == "":
			err = errWorkspaceInstanceIDEmpty
		case after != before:
			err = errors.Join(
				ErrWorkspaceStale,
				errors.New(
					"codeexecutor: workspace instance changed during creation",
				),
			)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		if r.nextToken == ^uint64(0) {
			err = errWorkspaceRegistryTokenExhausted
		} else {
			if r.byID == nil {
				r.byID = map[string]workspaceRegistryEntry{}
			}
			r.nextToken++
			entry.entryToken = r.nextToken
			r.byID[id] = entry
		}
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

// Invalidate removes only the exact cache entry represented by handle. It never
// calls [WorkspaceManager.Cleanup], because a deterministic workspace path may
// already belong to a newer physical instance by the time stale work is
// reported.
//
// The registry-owned token prevents a late stale report from evicting a
// workspace that another caller has already refreshed, including for legacy
// managers whose backend instance ID is empty.
func (r *WorkspaceRegistry) Invalidate(handle WorkspaceHandle) bool {
	if r == nil || handle.registry != r || handle.entryToken == 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.byID[handle.registryID]
	if !ok || entry.entryToken != handle.entryToken {
		return false
	}
	delete(r.byID, handle.registryID)
	return true
}

func (r *WorkspaceRegistry) handle(
	id string,
	entry workspaceRegistryEntry,
) WorkspaceHandle {
	return WorkspaceHandle{
		Workspace:  entry.ws,
		InstanceID: entry.backendInstanceID,
		registry:   r,
		registryID: id,
		entryToken: entry.entryToken,
	}
}
