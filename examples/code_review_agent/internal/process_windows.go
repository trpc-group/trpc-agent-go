//go:build windows

// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package internal

import (
	"os/exec"
	"strconv"
)

func prepareProcessTree(*exec.Cmd) {}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	if err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
