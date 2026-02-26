//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunInspect_DefaultIsPlugins(t *testing.T) {
	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runInspect(nil))
	})

	require.Empty(t, stderr)
	require.Contains(t, stdout, "Model types")
	require.Contains(t, stdout, "mock")
	require.Contains(t, stdout, "telegram")
}

func TestRunInspect_UnknownCommand(t *testing.T) {
	_, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 2, runInspect([]string{"nope"}))
	})

	require.Contains(t, stderr, "unknown inspect command")
	require.Contains(t, stderr, "Usage:")
}

func TestRunInspect_ConfigKeys(t *testing.T) {
	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runInspect([]string{
			inspectCmdConfigKeys,
			"-telegram-token",
			"x",
			"-enable-openclaw-tools",
		}))
	})

	require.Empty(t, stderr)

	got := strings.Split(strings.TrimSpace(stdout), "\n")
	want := []string{
		"channels.telegram",
		"channels.telegram.token",
		"plugins.entries.telegram.config",
		"plugins.entries.telegram.config.token",
		"plugins.entries.telegram.enabled",
		"tools.bash",
		"tools.exec",
		"tools.process",
	}
	require.Equal(t, want, got)
}

func captureInspectOutput(
	t *testing.T,
	fn func(),
) (stdout string, stderr string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr

	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	})

	fn()

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)

	return string(out), string(errOut)
}
