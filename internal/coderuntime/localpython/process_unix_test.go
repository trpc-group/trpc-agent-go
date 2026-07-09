//go:build !windows

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package localpython

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigureProcessUsesProcessGroup(t *testing.T) {
	cmd := &exec.Cmd{}
	configureProcess(cmd)
	require.NotNil(t, cmd.SysProcAttr)
	require.True(t, cmd.SysProcAttr.Setpgid)
}

func TestKillProcessGroupNoProcess(t *testing.T) {
	require.NoError(t, killProcessGroup(nil))
	require.NoError(t, killProcessGroup(&exec.Cmd{}))
}
