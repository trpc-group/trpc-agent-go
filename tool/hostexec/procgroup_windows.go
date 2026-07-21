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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func preparePipeCommand(_ *exec.Cmd) {}

func preparePTYCommand(_ *exec.Cmd) {}

func commandProcessGroupID(_ *exec.Cmd) int {
	return 0
}

func terminateProcessTree(
	_ context.Context,
	process *os.Process,
	_ int,
	grace time.Duration,
) error {
	if process == nil {
		return nil
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	const maxTaskkillDuration = 5 * time.Second
	taskkillTimeout := maxTaskkillDuration
	if grace > 0 && grace < taskkillTimeout {
		taskkillTimeout = grace
	}
	// Process cleanup must still run after the command context is canceled.
	taskkillCtx, cancel := context.WithTimeout(
		context.Background(),
		taskkillTimeout,
	)
	defer cancel()
	err := exec.CommandContext( //nolint:gosec
		taskkillCtx,
		filepath.Join(root, "System32", "taskkill.exe"),
		"/T", "/F", "/PID", strconv.Itoa(process.Pid),
	).Run()
	if err == nil {
		return nil
	}
	if killErr := killProcess(process); killErr != nil {
		return fmt.Errorf("terminate Windows process tree: %v; kill root: %w", err, killErr)
	}
	return fmt.Errorf("terminate Windows process tree: %w", err)
}
