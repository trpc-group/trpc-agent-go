//go:build windows

// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		taskkill := filepath.Join(root, "System32", "taskkill.exe")
		taskkillCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := exec.CommandContext( //nolint:gosec
			taskkillCtx,
			taskkill, "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid),
		).Run()
		if err == nil {
			return nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return os.ErrProcessDone
		}
		killErr := cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			return os.ErrProcessDone
		}
		if killErr != nil {
			return fmt.Errorf("taskkill failed: %v; kill root process: %w", err, killErr)
		}
		return fmt.Errorf("taskkill failed; root process killed: %w", err)
	}
}

func disableProcessGroup(cmd *exec.Cmd) {}
