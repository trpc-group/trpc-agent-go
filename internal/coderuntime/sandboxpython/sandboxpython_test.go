//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package sandboxpython

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
)

func TestRunnerStartsGuestWithCleanEnvAndRemovesWorkspace(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	t.Setenv("TRPC_SANDBOX_ENV_PROBE", "must-not-leak")
	root := t.TempDir()
	runner := New(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(root),
		sandbox.WithSessionPolicy(sandbox.SessionPolicy{
			Persistence: sandbox.SessionPersistencePerSession,
		}),
	)
	process, err := runner.StartScript(
		context.Background(),
		Config{},
		"print('ok')",
		"guest.py",
		[]byte("import os\nprint(os.getenv('TRPC_SANDBOX_ENV_PROBE'))\nprint(os.getcwd())\n"),
		nil,
		nil,
	)
	require.NoError(t, err)
	workspacePath := process.ws.Path
	require.DirExists(t, workspacePath)
	require.NoError(t, process.Stdin().Close())
	out, err := io.ReadAll(process.Stdout())
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, process.Stderr())
	require.NoError(t, err)
	require.NoError(t, process.Wait())
	require.NotContains(t, string(out), "must-not-leak")
	require.Contains(t, string(out), filepath.Join(workspacePath, "work"))
	require.NoDirExists(t, workspacePath)
}

func TestRunnerKillReapsProcessAndRemovesWorkspace(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	runner := New(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(t.TempDir()),
	)
	process, err := runner.StartScript(
		context.Background(),
		Config{},
		"while True: pass",
		"guest.py",
		[]byte("while True:\n    pass\n"),
		nil,
		nil,
	)
	require.NoError(t, err)
	workspacePath := process.ws.Path
	var drains sync.WaitGroup
	drains.Add(2)
	go func() {
		defer drains.Done()
		_, _ = io.Copy(io.Discard, process.Stdout())
	}()
	go func() {
		defer drains.Done()
		_, _ = io.Copy(io.Discard, process.Stderr())
	}()

	require.NoError(t, process.Kill())
	require.Error(t, process.Wait())
	drains.Wait()
	require.NoDirExists(t, workspacePath)
}

func TestRunnerRejectsCodeBeforeCreatingWorkspace(t *testing.T) {
	root := t.TempDir()
	runner := New(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(root),
	)
	_, err := runner.StartScript(
		context.Background(),
		Config{MaxCodeBytes: 4},
		"return None",
		"guest.py",
		[]byte("print('x')"),
		nil,
		nil,
	)
	require.ErrorContains(t, err, "code exceeds 4 bytes")
	entries, readErr := os.ReadDir(root)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestRunnerRejectsEmptyScriptName(t *testing.T) {
	root := t.TempDir()
	runner := New(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(root),
	)
	_, err := runner.StartScript(
		context.Background(),
		Config{},
		"return None",
		" ",
		[]byte("print('x')"),
		nil,
		nil,
	)
	require.ErrorContains(t, err, "script name is required")
	entries, readErr := os.ReadDir(root)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestRunnerCleansWorkspaceWhenGuestStartFails(t *testing.T) {
	root := t.TempDir()
	runner := New(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(root),
	)
	_, err := runner.StartScript(
		context.Background(),
		Config{Python: "python-that-does-not-exist"},
		"return None",
		"guest.py",
		[]byte("print('x')"),
		nil,
		nil,
	)
	require.Error(t, err)
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".py") {
			t.Fatalf("guest file remained after start failure: %s", path)
		}
		return nil
	}))
}

func TestRunnerSupportsRelativePythonPath(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	pythonPath, err = filepath.Abs(pythonPath)
	require.NoError(t, err)
	currentDir, err := os.Getwd()
	require.NoError(t, err)
	relativePython, err := filepath.Rel(currentDir, pythonPath)
	require.NoError(t, err)

	runner := New(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(t.TempDir()),
	)
	process, err := runner.StartScript(
		context.Background(),
		Config{Python: relativePython},
		"print('ok')",
		"guest.py",
		[]byte("print('ok')\n"),
		nil,
		nil,
	)
	require.NoError(t, err)
	out, err := io.ReadAll(process.Stdout())
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, process.Stderr())
	require.NoError(t, err)
	require.NoError(t, process.Wait())
	require.Equal(t, "ok", strings.TrimSpace(string(out)))
}

func TestRunnerTimeoutReapsProcessAndRemovesWorkspace(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	runner := New(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(t.TempDir()),
	)
	process, err := runner.StartScript(
		context.Background(),
		Config{Timeout: 20 * time.Millisecond},
		"while True: pass",
		"guest.py",
		[]byte("while True:\n    pass\n"),
		nil,
		nil,
	)
	require.NoError(t, err)
	workspacePath := process.ws.Path
	var drains sync.WaitGroup
	drains.Add(2)
	go func() {
		defer drains.Done()
		_, _ = io.Copy(io.Discard, process.Stdout())
	}()
	go func() {
		defer drains.Done()
		_, _ = io.Copy(io.Discard, process.Stderr())
	}()
	require.ErrorIs(t, process.Wait(), context.DeadlineExceeded)
	drains.Wait()
	require.NoDirExists(t, workspacePath)
}

func TestRunnerContextCancellationReapsProcessAndRemovesWorkspace(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	runner := New(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(t.TempDir()),
	)
	ctx, cancel := context.WithCancel(context.Background())
	process, err := runner.StartScript(
		ctx,
		Config{},
		"while True: pass",
		"guest.py",
		[]byte("while True:\n    pass\n"),
		nil,
		nil,
	)
	require.NoError(t, err)
	workspacePath := process.ws.Path
	var drains sync.WaitGroup
	drains.Add(2)
	go func() {
		defer drains.Done()
		_, _ = io.Copy(io.Discard, process.Stdout())
	}()
	go func() {
		defer drains.Done()
		_, _ = io.Copy(io.Discard, process.Stderr())
	}()

	cancel()
	require.ErrorIs(t, process.Wait(), context.Canceled)
	drains.Wait()
	require.NoDirExists(t, workspacePath)
}

func TestResolvePythonPathUsesHostPathAndReturnsAbsolutePath(t *testing.T) {
	binDir := t.TempDir()
	name := "custom-python"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	python := filepath.Join(binDir, name)
	require.NoError(t, os.WriteFile(python, []byte("#!/bin/sh\n"), 0o700))
	t.Setenv("PATH", binDir)

	resolved, err := resolvePythonPath(name)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(resolved))
	require.Equal(t, python, resolved)
}

func TestResolvePythonPathKeepsDefaultSandboxResolved(t *testing.T) {
	resolved, err := resolvePythonPath("")
	require.NoError(t, err)
	require.Equal(t, "python3", resolved)
}

func TestNilRunnerIsRejected(t *testing.T) {
	var runner *Runner
	_, err := runner.StartScript(
		context.Background(),
		Config{},
		"return None",
		"guest.py",
		[]byte("print('x')"),
		nil,
		nil,
	)
	require.ErrorContains(t, err, "runner is required")
}

func TestNilProcessOperations(t *testing.T) {
	var process *Process
	require.Nil(t, process.Stdin())
	require.Nil(t, process.Stdout())
	require.Nil(t, process.Stderr())
	require.NoError(t, process.Kill())
	require.NoError(t, process.Wait())
}

func TestNegativeCodeLimitDisablesValidation(t *testing.T) {
	require.NoError(t, validateCodeSize(strings.Repeat("x", defaultMaxCodeBytes+1), -1))
	require.Error(t, validateCodeSize(strings.Repeat("x", defaultMaxCodeBytes+1), 0))
}

func TestRunnerDoesNotFallbackFromUnsupportedBackend(t *testing.T) {
	backend := sandbox.BackendAuto
	switch runtime.GOOS {
	case "linux":
		backend = sandbox.BackendMacOSSandboxExec
	case "darwin":
		backend = sandbox.BackendLinuxBubblewrap
	}
	root := t.TempDir()
	runner := New(
		sandbox.WithBackend(backend),
		sandbox.WithWorkspaceRoot(root),
	)
	_, err := runner.StartScript(
		context.Background(),
		Config{},
		"return None",
		"guest.py",
		[]byte("print('x')"),
		nil,
		nil,
	)
	require.Error(t, err)
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && info.Name() == "guest.py" {
			t.Fatalf("unsupported backend fell back and retained guest script: %s", path)
		}
		return nil
	}))
}
