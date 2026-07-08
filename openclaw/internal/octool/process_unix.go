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
)

func prepareCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateCommandProcess(cmd *exec.Cmd) error {
	return signalCommandProcess(cmd, syscall.SIGTERM)
}

func forceKillCommandProcess(cmd *exec.Cmd) error {
	return signalCommandProcess(cmd, syscall.SIGKILL)
}

func cleanupCommandProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == nil || err == syscall.ESRCH {
		return nil
	}
	return err
}

func signalCommandProcess(cmd *exec.Cmd, sig os.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	sysSig, ok := sig.(syscall.Signal)
	if !ok {
		return cmd.Process.Signal(sig)
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
		if err := syscall.Kill(-pgid, sysSig); err == nil {
			return nil
		}
	}
	return cmd.Process.Signal(sig)
}
