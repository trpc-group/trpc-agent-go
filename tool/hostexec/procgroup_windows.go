//go:build windows

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
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// preparePipeCommand isolates a pipe-mode child, and — when detached — keeps it
// away from a console no one is there to answer.
//
// Redirecting the standard handles is not enough on Windows any more than a nil
// stdin is on Unix. A console child inherits its parent's console by default,
// and the prompts that matter reach it through the console rather than fd 0:
// they open CONIN$, which returns the console's input buffer whatever the
// standard input handle was set to. Started from a terminal, hostexec would
// hand that buffer to a foreground child no caller can write to.
//
// DETACHED_PROCESS starts the child with no console at all, so opening CONIN$
// fails and the command reports the error instead of waiting out the run
// timeout. It is the flag that matches Setsid on Unix; CREATE_NO_WINDOW would
// give the child a console of its own, which is a worse outcome — invisible,
// unreachable, and still readable by the child. Nothing else about the child
// changes: stdout and stderr are inherited pipe handles, which a console has no
// part in, and terminateProcessTree kills the process directly here rather than
// through a console control event.
//
// Only the detached path gives up the console. A background session is
// registered with the manager and stays addressable — write_stdin can answer
// its prompts — so it keeps what it has always had.
func preparePipeCommand(cmd *exec.Cmd, detach bool) {
	if cmd == nil || !detach {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.DETACHED_PROCESS
}

func preparePTYCommand(_ *exec.Cmd) {}

func commandProcessGroupID(_ *exec.Cmd) int {
	return 0
}

func terminateProcessTree(
	_ context.Context,
	process *os.Process,
	_ int,
	_ time.Duration,
) error {
	return killProcess(process)
}
