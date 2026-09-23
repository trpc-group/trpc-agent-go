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

	"github.com/creack/pty"
)

// startPTY starts cmd on a fresh pseudo-terminal and returns its master side.
// The pre-start hook runs after preparePTYCommand, so it sees the session and
// controlling-terminal attributes the child starts with, and before pty.Start
// opens the terminal, so a rejection leaves nothing to close. The attributes
// are re-established after the hook, so replacing SysProcAttr cannot change
// how the child is started. ctx reaches only the hook.
func startPTY(
	ctx context.Context,
	cmd *exec.Cmd,
	hook PreStartHook,
) (*os.File, func() error, error) {
	if cmd == nil {
		return nil, nil, errors.New("nil command")
	}

	preparePTYCommand(cmd)
	if err := applyPreStartHook(ctx, cmd, hook); err != nil {
		return nil, nil, err
	}
	preparePTYCommand(cmd)
	master, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}
	return master, master.Close, nil
}
