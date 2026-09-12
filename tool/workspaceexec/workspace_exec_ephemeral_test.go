//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspaceexec

import (
	"context"
	"encoding/json"
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
	return codeexecutor.Workspace{
		ID:   id,
		Path: filepath.Join(os.TempDir(), id),
	}, nil
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

// TestExecTool_EphemeralWorkspaceReleasedAfterCall is the production-path
// regression for the empty-session workspace leak: workspace_exec calls
// CreateWorkspaceHandle for every invocation, and an invocation with an
// invalid (empty-ID) session gets an ephemeral workspace that no session
// lifecycle owns. The tool must release it at the end of the call —
// cleaning up the backend workspace and dropping the registry entry —
// while valid sessions keep their workspace cached and reused.
func TestExecTool_EphemeralWorkspaceReleasedAfterCall(t *testing.T) {
	manager := &ephemeralProbeManager{}
	exec := &staleRetryExec{
		eng: codeexecutor.NewEngine(
			manager,
			&nonInteractiveFS{},
			&nonInteractiveRunner{},
		),
	}
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{
		Command: "echo ok",
		Timeout: timeoutSecSmall,
	})
	require.NoError(t, err)

	// Empty session ID -> ephemeral invocation workspace.
	eInv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
	)
	eCtx := agent.NewInvocationContext(context.Background(), eInv)

	_, err = tl.Call(eCtx, args)
	require.NoError(t, err)
	creates, cleans := manager.counts()
	require.Equal(t, 1, creates,
		"one ephemeral workspace must be created for the call")
	require.Equal(t, 1, cleans,
		"ephemeral workspace must be cleaned up after the call")
	require.Equal(t, manager.lastCreated(), manager.lastCleaned())

	// The released entry must not linger: the next call in the same
	// invocation creates a fresh workspace (and releases it too),
	// never reusing the released one.
	_, err = tl.Call(eCtx, args)
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
		_, err = tl.Call(vCtx, args)
		require.NoError(t, err)
	}
	creates, cleans = manager.counts()
	require.Equal(t, 3, creates,
		"session-scoped workspace must be created once and reused")
	require.Equal(t, 2, cleans,
		"session-scoped workspace must not be released after each call")
}

// TestExecTool_NonSessionalBackgroundReleasesEphemeralWorkspace covers the
// twin branch of startInteractive: a non-sessional executor (no
// InteractiveProgramRunner) with background/tty requested returns the
// "interactive sessions are not supported" error, but the workspace was
// already acquired for the attempt and must still be released. Without this,
// every such call through a long-lived process leaks one backend workspace.
func TestExecTool_NonSessionalBackgroundReleasesEphemeralWorkspace(t *testing.T) {
	manager := &ephemeralProbeManager{}
	exec := &staleRetryExec{
		eng: codeexecutor.NewEngine(
			manager,
			&nonInteractiveFS{},
			&nonInteractiveRunner{},
		),
	}
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{
		Command:    "echo ok",
		Background: true,
		Timeout:    timeoutSecSmall,
	})
	require.NoError(t, err)

	// Empty session ID -> ephemeral workspace; non-sessional executor +
	// background=true -> callNonSessional's interactive-unsupported branch.
	_, err = tl.Call(ephemeralEmptySessionCtx(), args)
	require.Error(t, err)
	require.Contains(t, err.Error(), "interactive sessions")

	creates, cleans := manager.counts()
	require.Equal(t, 1, creates,
		"one ephemeral workspace must be created for the attempt")
	require.Equal(t, 1, cleans,
		"the background error branch must release the ephemeral workspace")
	require.Equal(t, manager.lastCreated(), manager.lastCleaned(),
		"the acquired workspace must be the one released")
}

// replacingEphemeralManager creates every workspace at one deterministic
// path and records Cleanup so tests can prove a stale handle does not
// delete a replacement generation that reused that path.
type replacingEphemeralManager struct {
	mu      sync.Mutex
	probes  []codeexecutor.WorkspaceInstanceID
	current codeexecutor.WorkspaceInstanceID
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

func (m *replacingEphemeralManager) InstanceID(
	context.Context,
) (codeexecutor.WorkspaceInstanceID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.probes) > 0 {
		m.current = m.probes[0]
		m.probes = m.probes[1:]
	}
	if m.current == "" {
		return "", nil
	}
	return m.current, nil
}

func (m *replacingEphemeralManager) counts() (creates, cleans int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.creates, m.cleans
}

func ephemeralEmptySessionCtx() context.Context {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationID("ephemeral-stale-inv"),
	)
	return agent.NewInvocationContext(context.Background(), inv)
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

// TestExecTool_StaleEphemeralHandleDoesNotCleanupReplacement is the
// regression for releasing a stale empty-session handle: Cleanup would
// delete the deterministic path after a replacement instance already
// owns it. Stale must Invalidate only.
func TestExecTool_StaleEphemeralHandleDoesNotCleanupReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared-ws")
	manager := &replacingEphemeralManager{
		path: path,
		probes: []codeexecutor.WorkspaceInstanceID{
			"gen-1", "gen-1", "gen-2",
			"gen-2", "gen-2", "gen-3",
		},
	}
	exec := &staleRetryExec{
		eng: codeexecutor.NewEngine(
			manager,
			&nonInteractiveFS{},
			&nonInteractiveRunner{},
		),
	}
	tl := NewExecTool(exec)
	args, err := json.Marshal(execInput{
		Command: "echo ok",
		Timeout: timeoutSecSmall,
	})
	require.NoError(t, err)

	_, err = tl.Call(ephemeralEmptySessionCtx(), args)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	creates, cleans := manager.counts()
	require.Equal(t, 2, creates,
		"a retry-safe stale fence reacquires once; neither generation is Cleaned up")
	require.Equal(t, 0, cleans,
		"the stale acquired handle must not Cleanup")

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

// TestExecTool_PostReconcileStaleEphemeralDoesNotCleanupReplacement
// covers the post-reconcile fence: bootstrap may have started, the
// handle is stale, and Release would still delete the replacement.
func TestExecTool_PostReconcileStaleEphemeralDoesNotCleanupReplacement(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "shared-ws")
	manager := &replacingEphemeralManager{
		path: path,
		probes: []codeexecutor.WorkspaceInstanceID{
			"gen-1", "gen-1", "gen-1", "gen-2",
		},
	}
	exec := &staleRetryExec{
		eng: codeexecutor.NewEngine(
			manager,
			&nonInteractiveFS{},
			&nonInteractiveRunner{},
		),
	}
	tl := NewExecTool(
		exec,
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Commands: []codeexecutor.WorkspaceCommand{{Cmd: "true"}},
		}),
	)
	args, err := json.Marshal(execInput{
		Command: "echo ok",
		Timeout: timeoutSecSmall,
	})
	require.NoError(t, err)

	_, err = tl.Call(ephemeralEmptySessionCtx(), args)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)
	_, cleans := manager.counts()
	require.Equal(t, 0, cleans)

	replacement := filepath.Join(path, "replacement.txt")
	require.NoError(t, os.WriteFile(replacement, []byte("next-gen"), 0o644))
	waitForNoCleanup(t, func() int {
		_, n := manager.counts()
		return n
	}, 0)
	require.FileExists(t, replacement)
}

// TestExecTool_StaleEphemeralSessionWriteDoesNotCleanupReplacement
// covers invalidateSessionWorkspaceIfStale: a stale write on an
// empty-session interactive handle must drop the cache entry without
// Cleanup.
func TestExecTool_StaleEphemeralSessionWriteDoesNotCleanupReplacement(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "shared-ws")
	manager := &replacingEphemeralManager{
		path:    path,
		current: "gen-1",
	}
	exec := &staleRetryExec{
		eng: codeexecutor.NewEngine(
			manager,
			&nonInteractiveFS{},
			staleWriteRunner{},
		),
	}
	tl := NewExecTool(exec)
	startArgs, err := json.Marshal(execInput{
		Command:    "interactive",
		Background: true,
		Timeout:    timeoutSecSmall,
	})
	require.NoError(t, err)

	ctx := ephemeralEmptySessionCtx()
	started, err := tl.Call(ctx, startArgs)
	require.NoError(t, err)
	sessionID := started.(execOutput).SessionID
	require.NotEmpty(t, sessionID)
	_, cleans := manager.counts()
	require.Equal(t, 0, cleans)

	writeArgs, err := json.Marshal(writeInput{
		SessionID: sessionID,
		Chars:     "must-not-cleanup",
	})
	require.NoError(t, err)
	_, err = NewWriteStdinTool(tl).Call(ctx, writeArgs)
	require.ErrorIs(t, err, codeexecutor.ErrWorkspaceStale)

	replacement := filepath.Join(path, "replacement.txt")
	require.NoError(t, os.WriteFile(replacement, []byte("next-gen"), 0o644))
	waitForNoCleanup(t, func() int {
		_, n := manager.counts()
		return n
	}, 0)
	require.FileExists(t, replacement)
}
