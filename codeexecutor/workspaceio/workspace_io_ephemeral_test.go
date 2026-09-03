//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspaceio

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ephemeralProbeManager records workspace creations and cleanups so
// tests can assert the ephemeral release lifecycle end to end.
type ephemeralProbeManager struct {
	mu      sync.Mutex
	creates []string
	cleans  []string
}

func (m *ephemeralProbeManager) CreateWorkspace(
	_ context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates = append(m.creates, id)
	return codeexecutor.Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (m *ephemeralProbeManager) Cleanup(
	_ context.Context,
	ws codeexecutor.Workspace,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleans = append(m.cleans, ws.ID)
	return nil
}

func (m *ephemeralProbeManager) counts() (creates, cleans int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.creates), len(m.cleans)
}

func (m *ephemeralProbeManager) lastCreated() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.creates) == 0 {
		return ""
	}
	return m.creates[len(m.creates)-1]
}

func (m *ephemeralProbeManager) lastCleaned() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cleans) == 0 {
		return ""
	}
	return m.cleans[len(m.cleans)-1]
}

// TestWorkspace_EphemeralWorkspaceReusedWithinInvocation is the
// production-path regression for the empty-session workspace lifecycle:
// every Workspace facade method binds a handle via
// CreateWorkspaceHandle, and an invocation with an invalid (empty-ID)
// session gets an ephemeral workspace that no session lifecycle owns.
// Multiple calls within the same invocation must reuse that one workspace
// — so files written by one call stay visible to the next — and it is
// released exactly once when the invocation finishes, not after every
// call. Valid sessions keep their workspace cached and reused as before.
func TestWorkspace_EphemeralWorkspaceReusedWithinInvocation(t *testing.T) {
	manager := &ephemeralProbeManager{}
	backend := &staleOperationBackend{}
	eng := codeexecutor.NewEngine(manager, backend, backend)
	ws := New(&stubFSExec{eng: eng}, nil)
	require.NotNil(t, ws)

	// Empty session ID -> ephemeral invocation workspace. A stable
	// InvocationID lets the facade cache the handle for the invocation.
	eInv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationID("ephemeral-reuse"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eCtx := agent.NewInvocationContext(ctx, eInv)

	// Two calls in one invocation must reuse a single workspace.
	require.NoError(t, ws.PutFiles(eCtx, codeexecutor.PutFile{
		Path:    "work/a.txt",
		Content: []byte("a"),
	}))
	_, err := ws.RunProgram(
		eCtx,
		codeexecutor.RunProgramSpec{Cmd: "true"},
	)
	require.NoError(t, err)

	creates, cleans := manager.counts()
	require.Equal(t, 1, creates,
		"two calls in one invocation must reuse a single workspace")
	require.Equal(t, 0, cleans,
		"the ephemeral workspace must not be released between calls")

	// The workspace is released once when the invocation context is done.
	cancel()
	waitForCleanup(t, func() int {
		_, n := manager.counts()
		return n
	}, 1)

	// Valid sessions keep their workspace cached across calls: only one
	// create, and the ephemeral cleanup above must not touch it.
	vInv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "sess-1"}),
	)
	vCtx := agent.NewInvocationContext(context.Background(), vInv)
	for i := 0; i < 2; i++ {
		require.NoError(t, ws.PutFiles(vCtx, codeexecutor.PutFile{
			Path:    "work/a.txt",
			Content: []byte("a"),
		}))
	}
	creates, cleans = manager.counts()
	require.Equal(t, 2, creates,
		"session-scoped workspace must be created once and reused")
	require.Equal(t, 1, cleans,
		"session-scoped workspace must not be released after each call")
}

// waitForCleanup polls until the cleanup counter reaches want, failing on
// timeout. It lets tests observe the asynchronous invocation-end release.
func waitForCleanup(t *testing.T, cleans func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if cleans() == want {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("timed out waiting for %d cleanups", want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWorkspace_EphemeralHandleRefreshedAfterStale is the R1 regression: a
// stale operation invalidates the registry entry, so the next call in the
// same invocation re-acquires a new generation. The facade must refresh its
// cached handle to that new generation, otherwise invocation-end release
// targets the stale one (token mismatch, no-op) and leaks the new backend
// workspace forever.
func TestWorkspace_EphemeralHandleRefreshedAfterStale(t *testing.T) {
	manager := &ephemeralProbeManager{}
	backend := &staleOperationBackend{operation: "put_files"}
	eng := codeexecutor.NewEngine(manager, backend, backend)
	ws := New(&stubFSExec{eng: eng}, nil)
	require.NotNil(t, ws)

	eInv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationID("ephemeral-refresh"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eCtx := agent.NewInvocationContext(ctx, eInv)

	// First call returns stale: the registry entry is invalidated without
	// Cleanup, so a later call re-acquires a fresh generation.
	err := ws.PutFiles(eCtx, codeexecutor.PutFile{
		Path:    "work/a.txt",
		Content: []byte("a"),
	})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)

	// Second call in the same invocation re-acquires and succeeds.
	require.NoError(t, ws.PutFiles(eCtx, codeexecutor.PutFile{
		Path:    "work/a.txt",
		Content: []byte("a"),
	}))

	creates, _ := manager.counts()
	require.Equal(t, 2, creates, "stale + retry must create two generations")

	// Invocation end must release the refreshed generation exactly once
	// (the stale H1 was invalidated without Cleanup, so only H2 cleans up).
	cancel()
	waitForCleanup(t, func() int {
		_, n := manager.counts()
		return n
	}, 1)
}

// orderedReleaseManager records whether Cleanup ran before PutFiles finished,
// so a test can prove the no-InvocationID fallback releases after the
// operation (deferred), never before it.
type orderedReleaseManager struct {
	mu              sync.Mutex
	putFilesDone    bool
	cleanupRanEarly bool
}

func (m *orderedReleaseManager) CreateWorkspace(
	_ context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: id, Path: "/tmp/" + id}, nil
}

func (m *orderedReleaseManager) Cleanup(
	_ context.Context,
	_ codeexecutor.Workspace,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.putFilesDone {
		m.cleanupRanEarly = true
	}
	return nil
}

func (m *orderedReleaseManager) markPutFilesDone() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putFilesDone = true
}

func (m *orderedReleaseManager) cleanedUpEarly() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cleanupRanEarly
}

// orderedReleaseFS marks PutFiles completion on the manager so Cleanup can
// detect an out-of-order release.
type orderedReleaseFS struct {
	manager *orderedReleaseManager
}

func (f *orderedReleaseFS) PutFiles(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.PutFile,
) error {
	f.manager.markPutFilesDone()
	return nil
}

func (*orderedReleaseFS) StageDirectory(
	context.Context, codeexecutor.Workspace, string, string, codeexecutor.StageOptions,
) error {
	return nil
}

func (*orderedReleaseFS) Collect(
	context.Context, codeexecutor.Workspace, []string,
) ([]codeexecutor.File, error) {
	return nil, nil
}

func (*orderedReleaseFS) StageInputs(
	context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec,
) error {
	return nil
}

func (*orderedReleaseFS) CollectOutputs(
	context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

func (*orderedReleaseFS) RunProgram(
	context.Context, codeexecutor.Workspace, codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{}, nil
}

// TestWorkspace_EphemeralReleaseAfterOperationWhenNoInvocationID is the R2
// regression: with an empty session ID AND empty InvocationID, the resolver
// uses a random-UUID ephemeral key that cannot be reused across calls. The
// facade must fall back to per-call release, but release must run AFTER the
// operation (deferred), never before it — otherwise Cleanup races the
// PutFiles/Collect/RunProgram that follows.
func TestWorkspace_EphemeralReleaseAfterOperationWhenNoInvocationID(t *testing.T) {
	manager := &orderedReleaseManager{}
	backend := &orderedReleaseFS{manager: manager}
	eng := codeexecutor.NewEngine(manager, backend, backend)
	ws := New(&stubFSExec{eng: eng}, nil)
	require.NotNil(t, ws)

	// Empty Session.ID and no InvocationID -> random-UUID ephemeral key.
	eInv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
	)
	eCtx := agent.NewInvocationContext(context.Background(), eInv)

	require.NoError(t, ws.PutFiles(eCtx, codeexecutor.PutFile{
		Path:    "work/a.txt",
		Content: []byte("a"),
	}))

	require.False(t, manager.cleanedUpEarly(),
		"Cleanup must run after PutFiles completes, not before it")
}

// replacingEphemeralManager creates every workspace at one deterministic
// path and actually removes that path on Cleanup, so a stale handle can
// be shown not to delete a replacement generation.
type replacingEphemeralManager struct {
	mu      sync.Mutex
	path    string
	creates int
	cleans  int
}

func (m *replacingEphemeralManager) CreateWorkspace(
	_ context.Context,
	id string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates++
	if err := os.MkdirAll(m.path, 0o755); err != nil {
		return codeexecutor.Workspace{}, err
	}
	marker := filepath.Join(m.path, "generation.txt")
	if err := os.WriteFile(
		marker,
		[]byte(strconv.Itoa(m.creates)),
		0o644,
	); err != nil {
		return codeexecutor.Workspace{}, err
	}
	return codeexecutor.Workspace{ID: id, Path: m.path}, nil
}

func (m *replacingEphemeralManager) Cleanup(
	_ context.Context,
	ws codeexecutor.Workspace,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleans++
	return os.RemoveAll(ws.Path)
}

func (m *replacingEphemeralManager) counts() (creates, cleans int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.creates, m.cleans
}

func waitForNoCleanup(t *testing.T, cleans func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		require.Equal(t, want, cleans(),
			"stale ephemeral handle must not Cleanup a replacement workspace")
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWorkspace_StaleEphemeralHandleDoesNotCleanupReplacement is the
// facade-side regression: a stale operation on an empty-session handle
// must Invalidate without Cleanup, so a replacement generation at the
// same deterministic path survives the method-level ephemeral defer.
func TestWorkspace_StaleEphemeralHandleDoesNotCleanupReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared-ws")
	manager := &replacingEphemeralManager{path: path}
	backend := &staleOperationBackend{operation: "put_files"}
	eng := codeexecutor.NewEngine(manager, backend, backend)
	ws := New(&stubFSExec{eng: eng}, nil)
	require.NotNil(t, ws)

	eInv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationID("ephemeral-stale-wsio"),
	)
	eCtx := agent.NewInvocationContext(context.Background(), eInv)

	err := ws.PutFiles(eCtx, codeexecutor.PutFile{
		Path:    "work/a.txt",
		Content: []byte("a"),
	})
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	creates, cleans := manager.counts()
	require.Equal(t, 1, creates)
	require.Equal(t, 0, cleans,
		"the stale ephemeral handle must not Cleanup")

	replacement := filepath.Join(path, "replacement.txt")
	require.NoError(t, os.WriteFile(replacement, []byte("next-gen"), 0o644))
	waitForNoCleanup(t, func() int {
		_, n := manager.counts()
		return n
	}, 0)
	require.FileExists(t, replacement,
		"replacement generation at the deterministic path must survive")
	require.FileExists(t, filepath.Join(path, "generation.txt"))
}
