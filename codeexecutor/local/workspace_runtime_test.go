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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

const permDenied = "permission denied"

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

func TestRuntime_PutDirectory_And_Collect(t *testing.T) {
	const short = 5 * time.Second

	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-dir", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Build a source directory with nested files.
	src := t.TempDir()
	sub := filepath.Join(src, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(sub, "a.txt"), []byte("alpha"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "img.bin"), []byte{0x1, 0x2, 0x3}, 0o644,
	))

	// Copy directory into workspace under dst/.
	require.NoError(t, rt.PutDirectory(ctx, ws, src, "dst"))

	// Collect using doublestar patterns.
	files, err := rt.Collect(ctx, ws, []string{
		"dst/**/*.txt", "dst/*.bin",
	})
	require.NoError(t, err)
	require.Len(t, files, 2)
	// Names are relative to workspace root.
	require.Contains(t, []string{files[0].Name, files[1].Name},
		"dst/sub/a.txt")
	require.Contains(t, []string{files[0].Name, files[1].Name},
		"dst/img.bin")
}

func TestRuntime_PutSkill_ReadOnly(t *testing.T) {
	const short = 5 * time.Second

	rt := local.NewRuntimeWithOptions(
		"", local.WithReadOnlyStagedSkill(true),
	)
	ctx := context.Background()
	// Use a unique execID per run to avoid leftover perms between runs.
	ws, err := rt.CreateWorkspace(
		ctx, "rt-skill-"+t.Name(), codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Prepare a skill directory.
	skillDir := t.TempDir()
	f := filepath.Join(skillDir, "script.sh")
	require.NoError(t, os.WriteFile(f, []byte("echo ok"), 0o755))

	// Stage skill under target path and make it read-only.
	// On some hosts, staging may hit transient permission policies;
	// if so, skip to avoid environment-specific flakiness.
	if err := rt.PutSkill(ctx, ws, skillDir, "tool"); err != nil {
		if strings.Contains(err.Error(), permDenied) {
			t.Skip("skip due to permission policy: " + err.Error())
		}
		require.NoError(t, err)
	}

	// Attempt to append to the file should fail due to a-w.
	target := filepath.Join(ws.Path, "tool", "script.sh")
	ff, err := os.OpenFile(target, os.O_WRONLY|os.O_APPEND, 0)
	require.Error(t, err)
	if ff != nil {
		_ = ff.Close()
	}

	// Still executable: run it.
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", "./tool/script.sh"},
		Timeout: short,
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Contains(t, res.Stdout, "ok")
}

func TestRuntime_PutFiles_PathEscapeRejected(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-escape", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "../evil.txt",
		Content: []byte("x"),
		Mode:    0,
	}})
	require.Error(t, err)
}

func TestRuntime_RunProgram_EnvAndStdin(t *testing.T) {
	const short = 5 * time.Second

	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-env", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-lc", "cat; echo $FOO"},
		Env:  map[string]string{"FOO": "BAR"},
		// Provide data via stdin so cat prints it.
		Stdin:   "HELLO\n",
		Timeout: short,
	})
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "HELLO")
	require.Contains(t, res.Stdout, "BAR")
}

func TestRuntime_RunProgram_Timeout(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-timeout", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Intentionally time out a sleeping command.
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", "sleep 1; echo late"},
		Timeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	require.True(t, res.TimedOut)
}

func TestRuntime_RunProgram_DefaultTimeoutUsed(t *testing.T) {
	// When Timeout is zero, runtime uses defaultTimeout(); ensure
	// the call path executes successfully without timing out.
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-default", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", "true"},
		Timeout: 0,
	})
	require.NoError(t, err)
	require.False(t, res.TimedOut)
}

func TestRuntime_RunProgram_NonexistentCommandExitCode(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-missing-cmd", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "this-command-should-not-exist",
		Args:    []string{"--version"},
		Timeout: 500 * time.Millisecond,
	})
	require.NoError(t, err)
	// Non-ExitError maps to -1 per implementation.
	require.Equal(t, -1, res.ExitCode)
}

func TestRuntime_PutSkill_ReadOnly_EmptyDir(t *testing.T) {
	// Exercise makeTreeReadOnly without file permission churn by
	// staging an empty directory and enabling read-only flag.
	rt := local.NewRuntimeWithOptions(
		"", local.WithReadOnlyStagedSkill(true),
	)
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-ro-empty", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	empty := t.TempDir()
	err = rt.PutSkill(ctx, ws, empty, "tool")
	if err != nil && strings.Contains(err.Error(), permDenied) {
		t.Skip("skip due to permission policy: " + err.Error())
	}
	require.NoError(t, err)
}

func TestRuntime_ExecuteInline_Bash(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	blocks := []codeexecutor.CodeBlock{{
		Language: "bash",
		Code:     "echo hi-bash",
	}}
	res, err := rt.ExecuteInline(
		ctx, "rt-inline-bash", blocks, 5*time.Second,
	)
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "hi-bash")
}

func TestRuntime_Collect_EmptyPatterns(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-empty", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	files, err := rt.Collect(ctx, ws, nil)
	require.NoError(t, err)
	require.Nil(t, files)
}

func TestRuntime_Collect_PathTraversalFiltered(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-filter", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Write a file outside the workspace root.
	outside := filepath.Join(filepath.Dir(ws.Path), "ext.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))

	// Attempt to collect via traversal; should be filtered out.
	files, err := rt.Collect(ctx, ws, []string{"../*.txt"})
	require.NoError(t, err)
	if len(files) > 0 {
		for _, f := range files {
			require.NotEqual(t, "ext.txt", f.Name)
		}
	}
}

func TestRuntime_PutFiles_EmptyPathError(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-empty-path", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "",
		Content: []byte("x"),
		Mode:    0,
	}})
	require.Error(t, err)
}

func TestRuntime_PutDirectory_PreservesMode(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-preserve", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	src := t.TempDir()
	execFile := filepath.Join(src, "run.sh")
	require.NoError(t, os.WriteFile(execFile, []byte("echo"), 0o755))
	require.NoError(t, rt.PutDirectory(ctx, ws, src, "dst"))

	target := filepath.Join(ws.Path, "dst", "run.sh")
	st, err := os.Stat(target)
	require.NoError(t, err)
	// Executable bit should be preserved.
	require.NotZero(t, st.Mode()&0o111)
}
