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

var errWorkspaceManagerNil = errors.New(
	"codeexecutor: WorkspaceManager must not be nil",
)

// WorkspaceRegistry keeps a process-level mapping of logical IDs to
// created workspaces for reuse within a session.
type WorkspaceRegistry struct {
	mu       sync.Mutex
	byID     map[string]workspaceRegistryEntry
	inflight map[string]*workspaceCreateCall
	// releasing tracks in-flight Release cleanups so Acquire waits instead
	// of racing a concurrent Cleanup, and so concurrent Release coalesces.
	releasing map[string]*workspaceReleaseCall
	nextToken uint64
}

type workspaceReleaseCall struct {
	done chan struct{}
	err  error
}

type workspaceRegistryEntry struct {
	ws                Workspace
	backendInstanceID WorkspaceInstanceID
	entryToken        uint64
	// manager is the WorkspaceManager that created this workspace.
	// Release/ReleaseHandler always clean up through this manager,
	// preventing callers from accidentally skipping Cleanup (nil) or
	// running it against the wrong backend (mismatched manager).
	manager WorkspaceManager
}

type workspaceCreateCall struct {
	done  chan struct{}
	entry workspaceRegistryEntry
	err   error
}

// NewWorkspaceRegistry creates a new in-memory registry.
func NewWorkspaceRegistry() *WorkspaceRegistry {
	return &WorkspaceRegistry{
		byID:      map[string]workspaceRegistryEntry{},
		inflight:  map[string]*workspaceCreateCall{},
		releasing: map[string]*workspaceReleaseCall{},
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
	if m == nil {
		return Workspace{}, errWorkspaceManagerNil
	}
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
	if m == nil {
		return WorkspaceHandle{}, errWorkspaceManagerNil
	}
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
		if rel, ok := r.releasing[id]; ok {
			r.mu.Unlock()
			if err := waitWorkspaceRelease(ctx, rel); err != nil {
				// ctx was canceled while cleanup is still in flight.
				// Return instead of spinning until the detached
				// cleanup completes.
				return workspaceRegistryEntry{}, err
			}
			continue
		}
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
		manager:           m,
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

// Get returns a previously acquired workspace without creating one.
// ok is false when id is unknown or a release is in flight.
func (r *WorkspaceRegistry) Get(id string) (Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, releasing := r.releasing[id]; releasing {
		return Workspace{}, false
	}
	entry, ok := r.byID[id]
	return entry.ws, ok
}

// Release removes the current cache entry for id (generation-blind).
// Prefer ReleaseHandle when holding a WorkspaceHandle.
//
// Cleanup always runs through the WorkspaceManager that was passed to
// Acquire/AcquireHandle, stored in the registry entry. This prevents
// callers from accidentally skipping Cleanup (nil manager) or running
// it against the wrong backend (mismatched manager).
//
// Lifecycle symmetry (all wings):
//   - concurrent Acquire waits for in-flight Release
//   - Release waits for in-flight Create before cleaning
//   - Cleanup runs asynchronously so the initiator can cancel waiting
//   - failed Cleanup restores the entry so Release is retryable
//   - concurrent Release coalesces on the same releasing call
func (r *WorkspaceRegistry) Release(
	ctx context.Context, id string,
) error {
	for {
		r.mu.Lock()
		if rel, ok := r.releasing[id]; ok {
			r.mu.Unlock()
			return waitWorkspaceRelease(ctx, rel)
		}
		if call, ok := r.inflight[id]; ok {
			r.mu.Unlock()
			if _, err := waitWorkspaceCreate(ctx, call); err != nil {
				// Creation runs with WithoutCancel, so a canceled
				// wait does not mean creation failed. A live entry
				// may still be installed later; do not report nil.
				select {
				case <-call.done:
					// Creation finished (with error): nothing to release.
					return nil
				default:
					// Creation still in flight; release did not complete.
					return ctx.Err()
				}
			}
			continue
		}
		entry, ok := r.byID[id]
		if !ok {
			r.mu.Unlock()
			return nil
		}
		delete(r.byID, id)
		if r.releasing == nil {
			r.releasing = map[string]*workspaceReleaseCall{}
		}
		rel := &workspaceReleaseCall{done: make(chan struct{})}
		r.releasing[id] = rel
		cleanupCtx := context.WithoutCancel(ctx)
		r.mu.Unlock()

		go func(entry workspaceRegistryEntry, rel *workspaceReleaseCall) {
			var err error
			if entry.manager != nil {
				err = entry.manager.Cleanup(cleanupCtx, entry.ws)
			}
			r.mu.Lock()
			if err != nil {
				if r.byID == nil {
					r.byID = map[string]workspaceRegistryEntry{}
				}
				r.byID[id] = entry
			}
			rel.err = err
			delete(r.releasing, id)
			close(rel.done)
			r.mu.Unlock()
		}(entry, rel)

		return waitWorkspaceRelease(ctx, rel)
	}
}

// ReleaseHandle destroys only the exact generation in handle. If the cache
// already holds a newer token, this is a no-op and does not Cleanup the newer
// workspace.
//
// Cleanup always runs through the WorkspaceManager that was passed to
// Acquire/AcquireHandle, stored in the registry entry.
func (r *WorkspaceRegistry) ReleaseHandle(
	ctx context.Context, handle WorkspaceHandle,
) error {
	if r == nil || handle.registry != r || handle.entryToken == 0 {
		return nil
	}
	id := handle.registryID
	for {
		r.mu.Lock()
		if rel, ok := r.releasing[id]; ok {
			r.mu.Unlock()
			return waitWorkspaceRelease(ctx, rel)
		}
		if call, ok := r.inflight[id]; ok {
			r.mu.Unlock()
			if _, err := waitWorkspaceCreate(ctx, call); err != nil {
				// Creation runs with WithoutCancel, so a canceled
				// wait does not mean creation failed. A live entry
				// may still be installed later; do not report nil.
				select {
				case <-call.done:
					// Creation finished (with error): nothing to release.
					return nil
				default:
					// Creation still in flight; release did not complete.
					return ctx.Err()
				}
			}
			continue
		}
		entry, ok := r.byID[id]
		if !ok || entry.entryToken != handle.entryToken {
			r.mu.Unlock()
			return nil
		}
		delete(r.byID, id)
		if r.releasing == nil {
			r.releasing = map[string]*workspaceReleaseCall{}
		}
		rel := &workspaceReleaseCall{done: make(chan struct{})}
		r.releasing[id] = rel
		cleanupCtx := context.WithoutCancel(ctx)
		r.mu.Unlock()

		go func(entry workspaceRegistryEntry, rel *workspaceReleaseCall) {
			var err error
			if entry.manager != nil {
				err = entry.manager.Cleanup(cleanupCtx, entry.ws)
			}
			r.mu.Lock()
			if err != nil {
				if r.byID == nil {
					r.byID = map[string]workspaceRegistryEntry{}
				}
				if _, exists := r.byID[id]; !exists {
					r.byID[id] = entry
				}
			}
			rel.err = err
			delete(r.releasing, id)
			close(rel.done)
			r.mu.Unlock()
		}(entry, rel)

		return waitWorkspaceRelease(ctx, rel)
	}
}

func waitWorkspaceRelease(ctx context.Context, rel *workspaceReleaseCall) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-rel.done:
		return rel.err
	}
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
