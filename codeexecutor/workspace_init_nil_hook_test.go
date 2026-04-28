//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func TestNewWorkspaceInitExecutor_NilHookRejected(t *testing.T) {
	inner := localexec.New(localexec.WithWorkDir(t.TempDir()))
	valid := codeexecutor.NewWorkspaceInitHook(codeexecutor.WorkspaceInitSpec{
		Commands: []codeexecutor.WorkspaceInitCommand{{Cmd: "true"}},
	})
	_, err := codeexecutor.NewWorkspaceInitExecutor(inner, valid, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "hook 1 is nil")
}
