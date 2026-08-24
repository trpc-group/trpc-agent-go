//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// startPipes opens three pipes in sequence, and a failure partway through has to
// close the ones already opened rather than leak them. os/exec refuses to hand
// out a pipe for a stream the caller already wired up, which is what makes each
// of those partial failures reachable.
func TestStartPipesClosesOpenedPipesOnFailure(t *testing.T) {
	t.Run("stdin pipe fails", func(t *testing.T) {
		cmd := &exec.Cmd{Stdin: strings.NewReader("")}
		stdin, stdout, stderr, err := startPipes(cmd, keepStdin)
		require.Error(t, err)
		require.Nil(t, stdin)
		require.Nil(t, stdout)
		require.Nil(t, stderr)
	})

	t.Run("stdout pipe fails after stdin opened", func(t *testing.T) {
		// The stdin pipe is already open here, so this is the case that needs
		// closeStdin to actually close something.
		cmd := &exec.Cmd{Stdout: io.Discard}
		stdin, stdout, stderr, err := startPipes(cmd, keepStdin)
		require.Error(t, err)
		require.Nil(t, stdin)
		require.Nil(t, stdout)
		require.Nil(t, stderr)
	})

	t.Run("stderr pipe fails after stdin and stdout opened", func(t *testing.T) {
		cmd := &exec.Cmd{Stderr: io.Discard}
		stdin, stdout, stderr, err := startPipes(cmd, keepStdin)
		require.Error(t, err)
		require.Nil(t, stdin)
		require.Nil(t, stdout)
		require.Nil(t, stderr)
	})

	t.Run("detached failure has no stdin to close", func(t *testing.T) {
		// closeStdin must tolerate the detached case, where stdin was never
		// opened and the nil it holds is the normal state rather than a bug.
		cmd := &exec.Cmd{Stdout: io.Discard}
		stdin, stdout, stderr, err := startPipes(cmd, detachStdin)
		require.Error(t, err)
		require.Nil(t, stdin)
		require.Nil(t, stdout)
		require.Nil(t, stderr)
	})
}

// The detached path must not open a stdin pipe at all: a nil WriteCloser is what
// makes os/exec back fd 0 with the null device.
func TestStartPipesDetachLeavesStdinNil(t *testing.T) {
	cmd := &exec.Cmd{}
	stdin, stdout, stderr, err := startPipes(cmd, detachStdin)
	require.NoError(t, err)
	// Registered before the assertions, and holding this call's pipes rather
	// than the variables, so a failing assertion still closes them.
	closePipes(t, stdin, stdout, stderr)
	require.Nil(t, stdin)
	require.NotNil(t, stdout)
	require.NotNil(t, stderr)

	cmd = &exec.Cmd{}
	stdin, stdout, stderr, err = startPipes(cmd, keepStdin)
	require.NoError(t, err)
	closePipes(t, stdin, stdout, stderr)
	require.NotNil(t, stdin, "the background path keeps a writable stdin")
}

// closePipes closes one startPipes call's streams when the test ends.
func closePipes(
	t *testing.T,
	stdin io.WriteCloser,
	stdout io.ReadCloser,
	stderr io.ReadCloser,
) {
	t.Helper()
	t.Cleanup(func() {
		if stdin != nil {
			_ = stdin.Close()
		}
		if stdout != nil {
			_ = stdout.Close()
		}
		if stderr != nil {
			_ = stderr.Close()
		}
	})
}
