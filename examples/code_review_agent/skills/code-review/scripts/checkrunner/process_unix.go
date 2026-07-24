//go:build unix

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
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func configureTargetProcess(cmd *exec.Cmd, uid uint32) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: &syscall.Credential{Uid: uid, Gid: uid, NoSetGroups: true}}
}

func killProcess(process *os.Process) error { return syscall.Kill(-process.Pid, syscall.SIGKILL) }

func makeTargetDirectories(paths []string, uid uint32) error {
	// #nosec G204 -- fixed executable; prepareTargetDirectories constrains paths to /tmp/cr-target.
	mkdir := exec.Command("/bin/mkdir", append([]string{"-p", "--"}, paths...)...)
	mkdir.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid, NoSetGroups: true}}
	if output, err := mkdir.CombinedOutput(); err != nil {
		return fmt.Errorf("create target directories: %w: %s", err, output)
	}
	// #nosec G204 -- fixed executable; prepareTargetDirectories constrains paths to /tmp/cr-target.
	chmod := exec.Command("/bin/chmod", append([]string{"0700", "--"}, paths...)...)
	chmod.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid, NoSetGroups: true}}
	if output, err := chmod.CombinedOutput(); err != nil {
		return fmt.Errorf("chmod target directories: %w: %s", err, output)
	}
	return nil
}
