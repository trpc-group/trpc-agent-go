//go:build !windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

type testSignal string

func (s testSignal) String() string { return string(s) }

func (s testSignal) Signal() {}

func TestPrepareCommandProcessNil(t *testing.T) {
	prepareCommandProcess(nil)
}

func TestSignalCommandProcessNoProcess(t *testing.T) {
	require.NoError(t, signalCommandProcess(nil, syscall.SIGTERM))
	require.NoError(t, signalCommandProcess(&exec.Cmd{}, syscall.SIGTERM))
	require.NoError(
		t,
		signalCommandProcess(
			&exec.Cmd{Process: &os.Process{Pid: 0}},
			syscall.SIGTERM,
		),
	)
}

func TestSignalCommandProcessNonSyscallSignal(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is not available")
	}

	cmd := exec.Command("sleep", "5")
	require.NoError(t, cmd.Start())
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	require.Error(t, signalCommandProcess(cmd, testSignal("custom")))
}
