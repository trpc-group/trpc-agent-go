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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeWM struct {
	ws    Workspace
	err   error
	calls int
}

func (f *fakeWM) CreateWorkspace(
	_ context.Context, id string, _ WorkspacePolicy,
) (Workspace, error) {
	f.calls++
	if f.err != nil {
		return Workspace{}, f.err
	}
	f.ws.ID = id
	if f.ws.Path == "" {
		f.ws.Path = "/tmp/" + id
	}
	return f.ws, nil
}

func (f *fakeWM) Cleanup(_ context.Context, _ Workspace) error {
	return nil
}

func TestWorkspaceRegistry_Acquire_Reuses(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &fakeWM{ws: Workspace{Path: "/tmp/w"}}
	ctx := context.Background()

	ws1, err := r.Acquire(ctx, wm, "abc")
	require.NoError(t, err)
	ws2, err := r.Acquire(ctx, wm, "abc")
	require.NoError(t, err)

	require.Equal(t, ws1, ws2)
	// CreateWorkspace should be called once for the id.
	require.Equal(t, 1, wm.calls)
}

func TestWorkspaceRegistry_Acquire_Error(t *testing.T) {
	r := NewWorkspaceRegistry()
	boom := errors.New("boom")
	wm := &fakeWM{err: boom}
	_, err := r.Acquire(context.Background(), wm, "x")
	require.Error(t, err)
}

type blockingWM struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingWM) CreateWorkspace(
	ctx context.Context, id string, _ WorkspacePolicy,
) (Workspace, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
		return Workspace{ID: id, Path: "/tmp/" + id}, nil
	case <-ctx.Done():
		return Workspace{}, ctx.Err()
	}
}

func (b *blockingWM) Cleanup(_ context.Context, _ Workspace) error {
	return nil
}

func TestWorkspaceRegistry_Acquire_CreateIgnoresLeaderCancel(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &blockingWM{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}

	leaderCtx, leaderCancel := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := r.Acquire(leaderCtx, wm, "shared")
		leaderDone <- err
	}()

	select {
	case <-wm.entered:
	case <-time.After(time.Second):
		t.Fatal("leader did not start workspace creation")
	}

	followerDone := make(chan error, 1)
	go func() {
		ws, err := r.Acquire(context.Background(), wm, "shared")
		if err == nil {
			require.Equal(t, "shared", ws.ID)
		}
		followerDone <- err
	}()

	leaderCancel()
	close(wm.release)

	require.ErrorIs(t, <-leaderDone, context.Canceled)
	require.NoError(t, <-followerDone)
}
