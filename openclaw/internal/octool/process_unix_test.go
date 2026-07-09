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

const processUnixSignalHelperEnv = "OPENCLAW_TEST_PROCESS_UNIX_SIGNAL_HELPER"

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
	cmd := exec.Command(os.Args[0], "-test.run=TestProcessUnixSignalHelper")
	cmd.Env = append(os.Environ(), processUnixSignalHelperEnv+"=1")
	require.NoError(t, cmd.Start())
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	require.Error(t, signalCommandProcess(cmd, testSignal("custom")))
}

func TestProcessUnixSignalHelper(t *testing.T) {
	if os.Getenv(processUnixSignalHelperEnv) != "1" {
		return
	}
	select {}
}
