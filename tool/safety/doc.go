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
// Shell commands are parsed as a whole by internal/shellsafe and then scanned
// per pipeline segment. Reports aggregate all findings and use the most severe
// decision/risk across the command. When configured to review shell pipelines,
// a multi-segment command adds an ask finding even if each segment is otherwise
// allowed.
//
// Scanner values clone their policy at construction and are safe for
// concurrent scans when the configured AuditSink is safe for concurrent writes.
// The built-in writer, file and recording sinks satisfy that contract. A nil
// *Scanner receiver behaves like NewScanner(DefaultPolicy()) without an audit
// sink and is intended only as a convenience fallback.
//
// The scanner is a static guard and does not replace sandboxing. It should be
// used together with workspace isolation, clean environments, process cleanup,
// output limits, artifact controls and network/filesystem restrictions. Use
// ScanOutput for execution output, logs or artifact text that need a
// post-execution secret or sensitive-path pass before persistence/export.
package safety
