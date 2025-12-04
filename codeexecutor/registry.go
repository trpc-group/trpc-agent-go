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
	mu   sync.Mutex
	byID map[string]Workspace
}

// NewWorkspaceRegistry creates a new in-memory registry.
func NewWorkspaceRegistry() *WorkspaceRegistry {
	return &WorkspaceRegistry{byID: map[string]Workspace{}}
}

// Acquire creates or returns an existing workspace with the given id.
func (r *WorkspaceRegistry) Acquire(
	ctx context.Context, m WorkspaceManager, id string,
) (Workspace, error) {
	r.mu.Lock()
	if ws, ok := r.byID[id]; ok {
		r.mu.Unlock()
		return ws, nil
	}
	r.mu.Unlock()
	ws, err := m.CreateWorkspace(ctx, id, WorkspacePolicy{})
	if err != nil {
		return Workspace{}, err
	}
	r.mu.Lock()
	r.byID[id] = ws
	r.mu.Unlock()
	return ws, nil
}
