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
	"fmt"
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
	// Keep the workspace deterministic across instances. The registry token,
	// not Workspace equality, prevents late invalidation of a refreshed entry.
	return Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (*rotatingWM) Cleanup(context.Context, Workspace) error {
	return nil
}

func (m *rotatingWM) InstanceID(
	context.Context,
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

func TestWorkspaceRegistry_Invalidate_IsConditional(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &rotatingWM{instanceID: "instance-1"}
	ctx := context.Background()

	handle, err := r.AcquireHandle(ctx, wm, "invalidate")
	require.NoError(t, err)
	require.False(t, r.Invalidate(WorkspaceHandle{}))
	require.False(t, NewWorkspaceRegistry().Invalidate(handle))
	require.True(t, r.Invalidate(handle))

	_, err = r.Acquire(ctx, wm, "invalidate")
	require.NoError(t, err)
	require.Equal(t, 2, wm.callCount())

	wm.setInstanceID("instance-2")
	refreshed, err := r.AcquireHandle(ctx, wm, "invalidate")
	require.NoError(t, err)
	require.Equal(t, 3, wm.callCount())
	require.False(t, r.Invalidate(handle),
		"a stale instance must not evict the refreshed deterministic handle")
	require.True(t, r.Invalidate(refreshed))
}

type instanceProbeResult struct {
	id  WorkspaceInstanceID
	err error
}

type fencedWM struct {
	mu       sync.Mutex
	probes   []instanceProbeResult
	creates  int
	cleanups int
}

func (m *fencedWM) CreateWorkspace(
	_ context.Context,
	id string,
	_ WorkspacePolicy,
) (Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates++
	return Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (m *fencedWM) Cleanup(context.Context, Workspace) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanups++
	return nil
}

func (m *fencedWM) InstanceID(
	context.Context,
) (WorkspaceInstanceID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.probes) == 0 {
		return "", errors.New("unexpected instance probe")
	}
	result := m.probes[0]
	m.probes = m.probes[1:]
	return result.id, result.err
}

func (m *fencedWM) addProbes(results ...instanceProbeResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probes = append(m.probes, results...)
}

func TestWorkspaceRegistry_AcquireHandle_GenerationFence(t *testing.T) {
	t.Run("stable generation installs handle", func(t *testing.T) {
		r := NewWorkspaceRegistry()
		wm := &fencedWM{probes: []instanceProbeResult{
			{id: "instance-a"},
			{id: "instance-a"},
		}}

		handle, err := r.AcquireHandle(context.Background(), wm, "stable")
		require.NoError(t, err)
		require.Equal(t, WorkspaceInstanceID("instance-a"),
			handle.InstanceID)
		require.NotZero(t, handle.entryToken)
		require.Equal(t, 1, wm.creates)
	})

	t.Run("rotation during creation is not cached or cleaned", func(t *testing.T) {
		r := NewWorkspaceRegistry()
		wm := &fencedWM{probes: []instanceProbeResult{
			{id: "instance-a"},
			{id: "instance-b"},
		}}

		_, err := r.AcquireHandle(context.Background(), wm, "rotated")
		require.ErrorIs(t, err, ErrWorkspaceStale)
		require.NotContains(t, r.byID, "rotated")
		require.Equal(t, 1, wm.creates)
		require.Zero(t, wm.cleanups)

		wm.addProbes(
			instanceProbeResult{id: "instance-b"},
			instanceProbeResult{id: "instance-b"},
		)
		handle, err := r.AcquireHandle(
			context.Background(), wm, "rotated",
		)
		require.NoError(t, err)
		require.Equal(t, WorkspaceInstanceID("instance-b"),
			handle.InstanceID)
		require.Equal(t, 2, wm.creates)
	})
}

func TestWorkspaceRegistry_AcquireHandle_PostflightErrorKeepsOldEntry(
	t *testing.T,
) {
	r := NewWorkspaceRegistry()
	wm := &fencedWM{probes: []instanceProbeResult{
		{id: "instance-a"},
		{id: "instance-a"},
	}}
	old, err := r.AcquireHandle(context.Background(), wm, "postflight")
	require.NoError(t, err)

	postErr := errors.New("postflight failed")
	wm.addProbes(
		instanceProbeResult{id: "instance-b"}, // cache validation
		instanceProbeResult{id: "instance-b"}, // creation preflight
		instanceProbeResult{err: postErr},     // creation postflight
	)
	_, err = r.AcquireHandle(context.Background(), wm, "postflight")
	require.ErrorIs(t, err, postErr)
	require.Equal(t, old.entryToken, r.byID["postflight"].entryToken)
	require.Equal(t, 2, wm.creates)
}

func TestWorkspaceRegistry_Invalidate_LegacyLateStaleCannotEvictNewToken(
	t *testing.T,
) {
	r := NewWorkspaceRegistry()
	wm := &fakeWM{}
	ctx := context.Background()

	old, err := r.AcquireHandle(ctx, wm, "legacy")
	require.NoError(t, err)
	require.True(t, r.Invalidate(old))
	current, err := r.AcquireHandle(ctx, wm, "legacy")
	require.NoError(t, err)
	require.Equal(t, old.Workspace, current.Workspace)
	require.NotEqual(t, old.entryToken, current.entryToken)

	require.False(t, r.Invalidate(old))
	require.False(t, r.Invalidate(old))
	got, err := r.AcquireHandle(ctx, wm, "legacy")
	require.NoError(t, err)
	require.Equal(t, current.entryToken, got.entryToken)
	require.Equal(t, 2, wm.callCount())
}

type countingCleanupWM struct {
	mu      sync.Mutex
	creates int
	cleans  int
	paths   []string
	block   chan struct{}
	entered chan struct{}
	fail    error
}

func (c *countingCleanupWM) CreateWorkspace(_ context.Context, id string, _ WorkspacePolicy) (Workspace, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creates++
	path := "/tmp/" + id + "-" + string(rune('a'+c.creates-1))
	// keep path unique per create without non-ascii
	path = fmt.Sprintf("/tmp/%s-%d", id, c.creates)
	return Workspace{ID: id, Path: path}, nil
}

func (c *countingCleanupWM) Cleanup(_ context.Context, ws Workspace) error {
	c.mu.Lock()
	c.cleans++
	c.paths = append(c.paths, ws.Path)
	fail := c.fail
	entered := c.entered
	block := c.block
	c.mu.Unlock()
	if entered != nil {
		select {
		case <-entered:
		default:
			close(entered)
		}
	}
	if block != nil {
		<-block
	}
	return fail
}

func TestWorkspaceRegistry_ReleaseHandle_IgnoresNewerGeneration(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &countingCleanupWM{}
	ctx := context.Background()
	h1, err := r.AcquireHandle(ctx, wm, "gen")
	require.NoError(t, err)
	require.True(t, r.Invalidate(h1))
	h2, err := r.AcquireHandle(ctx, wm, "gen")
	require.NoError(t, err)
	require.NotEqual(t, h1.Workspace.Path, h2.Workspace.Path)

	// Releasing the stale handle must not cleanup the newer workspace.
	require.NoError(t, r.ReleaseHandle(ctx, h1))
	wm.mu.Lock()
	require.Equal(t, 0, wm.cleans)
	wm.mu.Unlock()
	got, ok := r.Get("gen")
	require.True(t, ok)
	require.Equal(t, h2.Workspace.Path, got.Path)

	require.NoError(t, r.ReleaseHandle(ctx, h2))
	wm.mu.Lock()
	require.Equal(t, 1, wm.cleans)
	require.Equal(t, []string{h2.Workspace.Path}, wm.paths)
	wm.mu.Unlock()
	_, ok = r.Get("gen")
	require.False(t, ok)
}

func TestWorkspaceRegistry_Release_WaitsInflightCreateAndCleans(t *testing.T) {
	r := NewWorkspaceRegistry()
	entered := make(chan struct{})
	releaseCreate := make(chan struct{})
	wm := &blockingCreateThenCleanWM{entered: entered, releaseCreate: releaseCreate}
	ctx := context.Background()
	acqErr := make(chan error, 1)
	go func() {
		_, err := r.Acquire(ctx, wm, "inflight-rel")
		acqErr <- err
	}()
	<-entered
	relErr := make(chan error, 1)
	go func() { relErr <- r.Release(ctx, "inflight-rel") }()
	select {
	case e := <-relErr:
		t.Fatalf("Release returned before create finished: %v", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseCreate)
	require.NoError(t, <-acqErr)
	require.NoError(t, <-relErr)
	_, ok := r.Get("inflight-rel")
	require.False(t, ok)
	require.Equal(t, 1, wm.cleans)
}

type blockingCreateThenCleanWM struct {
	entered       chan struct{}
	releaseCreate chan struct{}
	mu            sync.Mutex
	cleans        int
	once          sync.Once
}

func (b *blockingCreateThenCleanWM) CreateWorkspace(ctx context.Context, id string, _ WorkspacePolicy) (Workspace, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.releaseCreate:
		return Workspace{ID: id, Path: "/tmp/" + id}, nil
	case <-ctx.Done():
		return Workspace{}, ctx.Err()
	}
}

func (b *blockingCreateThenCleanWM) Cleanup(_ context.Context, _ Workspace) error {
	b.mu.Lock()
	b.cleans++
	b.mu.Unlock()
	return nil
}

func TestWorkspaceRegistry_Release_RetryAfterCleanupFailure(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &countingCleanupWM{fail: errors.New("boom")}
	ctx := context.Background()
	_, err := r.Acquire(ctx, wm, "retry")
	require.NoError(t, err)
	err = r.Release(ctx, "retry")
	require.Error(t, err)
	_, ok := r.Get("retry")
	require.True(t, ok, "entry restored after failed cleanup")
	wm.fail = nil
	require.NoError(t, r.Release(ctx, "retry"))
	_, ok = r.Get("retry")
	require.False(t, ok)
}

func TestWorkspaceRegistry_Release_CallerCancelDoesNotAbortCleanup(t *testing.T) {
	r := NewWorkspaceRegistry()
	entered := make(chan struct{})
	block := make(chan struct{})
	wm := &countingCleanupWM{entered: entered, block: block}
	ctx := context.Background()
	_, err := r.Acquire(ctx, wm, "cancel-rel")
	require.NoError(t, err)
	callCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() { errCh <- r.Release(callCtx, "cancel-rel") }()
	<-entered
	cancel()
	require.ErrorIs(t, <-errCh, context.Canceled)
	close(block)
	// Coalesced waiters / later Release should finish cleanup.
	require.NoError(t, r.Release(ctx, "cancel-rel"))
	_, ok := r.Get("cancel-rel")
	require.False(t, ok)
}

func TestWorkspaceRegistry_ReleaseHandle_NilAndInvalidHandle(t *testing.T) {
	ctx := context.Background()
	wm := &fakeWM{}

	// nil receiver is a safe no-op.
	var nilReg *WorkspaceRegistry
	require.NoError(t, nilReg.ReleaseHandle(ctx, WorkspaceHandle{}))

	r := NewWorkspaceRegistry()

	// Handle owned by a different registry is a no-op.
	other := NewWorkspaceRegistry()
	h, err := other.AcquireHandle(ctx, wm, "foreign")
	require.NoError(t, err)
	require.NoError(t, r.ReleaseHandle(ctx, h))

	// Zero entryToken is a no-op even when registry matches.
	require.NoError(t, r.ReleaseHandle(ctx, WorkspaceHandle{
		registry:   r,
		registryID: "zero-token",
	}))
}

func TestWorkspaceRegistry_ReleaseHandle_RestoresEntryOnCleanupFailure(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &countingCleanupWM{fail: errors.New("cleanup boom")}
	ctx := context.Background()

	handle, err := r.AcquireHandle(ctx, wm, "fail-clean")
	require.NoError(t, err)

	require.Error(t, r.ReleaseHandle(ctx, handle))
	// Entry restored after failed cleanup so Release is retryable.
	got, ok := r.Get("fail-clean")
	require.True(t, ok)
	require.Equal(t, handle.Workspace.Path, got.Path)

	// Retry succeeds once cleanup stops failing.
	wm.fail = nil
	require.NoError(t, r.ReleaseHandle(ctx, handle))
	_, ok = r.Get("fail-clean")
	require.False(t, ok)
}

func TestWorkspaceRegistry_ReleaseHandle_CoalescesInflightRelease(t *testing.T) {
	r := NewWorkspaceRegistry()
	entered := make(chan struct{})
	block := make(chan struct{})
	wm := &countingCleanupWM{entered: entered, block: block}
	ctx := context.Background()

	handle, err := r.AcquireHandle(ctx, wm, "coalesce")
	require.NoError(t, err)

	// Start a ReleaseHandle that blocks inside Cleanup. By the time
	// entered is closed, releasing[id] is set and the first caller is
	// blocked in waitWorkspaceRelease.
	firstErr := make(chan error, 1)
	go func() { firstErr <- r.ReleaseHandle(ctx, handle) }()
	<-entered

	// Unblock the cleanup shortly so the synchronous second call below
	// has a window to find releasing[id] and coalesce.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(block)
	}()

	// If the second call coalesces, it blocks in waitWorkspaceRelease until
	// close(block) unblocks the cleanup goroutine. If it does NOT coalesce,
	// it returns nil immediately (the entry was already deleted) — the test
	// timeout would then fire because the first caller is still blocked.
	require.NoError(t, r.ReleaseHandle(ctx, handle))
	require.NoError(t, <-firstErr)

	wm.mu.Lock()
	require.Equal(t, 1, wm.cleans, "concurrent ReleaseHandle must coalesce")
	wm.mu.Unlock()
}

// secondCallBlocksWM lets the first CreateWorkspace succeed immediately and
// blocks subsequent calls until release is closed. This lets a test hold a
// second create in-flight while exercising ReleaseHandle.
type secondCallBlocksWM struct {
	mu            sync.Mutex
	creates       int
	secondEntered chan struct{}
	releaseSecond chan struct{}
	once          sync.Once
}

func (m *secondCallBlocksWM) CreateWorkspace(
	ctx context.Context, id string, _ WorkspacePolicy,
) (Workspace, error) {
	m.mu.Lock()
	m.creates++
	call := m.creates
	m.mu.Unlock()

	if call == 1 {
		return Workspace{ID: id, Path: "/tmp/" + id}, nil
	}
	m.once.Do(func() { close(m.secondEntered) })
	select {
	case <-m.releaseSecond:
		return Workspace{ID: id, Path: "/tmp/" + id + "-v2"}, nil
	case <-ctx.Done():
		return Workspace{}, ctx.Err()
	}
}

func (*secondCallBlocksWM) Cleanup(context.Context, Workspace) error {
	return nil
}

func TestWorkspaceRegistry_ReleaseHandle_WaitsInflightCreate(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &secondCallBlocksWM{
		secondEntered: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
	ctx := context.Background()

	// First acquire succeeds immediately.
	h1, err := r.AcquireHandle(ctx, wm, "inflight-create")
	require.NoError(t, err)
	require.True(t, r.Invalidate(h1))

	// Start a second acquire that blocks in CreateWorkspace. By the time
	// secondEntered is closed, inflight[id] is set.
	acqDone := make(chan error, 1)
	go func() {
		_, err := r.AcquireHandle(ctx, wm, "inflight-create")
		acqDone <- err
	}()
	<-wm.secondEntered

	// ReleaseHandle with the stale handle waits for the in-flight create,
	// then finds a newer token and returns without cleaning up.
	relDone := make(chan error, 1)
	go func() { relDone <- r.ReleaseHandle(ctx, h1) }()

	// Give ReleaseHandle time to lock, find inflight[id], and enter
	// waitWorkspaceCreate before the create completes and deletes it.
	time.Sleep(50 * time.Millisecond)

	close(wm.releaseSecond)
	require.NoError(t, <-acqDone)
	require.NoError(t, <-relDone)

	// The newer workspace survives because ReleaseHandle is token-scoped.
	got, ok := r.Get("inflight-create")
	require.True(t, ok)
	require.NotEqual(t, h1.Workspace.Path, got.Path)
}

// Regression: Acquire must return ctx.Err() promptly when a Release cleanup
// is in flight and ctx is canceled. Previously the error was discarded and
// the loop spun until the detached cleanup finished.
func TestWorkspaceRegistry_Acquire_ReturnsCanceledWhenReleaseInFlight(t *testing.T) {
	r := NewWorkspaceRegistry()
	entered := make(chan struct{})
	block := make(chan struct{})
	wm := &countingCleanupWM{entered: entered, block: block}
	ctx := context.Background()

	_, err := r.Acquire(ctx, wm, "spin-guard")
	require.NoError(t, err)

	// Start a Release that blocks inside Cleanup.
	go func() { _ = r.Release(ctx, "spin-guard") }()
	<-entered // releasing[id] is now installed

	// A concurrent Acquire with a canceled context must return promptly.
	acqCtx, acqCancel := context.WithCancel(context.Background())
	acqDone := make(chan error, 1)
	go func() {
		_, err := r.Acquire(acqCtx, wm, "spin-guard")
		acqDone <- err
	}()

	// Give the goroutine time to enter waitWorkspaceRelease.
	time.Sleep(50 * time.Millisecond)
	acqCancel()

	select {
	case err := <-acqDone:
		require.ErrorIs(t, err, context.Canceled,
			"Acquire must return ctx.Err() instead of spinning")
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire spun instead of returning on canceled context")
	}

	close(block)
}

// Regression: Release must return ctx.Err() (not nil) when an in-flight
// CreateWorkspace is still running and the caller's context is canceled.
// Previously the error was silently converted to nil (false success).
func TestWorkspaceRegistry_Release_ReturnsCanceledWhenCreateInFlight(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &secondCallBlocksWM{
		secondEntered: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
	ctx := context.Background()

	// First acquire succeeds immediately; invalidate to force a second create.
	h1, err := r.AcquireHandle(ctx, wm, "release-cancel")
	require.NoError(t, err)
	require.True(t, r.Invalidate(h1))

	// Start a second acquire that blocks in CreateWorkspace.
	acqDone := make(chan error, 1)
	go func() {
		_, err := r.AcquireHandle(ctx, wm, "release-cancel")
		acqDone <- err
	}()
	<-wm.secondEntered // inflight[id] is now set

	// Release with a short-timeout context must not report success.
	relCtx, relCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer relCancel()
	err = r.Release(relCtx, "release-cancel")
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"Release must return ctx.Err() when creation is still in flight")

	// Let creation finish and verify the entry was installed.
	close(wm.releaseSecond)
	require.NoError(t, <-acqDone)
	got, ok := r.Get("release-cancel")
	require.True(t, ok, "entry must still be installed after create finishes")
	require.NotEqual(t, h1.Workspace.Path, got.Path)

	// A retry Release should now succeed (entry exists, no in-flight create).
	require.NoError(t, r.Release(ctx, "release-cancel"))
}

// Regression: ReleaseHandle must return ctx.Err() (not nil) when an
// in-flight CreateWorkspace is still running and the caller's context
// is canceled.
func TestWorkspaceRegistry_ReleaseHandle_ReturnsCanceledWhenCreateInFlight(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &secondCallBlocksWM{
		secondEntered: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
	ctx := context.Background()

	h1, err := r.AcquireHandle(ctx, wm, "handle-cancel")
	require.NoError(t, err)
	require.True(t, r.Invalidate(h1))

	// Start a second acquire that blocks in CreateWorkspace.
	acqDone := make(chan error, 1)
	go func() {
		_, err := r.AcquireHandle(ctx, wm, "handle-cancel")
		acqDone <- err
	}()
	<-wm.secondEntered

	// ReleaseHandle with a short-timeout context must not report success.
	relCtx, relCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer relCancel()
	err = r.ReleaseHandle(relCtx, h1)
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"ReleaseHandle must return ctx.Err() when creation is still in flight")

	close(wm.releaseSecond)
	require.NoError(t, <-acqDone)
}

// TestWorkspaceRegistry_Acquire_RejectsNilManager verifies that Acquire and
// AcquireHandle reject a nil WorkspaceManager. Without this, createWorkspace
// would panic when calling m.CreateWorkspace, and Release would silently skip
// Cleanup (reporting success while leaking the workspace).
func TestWorkspaceRegistry_Acquire_RejectsNilManager(t *testing.T) {
	r := NewWorkspaceRegistry()
	ctx := context.Background()

	_, err := r.Acquire(ctx, nil, "nil-mgr")
	require.ErrorIs(t, err, errWorkspaceManagerNil)

	_, err = r.AcquireHandle(ctx, nil, "nil-mgr")
	require.ErrorIs(t, err, errWorkspaceManagerNil)
}

// TestWorkspaceRegistry_Release_UsesStoredManager verifies that Release
// cleans up through the manager stored at Acquire time, not through a
// caller-supplied parameter. This prevents nil from skipping Cleanup and
// a mismatched manager from running it against the wrong backend.
func TestWorkspaceRegistry_Release_UsesStoredManager(t *testing.T) {
	r := NewWorkspaceRegistry()
	wm := &countingCleanupWM{}
	ctx := context.Background()

	_, err := r.Acquire(ctx, wm, "stored-mgr")
	require.NoError(t, err)

	// Release no longer takes a manager parameter; it always uses the
	// one stored during Acquire.
	require.NoError(t, r.Release(ctx, "stored-mgr"))
	wm.mu.Lock()
	require.Equal(t, 1, wm.cleans, "Release must clean up through the stored manager")
	wm.mu.Unlock()
}
