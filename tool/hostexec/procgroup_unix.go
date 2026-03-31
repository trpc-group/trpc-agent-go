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
	"time"
)

const (
	processLivenessSignal = syscall.Signal(0)
	processKillPoll       = 10 * time.Millisecond
)

func preparePipeCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	attrs := ensureSysProcAttr(cmd)
	attrs.Setpgid = true
	applyParentDeathSignal(attrs)
}

func preparePTYCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	applyParentDeathSignal(ensureSysProcAttr(cmd))
}

func ensureSysProcAttr(cmd *exec.Cmd) *syscall.SysProcAttr {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	return cmd.SysProcAttr
}

func commandProcessGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	// Both spawn paths make the child the leader of its owned group.
	// Pipe mode sets Setpgid=true, and PTY mode starts a new session.
	// That keeps PGID == PID here, while signalProcessTree still falls
	// back to direct process signals if group signaling fails.
	return cmd.Process.Pid
}

func terminateProcessTree(
	ctx context.Context,
	process *os.Process,
	processGroupID int,
	grace time.Duration,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if process == nil && processGroupID <= 0 {
		return nil
	}

	if err := signalProcessTree(
		process,
		processGroupID,
		syscall.SIGTERM,
	); err != nil {
		return err
	}
	if waitForProcessTreeExit(ctx, process, processGroupID, grace) {
		return nil
	}
	return signalProcessTree(
		process,
		processGroupID,
		syscall.SIGKILL,
	)
}

func signalProcessTree(
	process *os.Process,
	processGroupID int,
	signal syscall.Signal,
) error {
	if processGroupID > 0 {
		err := syscall.Kill(-processGroupID, signal)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if !errors.Is(err, syscall.EPERM) {
			return err
		}
	}

	if process == nil {
		return nil
	}
	err := process.Signal(signal)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrProcessDone):
		return nil
	case errors.Is(err, syscall.ESRCH):
		return nil
	default:
		return err
	}
}

func waitForProcessTreeExit(
	ctx context.Context,
	process *os.Process,
	processGroupID int,
	grace time.Duration,
) bool {
	if !processTreeAlive(process, processGroupID) {
		return true
	}
	if grace <= 0 {
		return false
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()

	ticker := time.NewTicker(processKillPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return !processTreeAlive(process, processGroupID)
		case <-ticker.C:
			if !processTreeAlive(process, processGroupID) {
				return true
			}
		}
	}
}

func processTreeAlive(
	process *os.Process,
	processGroupID int,
) bool {
	if processGroupID > 0 {
		switch err := syscall.Kill(
			-processGroupID,
			processLivenessSignal,
		); {
		case err == nil:
			return true
		case errors.Is(err, syscall.EPERM):
			return true
		case errors.Is(err, syscall.ESRCH):
			return false
		}
	}
	if process == nil {
		return false
	}

	err := process.Signal(processLivenessSignal)
	switch {
	case err == nil:
		return true
	case errors.Is(err, syscall.EPERM):
		return true
	case errors.Is(err, os.ErrProcessDone):
		return false
	case errors.Is(err, syscall.ESRCH):
		return false
	default:
		return false
	}
}
