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
	"sync"
	"testing"

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
