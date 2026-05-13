//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import "time"

// AuditRecord is a small execution audit payload suitable for structured logs.
// It deliberately excludes secrets and full environment values.
type AuditRecord struct {
	Backend     BackendType
	SandboxType Enforcement
	SessionID   string
	PolicyID    string
	Cwd         string
	ExitCode    int
	Duration    time.Duration
	TimedOut    bool
	StdoutCut   bool
	StderrCut   bool
}
