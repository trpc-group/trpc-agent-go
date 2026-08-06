//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"os"
	"os/exec"
)

func configureTargetProcess(cmd *exec.Cmd, _ uint32) { cmd.SysProcAttr = nil }
func killProcess(process *os.Process) error          { return process.Kill() }

func makeTargetDirectories(paths []string, _ uint32) error {
	for _, path := range paths {
		if err := os.MkdirAll(path, privateDirectoryMode); err != nil {
			return err
		}
		if err := os.Chmod(path, privateDirectoryMode); err != nil {
			return err
		}
	}
	return nil
}
