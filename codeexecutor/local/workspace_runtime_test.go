//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func TestRuntime_RunProgram_Basic(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx,
		"rt-basic",
		codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Stage a file
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{
		{
			Path:    "hello.txt",
			Content: []byte("hello runtime\n"),
			Mode:    0o644,
		},
	})
	require.NoError(t, err)

	// Run bash to print the file
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-c", "cat hello.txt"},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "hello runtime")
}

func TestRuntime_ExecuteInline_PythonOrSkip(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	rt := local.NewRuntime("")
	ctx := context.Background()
	blocks := []codeexecutor.CodeBlock{
		{Language: "python", Code: "print('hi v2')"},
	}
	res, err := rt.ExecuteInline(ctx, "rt-inline", blocks, 5*time.Second)
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "hi v2")
}
