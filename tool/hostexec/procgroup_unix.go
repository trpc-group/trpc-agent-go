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

// preparePipeCommand isolates a pipe-mode child so the manager can clean up its
// whole tree, and — when detached — so it cannot reach a terminal no one is
// there to answer.
//
// Setpgid alone gives the child its own process group but leaves it attached to
// whatever controlling terminal the host process inherited. A command that
// prompts by opening /dev/tty directly — sudo, ssh, git, Python's getpass —
// never touches fd 0 and still finds that terminal, so a nil stdin does not
// reach it. Worse, reading a terminal from a background process group raises
// SIGTTIN and stops the child, so the prompt hangs until the run times out
// instead of failing.
//
// Setsid puts the child in a new session with no controlling terminal, so that
// open fails with ENXIO and the command reports the error immediately. It also
// makes the child a session and process-group leader, so PGID == PID and
// terminateProcessTree still signals the whole tree. The two attributes cannot
// be combined: Go issues setsid before setpgid, and setpgid rejects a session
// leader with EPERM.
//
// Only the detached path gives up the terminal. A background session is
// registered with the manager and remains addressable — write_stdin can answer
// its prompts, and the caller can see it waiting and kill it — so it keeps the
// terminal it has always had.
func preparePipeCommand(cmd *exec.Cmd, detach bool) {
	if cmd == nil {
		return
	}

	attrs := ensureSysProcAttr(cmd)
	if detach {
		attrs.Setsid = true
	} else {
		attrs.Setpgid = true
	}
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
