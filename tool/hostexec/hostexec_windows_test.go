//go:build windows

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
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func processExists(_ int) bool {
	return false
}

func waitForProcessExit(
	t *testing.T,
	_ int,
	_ time.Duration,
) {
	t.Helper()
}

// The detached foreground child must start without a console. Redirecting the
// standard handles leaves CONIN$ open to it, and a prompt that reads the console
// there waits out the run timeout that this disposition exists to avoid.
func TestPrepareCommands(t *testing.T) {
	preparePipeCommand(nil, keepStdin)
	preparePipeCommand(nil, detachStdin)
	preparePTYCommand(nil)

	// The background path stays attached: its session is registered with the
	// manager, so write_stdin can answer whatever it asks.
	pipeCmd := &exec.Cmd{}
	preparePipeCommand(pipeCmd, keepStdin)
	if pipeCmd.SysProcAttr != nil {
		require.Zero(t,
			pipeCmd.SysProcAttr.CreationFlags&windows.DETACHED_PROCESS)
	}

	detachedCmd := &exec.Cmd{}
	preparePipeCommand(detachedCmd, detachStdin)
	require.NotNil(t, detachedCmd.SysProcAttr)
	require.NotZero(t,
		detachedCmd.SysProcAttr.CreationFlags&windows.DETACHED_PROCESS)

	// A caller's own flags survive: the disposition adds one bit rather than
	// replacing the field.
	preservedCmd := &exec.Cmd{
		SysProcAttr: &syscall.SysProcAttr{
			CreationFlags: windows.CREATE_UNICODE_ENVIRONMENT,
		},
	}
	preparePipeCommand(preservedCmd, detachStdin)
	require.NotZero(t,
		preservedCmd.SysProcAttr.CreationFlags&windows.CREATE_UNICODE_ENVIRONMENT)
	require.NotZero(t,
		preservedCmd.SysProcAttr.CreationFlags&windows.DETACHED_PROCESS)
}
