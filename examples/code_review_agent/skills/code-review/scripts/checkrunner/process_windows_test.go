//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os/exec"
	"testing"
)

func TestWindowsProcessControls(t *testing.T) {
	// helperCommand executes only this test binary with fixed arguments.
	//nolint:gosec
	configured := exec.Command(helperCommand("success")[0])
	configureTargetProcess(configured, targetUID)
	if configured.SysProcAttr != nil {
		t.Fatal("Windows target process has unexpected SysProcAttr")
	}
	commandArgs := helperCommand("timeout")
	// helperCommand executes only this test binary with fixed arguments.
	//nolint:gosec
	command := exec.Command(commandArgs[0], commandArgs[1:]...)
	if err := command.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := killProcess(command.Process); err != nil {
		t.Fatalf("killProcess() error = %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed process returned success")
	}
}
