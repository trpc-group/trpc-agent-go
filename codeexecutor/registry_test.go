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
	mu    sync.Mutex
	calls int
}

func (f *fakeWM) CreateWorkspace(
	_ context.Context, id string, _ WorkspacePolicy,
) (Workspace, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
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

func (f *fakeWM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
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
	require.Equal(t, 1, wm.callCount())
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
	mu      sync.Mutex
	calls   int
}

func (b *blockingWM) CreateWorkspace(
	ctx context.Context, id string, _ WorkspacePolicy,
) (Workspace, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
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

func (b *blockingWM) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
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
	require.Equal(t, 1, wm.callCount())
}

func TestWorkspaceRegistry_Acquire_CanceledMissDoesNotCreate(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &fakeWM{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Acquire(ctx, wm, "canceled")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, wm.callCount())
}

type rotatingWM struct {
	mu sync.Mutex

	instanceID  WorkspaceInstanceID
	createErr   error
	providerErr error
	calls       int

	rebuildEntered chan struct{}
	rebuildRelease chan struct{}
	rebuildOnce    sync.Once
}

func (m *rotatingWM) CreateWorkspace(
	ctx context.Context,
	id string,
	_ WorkspacePolicy,
) (Workspace, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	err := m.createErr
	entered := m.rebuildEntered
	release := m.rebuildRelease
	m.mu.Unlock()

	if call > 1 && entered != nil {
		m.rebuildOnce.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
			return Workspace{}, ctx.Err()
		}
	}
	if err != nil {
		return Workspace{}, err
	}
	// Keep the handle deterministic across instances. The instance ID is what
	// prevents a late stale report from evicting a refreshed entry.
	return Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (*rotatingWM) Cleanup(context.Context, Workspace) error {
	return nil
}

func (m *rotatingWM) WorkspaceInstanceID(
	context.Context,
	Workspace,
) (WorkspaceInstanceID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instanceID, m.providerErr
}

func (m *rotatingWM) setInstanceID(id WorkspaceInstanceID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instanceID = id
}

func (m *rotatingWM) setCreateError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createErr = err
}

func (m *rotatingWM) setProviderError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerErr = err
}

func (m *rotatingWM) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestWorkspaceRegistry_Acquire_InstanceAwareStable(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &rotatingWM{instanceID: "instance-1"}

	ws1, err := r.Acquire(context.Background(), wm, "stable")
	require.NoError(t, err)
	ws2, err := r.Acquire(context.Background(), wm, "stable")
	require.NoError(t, err)

	require.Equal(t, ws1, ws2)
	require.Equal(t, 1, wm.callCount())
}

func TestWorkspaceRegistry_Acquire_InstanceChangeRecreates(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &rotatingWM{instanceID: "instance-1"}

	ws1, err := r.Acquire(context.Background(), wm, "rotating")
	require.NoError(t, err)
	wm.setInstanceID("instance-2")
	ws2, err := r.Acquire(context.Background(), wm, "rotating")
	require.NoError(t, err)

	require.Equal(t, ws1, ws2, "deterministic handles may be reused across instances")
	require.Equal(t, 2, wm.callCount())
	_, err = r.Acquire(context.Background(), wm, "rotating")
	require.NoError(t, err)
	require.Equal(t, 2, wm.callCount())
}

func TestWorkspaceRegistry_Acquire_RejectsEmptyInstanceID(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &rotatingWM{}

	_, err := r.Acquire(context.Background(), wm, "empty-instance")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty instance ID")
}

func TestWorkspaceRegistry_Acquire_ValidationFailureKeepsEntry(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &rotatingWM{instanceID: "instance-1"}
	ctx := context.Background()

	want, err := r.Acquire(ctx, wm, "validation")
	require.NoError(t, err)
	probeErr := errors.New("probe failed")
	wm.setProviderError(probeErr)
	_, err = r.Acquire(ctx, wm, "validation")
	require.ErrorIs(t, err, probeErr)
	require.Equal(t, 1, wm.callCount())

	wm.setProviderError(nil)
	got, err := r.Acquire(ctx, wm, "validation")
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, 1, wm.callCount())
}

func TestWorkspaceRegistry_Acquire_RebuildFailureKeepsEntry(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &rotatingWM{instanceID: "instance-1"}
	ctx := context.Background()

	_, err := r.Acquire(ctx, wm, "rebuild")
	require.NoError(t, err)
	wm.setInstanceID("instance-2")
	rebuildErr := errors.New("rebuild failed")
	wm.setCreateError(rebuildErr)
	_, err = r.Acquire(ctx, wm, "rebuild")
	require.ErrorIs(t, err, rebuildErr)
	require.Equal(t, 2, wm.callCount())

	wm.setCreateError(nil)
	_, err = r.Acquire(ctx, wm, "rebuild")
	require.NoError(t, err)
	require.Equal(t, 3, wm.callCount())
}

func TestWorkspaceRegistry_Acquire_ConcurrentRefreshCreatesOnce(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &rotatingWM{instanceID: "instance-1"}
	_, err := r.Acquire(context.Background(), wm, "concurrent-refresh")
	require.NoError(t, err)

	wm.setInstanceID("instance-2")
	wm.rebuildEntered = make(chan struct{})
	wm.rebuildRelease = make(chan struct{})

	const n = 32
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := r.Acquire(
				context.Background(),
				wm,
				"concurrent-refresh",
			)
			errs <- err
		}()
	}
	close(start)
	select {
	case <-wm.rebuildEntered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	close(wm.rebuildRelease)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 2, wm.callCount())
}

func TestWorkspaceRegistry_InvalidateIf_IsConditional(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &rotatingWM{instanceID: "instance-1"}
	ctx := context.Background()

	ws, err := r.Acquire(ctx, wm, "invalidate")
	require.NoError(t, err)
	require.False(t, r.InvalidateIf(
		"invalidate",
		Workspace{ID: ws.ID, Path: "/other"},
		"instance-1",
	))
	require.False(t, r.InvalidateIf("invalidate", ws, "instance-2"))
	require.True(t, r.InvalidateIf("invalidate", ws, "instance-1"))

	_, err = r.Acquire(ctx, wm, "invalidate")
	require.NoError(t, err)
	require.Equal(t, 2, wm.callCount())

	wm.setInstanceID("instance-2")
	_, err = r.Acquire(ctx, wm, "invalidate")
	require.NoError(t, err)
	require.Equal(t, 3, wm.callCount())
	require.False(t, r.InvalidateIf("invalidate", ws, "instance-1"),
		"a stale instance must not evict the refreshed deterministic handle")
}
