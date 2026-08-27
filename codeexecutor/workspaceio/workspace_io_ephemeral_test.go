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

// TestWorkspace_EphemeralWorkspaceReleasedAfterEachCall is the
// production-path regression for the empty-session workspace leak:
// every Workspace facade method binds a handle via
// CreateWorkspaceHandle, and an invocation with an invalid (empty-ID)
// session gets an ephemeral workspace that no session lifecycle owns.
// Each call must release it on completion — cleaning up the backend
// workspace and dropping the registry entry — while valid sessions keep
// their workspace cached and reused across calls.
func TestWorkspace_EphemeralWorkspaceReleasedAfterEachCall(t *testing.T) {
	manager := &ephemeralProbeManager{}
	backend := &staleOperationBackend{}
	eng := codeexecutor.NewEngine(manager, backend, backend)
	ws := New(&stubFSExec{eng: eng}, nil)
	require.NotNil(t, ws)

	// Empty session ID -> ephemeral invocation workspace.
	eInv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
	)
	eCtx := agent.NewInvocationContext(context.Background(), eInv)

	require.NoError(t, ws.PutFiles(eCtx, codeexecutor.PutFile{
		Path:    "work/a.txt",
		Content: []byte("a"),
	}))
	creates, cleans := manager.counts()
	require.Equal(t, 1, creates,
		"one ephemeral workspace must be created for the call")
	require.Equal(t, 1, cleans,
		"ephemeral workspace must be cleaned up after the call")

	// The released entry must not linger: the next call in the same
	// invocation creates a fresh workspace (and releases it too).
	_, err := ws.RunProgram(
		eCtx,
		codeexecutor.RunProgramSpec{Cmd: "true"},
	)
	require.NoError(t, err)
	creates, cleans = manager.counts()
	require.Equal(t, 2, creates,
		"a new call must not reuse the released ephemeral entry")
	require.Equal(t, 2, cleans,
		"every ephemeral workspace must be released, not leaked")

	// Valid sessions keep their workspace cached across calls: only one
	// create, and the ephemeral cleanups above must not touch it.
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
	require.Equal(t, 3, creates,
		"session-scoped workspace must be created once and reused")
	require.Equal(t, 2, cleans,
		"session-scoped workspace must not be released after each call")
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
