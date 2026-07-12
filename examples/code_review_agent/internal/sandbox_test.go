//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func platformCommand(unix, windows string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return unix
}

func TestSandbox_ExecuteSuccess(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Timeout:        5 * time.Second,
		MaxOutputBytes: 1024,
		AllowedEnvVars: []string{"PATH"},
	})

	run := sandbox.Execute(context.Background(), "task-1",
		platformCommand("printf hello", "cmd /c echo hello"),
		DecisionAllow, "")

	require.Equal(t, SandboxStatusSuccess, run.Status)
	require.Equal(t, 0, run.ExitCode)
	require.Contains(t, run.Stdout, "hello")
	require.False(t, run.TimedOut)
}

func TestSandbox_ExecuteBlocked(t *testing.T) {
	sandbox := NewDefaultSandbox()

	run := sandbox.Execute(context.Background(), "task-1", "rm -rf /",
		DecisionDeny, "command denied")

	require.Equal(t, SandboxStatusBlocked, run.Status)
	require.Contains(t, run.Error, "blocked")
}

func TestSandbox_ExecuteTimeout(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Timeout:        100 * time.Millisecond,
		MaxOutputBytes: 1024,
		AllowedEnvVars: []string{"PATH"},
	})

	run := sandbox.Execute(context.Background(), "task-1",
		platformCommand("sleep 5", "powershell -NoProfile -Command Start-Sleep -Seconds 5"),
		DecisionAllow, "")

	require.Equal(t, SandboxStatusTimeout, run.Status)
	require.True(t, run.TimedOut)
}

func TestSandbox_ExecuteFailed(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Timeout:        5 * time.Second,
		MaxOutputBytes: 1024,
		AllowedEnvVars: []string{"PATH"},
	})

	run := sandbox.Execute(context.Background(), "task-1",
		platformCommand("false", "cmd /c exit 1"), DecisionAllow, "")

	require.Equal(t, SandboxStatusFailed, run.Status)
	require.NotEqual(t, 0, run.ExitCode)
}

func TestSandbox_OutputTruncation(t *testing.T) {
	sandbox := NewSandbox(SandboxConfig{
		Timeout:        5 * time.Second,
		MaxOutputBytes: 10,
		AllowedEnvVars: []string{"PATH"},
	})

	run := sandbox.Execute(context.Background(), "task-1",
		platformCommand("printf abcdefghijklmnopqrstuvwxyz", "cmd /c echo abcdefghijklmnopqrstuvwxyz"),
		DecisionAllow, "")

	require.Equal(t, SandboxStatusSuccess, run.Status)
	require.Contains(t, run.Stdout, "truncated")
}

func TestSandbox_FailureDoesNotPanic(t *testing.T) {
	sandbox := NewDefaultSandbox()

	// Even with a non-existent command, Execute should return an
	// error run, not panic.
	run := sandbox.Execute(context.Background(), "task-1",
		"nonexistent-command-xyz", DecisionAllow, "")

	require.NotEqual(t, SandboxStatusSuccess, run.Status)
	require.NotEmpty(t, run.Error)
}
