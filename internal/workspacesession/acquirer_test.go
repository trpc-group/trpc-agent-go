//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspacesession

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// funcAcquirer is a non-pointer WorkspaceAcquirer used to check that typed
// nils of a dynamic kind other than pointer also normalize to true nil.
type funcAcquirer func()

func (funcAcquirer) Acquire(
	context.Context, codeexecutor.WorkspaceManager, string,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{}, nil
}

func TestNormalizeAcquirer(t *testing.T) {
	require.True(t, NormalizeAcquirer(nil) == nil)

	var nilPtr *codeexecutor.WorkspaceRegistry
	require.True(t, NormalizeAcquirer(nilPtr) == nil)

	var nilFunc funcAcquirer
	require.True(t, NormalizeAcquirer(nilFunc) == nil)

	real := codeexecutor.NewWorkspaceRegistry()
	require.Same(t, real, NormalizeAcquirer(real))
}
