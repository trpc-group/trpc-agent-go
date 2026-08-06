//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

type contextBoundCleanupManager struct {
	sawDeadline bool
}

func (m *contextBoundCleanupManager) CreateWorkspace(
	context.Context,
	string,
	codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{}, nil
}

func (m *contextBoundCleanupManager) Cleanup(
	ctx context.Context,
	_ codeexecutor.Workspace,
) error {
	_, m.sawDeadline = ctx.Deadline()
	<-ctx.Done()
	return ctx.Err()
}

func TestCleanupWorkspaceHasDeadline(t *testing.T) {
	manager := &contextBoundCleanupManager{}
	started := time.Now()
	cleanupWorkspace(
		manager, codeexecutor.Workspace{}, 10*time.Millisecond,
	)
	require.True(t, manager.sawDeadline)
	require.Less(t, time.Since(started), time.Second)
}
