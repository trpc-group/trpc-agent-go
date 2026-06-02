//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

// BackendType selects the OS sandbox backend.
type BackendType string

const (
	// BackendAuto selects the native backend for the current platform.
	BackendAuto BackendType = "auto"
	// BackendLinuxBubblewrap uses bubblewrap on Linux.
	BackendLinuxBubblewrap BackendType = "linux-bubblewrap"
)

type commandCleanup func()

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
