//go:build linux

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

	"github.com/stretchr/testify/require"
)

func TestApplyParentDeathSignal(t *testing.T) {
	applyParentDeathSignal(nil)

	attr := &syscall.SysProcAttr{}
	applyParentDeathSignal(attr)
	require.Equal(t, syscall.SIGTERM, attr.Pdeathsig)
}

func TestPrepareCommandsParentDeathSignal(t *testing.T) {
	pipeCmd := &exec.Cmd{}
	preparePipeCommand(pipeCmd)
	require.NotNil(t, pipeCmd.SysProcAttr)
	require.Equal(t, syscall.SIGTERM, pipeCmd.SysProcAttr.Pdeathsig)

	ptyCmd := &exec.Cmd{}
	preparePTYCommand(ptyCmd)
	require.NotNil(t, ptyCmd.SysProcAttr)
	require.Equal(t, syscall.SIGTERM, ptyCmd.SysProcAttr.Pdeathsig)
}
