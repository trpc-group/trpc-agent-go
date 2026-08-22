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
	"time"
)

// preparePipeCommand takes the detach disposition for parity with the Unix
// build. Windows has no controlling-terminal device a child can open behind the
// standard handles, so there is nothing to detach from.
func preparePipeCommand(_ *exec.Cmd, _ bool) {}

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
