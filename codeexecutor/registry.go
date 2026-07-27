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
	// releasing tracks in-flight Release cleanups so Acquire waits
	// instead of racing a concurrent Cleanup on the same path.
	releasing map[string]*workspaceReleaseCall
}

type workspaceCreateCall struct {
	done chan struct{}
	ws   Workspace
	err  error
}

type workspaceReleaseCall struct {
	done chan struct{}
	err  error
}

// NewWorkspaceRegistry creates a new in-memory registry.
func NewWorkspaceRegistry() *WorkspaceRegistry {
	return &WorkspaceRegistry{
		byID:      map[string]Workspace{},
		inflight:  map[string]*workspaceCreateCall{},
		releasing: map[string]*workspaceReleaseCall{},
	}
}

// Acquire creates or returns an existing workspace with the given id.
// Concurrent first-time acquires for the same id coalesce to a single
// CreateWorkspace so init hooks and workspace creation run at most once per id.
// If a Release is in flight for id, Acquire waits for it to finish so it
// cannot observe a workspace path mid-cleanup.
func (r *WorkspaceRegistry) Acquire(
	ctx context.Context, m WorkspaceManager, id string,
) (Workspace, error) {
	for {
		r.mu.Lock()
		if ws, ok := r.byID[id]; ok {
			r.mu.Unlock()
			return ws, nil
		}
		if err := ctx.Err(); err != nil {
			r.mu.Unlock()
			return Workspace{}, err
		}
		if rel, ok := r.releasing[id]; ok {
			r.mu.Unlock()
			if err := waitWorkspaceRelease(ctx, rel); err != nil {
				// Cleanup failed but release finished; fall through to recreate.
				_ = err
			}
			continue
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

// Get returns a previously acquired workspace without creating one.
// ok is false when id is unknown.
func (r *WorkspaceRegistry) Get(id string) (Workspace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ws, ok := r.byID[id]
	return ws, ok
}

// Release removes id from the registry and optionally cleans up the
// underlying workspace when m is non-nil.
//
// Release is serialized with Acquire for the same id: a concurrent
// Acquire waits until cleanup finishes. If cleanup fails, the entry is
// restored so a later Release can retry. If the id is unknown and no
// release is in flight, Release is a no-op and returns nil.
func (r *WorkspaceRegistry) Release(
	ctx context.Context, m WorkspaceManager, id string,
) error {
	r.mu.Lock()
	if rel, ok := r.releasing[id]; ok {
		r.mu.Unlock()
		return waitWorkspaceRelease(ctx, rel)
	}
	ws, ok := r.byID[id]
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

	var err error
	if m != nil {
		err = m.Cleanup(cleanupCtx, ws)
	}

	r.mu.Lock()
	if err != nil {
		// Keep the entry so Release remains retryable.
		if r.byID == nil {
			r.byID = map[string]Workspace{}
		}
		r.byID[id] = ws
	}
	rel.err = err
	delete(r.releasing, id)
	close(rel.done)
	r.mu.Unlock()
	return err
}

func waitWorkspaceRelease(ctx context.Context, rel *workspaceReleaseCall) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-rel.done:
		return rel.err
	}
}
