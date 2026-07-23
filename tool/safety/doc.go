//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety implements an opt-in pre-execution guard for command-like
// tools.
//
// The package scans pending workspace_exec, hostexec and codeexec requests
// before execution and returns allow, deny or ask decisions. It is designed to
// plug into the framework through tool.PermissionPolicy, so dangerous tool
// calls can be skipped before the underlying tool starts.
//
// The scanner is a static guard and does not replace sandboxing. It should be
// used together with workspace isolation, clean environments, process cleanup,
// output limits, artifact controls and network/filesystem restrictions. Use
// ScanOutput for execution output, logs or artifact text that need a
// post-execution secret or sensitive-path pass before persistence/export.
package safety
