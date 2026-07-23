//go:build !windows

// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package internal

import (
	"errors"
	"os/exec"
	"syscall"
)

func prepareProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
