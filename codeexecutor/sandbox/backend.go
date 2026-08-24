//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"os/exec"
)

// BackendType selects the OS sandbox backend.
type BackendType string

const (
	// BackendAuto selects the native backend for the current platform.
	BackendAuto BackendType = "auto"
	// BackendLinuxBubblewrap uses bubblewrap on Linux.
	BackendLinuxBubblewrap BackendType = "linux-bubblewrap"
	// BackendMacOSSandboxExec uses sandbox-exec on macOS.
	BackendMacOSSandboxExec BackendType = "macos-sandbox-exec"
)

type commandCleanup func()

// releaseCmdExtraFiles closes the parent copies of Cmd.ExtraFiles after Start.
// os/exec does not close them; the child already inherited duplicates, so
// holding the parent FDs until Wait only pins descriptors for the process
// lifetime (seccomp memfd and deny-read bind-data on Linux).
func releaseCmdExtraFiles(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	for i, f := range cmd.ExtraFiles {
		if f == nil {
			continue
		}
		_ = f.Close()
		cmd.ExtraFiles[i] = nil
	}
}

// backendCapabilitiesInfo reports backend support above the generic engine
// capabilities exposed by codeexecutor.Engine.
type backendCapabilitiesInfo struct {
	OSSandbox          bool
	PTY                bool
	Stdin              bool
	NetworkIsolation   bool
	DenyReadGlob       bool
	Snapshot           bool
	Ports              bool
	ExternalPathGrants bool
	ProtectedPathMasks bool
	PerCommandGrants   bool
}
