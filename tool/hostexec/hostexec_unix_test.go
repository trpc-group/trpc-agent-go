//go:build !windows

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
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	invalidProcessGroupID = 1 << 30
	invalidSignalNumber   = 999
	shortWaitTimeout      = 100 * time.Millisecond
)

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitForProcessExit(
	t *testing.T,
	pid int,
	timeout time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("background child pid %d is still alive", pid)
}

func TestApplyParentDeathSignal(t *testing.T) {
	applyParentDeathSignal(nil)

	attr := &syscall.SysProcAttr{}
	applyParentDeathSignal(attr)
	require.Equal(t, syscall.SIGTERM, attr.Pdeathsig)
}

func TestPrepareCommands(t *testing.T) {
	preparePipeCommand(nil)
	preparePTYCommand(nil)

	pipeCmd := &exec.Cmd{}
	preparePipeCommand(pipeCmd)
	require.NotNil(t, pipeCmd.SysProcAttr)
	require.True(t, pipeCmd.SysProcAttr.Setpgid)
	require.Equal(t, syscall.SIGTERM, pipeCmd.SysProcAttr.Pdeathsig)

	ptyCmd := &exec.Cmd{}
	preparePTYCommand(ptyCmd)
	require.NotNil(t, ptyCmd.SysProcAttr)
	require.False(t, ptyCmd.SysProcAttr.Setpgid)
	require.Equal(t, syscall.SIGTERM, ptyCmd.SysProcAttr.Pdeathsig)
}

func TestCommandProcessGroupID(t *testing.T) {
	require.Zero(t, commandProcessGroupID(nil))
	require.Zero(t, commandProcessGroupID(&exec.Cmd{}))
}

func TestTerminateProcessTree_NoProcess(t *testing.T) {
	require.NoError(
		t,
		terminateProcessTree(nil, nil, 0, shortWaitTimeout),
	)
}

func TestTerminateProcessTree_DoneProcess(t *testing.T) {
	cmd, err := shellCmd(context.Background(), "true")
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	_, err = cmd.Process.Wait()
	require.NoError(t, err)

	require.NoError(
		t,
		terminateProcessTree(nil, cmd.Process, 0, shortWaitTimeout),
	)
}

func TestSignalProcessTree(t *testing.T) {
	require.NoError(t, signalProcessTree(nil, 0, syscall.SIGTERM))
	require.NoError(
		t,
		signalProcessTree(
			nil,
			invalidProcessGroupID,
			syscall.SIGTERM,
		),
	)

	pgid, err := syscall.Getpgid(os.Getpid())
	require.NoError(t, err)
	require.Error(
		t,
		signalProcessTree(
			nil,
			pgid,
			syscall.Signal(invalidSignalNumber),
		),
	)

	currentProcess, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.Error(
		t,
		signalProcessTree(
			currentProcess,
			0,
			syscall.Signal(invalidSignalNumber),
		),
	)

	cmd, err := shellCmd(context.Background(), "true")
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	_, err = cmd.Process.Wait()
	require.NoError(t, err)
	require.NoError(t, signalProcessTree(cmd.Process, 0, syscall.SIGTERM))
}

func TestWaitForProcessTreeExit(t *testing.T) {
	require.True(
		t,
		waitForProcessTreeExit(
			context.Background(),
			nil,
			0,
			shortWaitTimeout,
		),
	)

	currentProcess, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	pgid, err := syscall.Getpgid(os.Getpid())
	require.NoError(t, err)

	require.False(
		t,
		waitForProcessTreeExit(
			context.Background(),
			currentProcess,
			pgid,
			0,
		),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(
		t,
		waitForProcessTreeExit(
			ctx,
			currentProcess,
			pgid,
			shortWaitTimeout,
		),
	)
}

func TestProcessTreeAlive(t *testing.T) {
	require.False(t, processTreeAlive(nil, 0))

	currentProcess, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	pgid, err := syscall.Getpgid(os.Getpid())
	require.NoError(t, err)
	require.True(t, processTreeAlive(currentProcess, pgid))

	cmd, err := shellCmd(context.Background(), "true")
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	_, err = cmd.Process.Wait()
	require.NoError(t, err)
	require.False(t, processTreeAlive(cmd.Process, 0))
}
