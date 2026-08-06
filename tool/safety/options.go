//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"io"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

type guardConfig struct {
	policyFile  string
	policy      *toolsafety.SafetyPolicy
	auditFile   string
	auditWriter io.Writer
	backend     string
}

func defaultGuardConfig() guardConfig {
	return guardConfig{
		backend: "workspaceexec",
	}
}

// Option configures a SafetyGuard.
type Option func(*guardConfig)

// WithPolicyFile loads the safety policy from a YAML or JSON file.
// The format is auto-detected from the extension (.yaml, .yml, .json).
func WithPolicyFile(path string) Option {
	return func(c *guardConfig) {
		c.policyFile = path
	}
}

// WithPolicy uses an in-memory SafetyPolicy. This is useful when the
// policy is constructed programmatically or loaded from a non-file
// source.
func WithPolicy(policy *toolsafety.SafetyPolicy) Option {
	return func(c *guardConfig) {
		c.policy = policy
	}
}

// WithAuditFile appends structured audit events in JSONL format to
// the specified file. The file is created if it does not exist.
func WithAuditFile(path string) Option {
	return func(c *guardConfig) {
		c.auditFile = path
	}
}

// WithAuditWriter sends audit events to the given writer.
func WithAuditWriter(w io.Writer) Option {
	return func(c *guardConfig) {
		c.auditWriter = w
	}
}

// WithBackend sets the default backend label used when a tool's
// argument schema doesn't clearly identify the backend. Typical
// values: "workspaceexec", "hostexec", "codeexec".
func WithBackend(backend string) Option {
	return func(c *guardConfig) {
		c.backend = backend
	}
}
