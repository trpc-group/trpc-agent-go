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
	"errors"
	"os"
	"os/exec"
)

func startPTY(
	cmd *exec.Cmd,
	hook func(*exec.Cmd) error,
) (*os.File, func() error, error) {
	_ = cmd
	_ = hook
	return nil, nil, errors.New("pty is not supported on windows")
}
