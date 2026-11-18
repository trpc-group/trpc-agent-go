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
	"errors"
	"os"
	"os/exec"
)

func startPTY(
	ctx context.Context,
	cmd *exec.Cmd,
	hook PreStartHook,
) (*os.File, func() error, error) {
	_ = ctx
	_ = cmd
	_ = hook
	return nil, nil, errors.New("pty is not supported on windows")
}
