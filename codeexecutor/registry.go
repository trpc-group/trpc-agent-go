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
	"fmt"
	"sync"

	"golang.org/x/sync/singleflight"
)

// WorkspaceRegistry keeps a process-level mapping of logical IDs to
// created workspaces for reuse within a session.
type WorkspaceRegistry struct {
	mu   sync.Mutex
	byID map[string]Workspace
	sf   singleflight.Group
}

// NewWorkspaceRegistry creates a new in-memory registry.
func NewWorkspaceRegistry() *WorkspaceRegistry {
	return &WorkspaceRegistry{byID: map[string]Workspace{}}
}

// Acquire creates or returns an existing workspace with the given id.
// Concurrent first-time acquires for the same id coalesce to a single
// CreateWorkspace via singleflight so init hooks and workspace creation run
// at most once per id.
func (r *WorkspaceRegistry) Acquire(
	ctx context.Context, m WorkspaceManager, id string,
) (Workspace, error) {
	createCtx := context.WithoutCancel(ctx)
	ch := r.sf.DoChan(id, func() (interface{}, error) {
		r.mu.Lock()
		if ws, ok := r.byID[id]; ok {
			r.mu.Unlock()
			return ws, nil
		}
		r.mu.Unlock()

		ws, err := m.CreateWorkspace(createCtx, id, WorkspacePolicy{})
		if err != nil {
			return nil, err
		}

		r.mu.Lock()
		r.byID[id] = ws
		r.mu.Unlock()
		return ws, nil
	})

	select {
	case <-ctx.Done():
		return Workspace{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return Workspace{}, res.Err
		}
		ws, ok := res.Val.(Workspace)
		if !ok {
			return Workspace{}, fmt.Errorf(
				"workspace registry: unexpected Acquire result type %T",
				res.Val,
			)
		}
		return ws, nil
	}
}
