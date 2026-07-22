//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package localpython

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// The guest leader can leave the process group created at startup. Signal
	// that group for descendants, then address the leader by PID as well.
	groupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	leaderErr := cmd.Process.Kill()
	groupDone := groupErr == nil || errors.Is(groupErr, syscall.ESRCH)
	leaderDone := leaderErr == nil || errors.Is(leaderErr, os.ErrProcessDone)
	switch {
	case groupDone && leaderDone:
		if errors.Is(groupErr, syscall.ESRCH) && errors.Is(leaderErr, os.ErrProcessDone) {
			return os.ErrProcessDone
		}
		return nil
	case groupDone:
		return leaderErr
	case leaderDone:
		return groupErr
	default:
		return errors.Join(groupErr, leaderErr)
	}
}

func cleanupProcessTree(cmd *exec.Cmd) {
	_ = killProcessGroup(cmd)
}
