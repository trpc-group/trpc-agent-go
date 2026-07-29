//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package governance enforces fail-closed authorization for sandbox execution.
package governance

// Decision is the result of a governance check.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
	Action  string `json:"action"` // "allow", "deny", "ask"
}

// Policy evaluates whether a command is allowed to execute in the sandbox.
type Policy struct {
	allowedCommands []string
	deniedCommands  []string
	dryRun          bool
}

// NewPolicy creates a governance policy.
func NewPolicy(allowedCommands, deniedCommands []string, dryRun bool) *Policy {
	return &Policy{
		allowedCommands: allowedCommands,
		deniedCommands:  deniedCommands,
		dryRun:          dryRun,
	}
}

// Check evaluates whether the given command is allowed.
func (p *Policy) Check(command string) Decision {
	// In dry-run mode, record the decision but always allow for testing.
	if p.dryRun {
		return Decision{Allowed: true, Action: "allow", Reason: "dry_run: governance recorded"}
	}

	// Check denied commands first (fail-closed).
	for _, denied := range p.deniedCommands {
		if command == denied {
			return Decision{Allowed: false, Action: "deny", Reason: "command is in deny list"}
		}
	}

	// If allowed list is set, command must be in it.
	if len(p.allowedCommands) > 0 {
		for _, allowed := range p.allowedCommands {
			if command == allowed {
				return Decision{Allowed: true, Action: "allow"}
			}
		}
		return Decision{Allowed: false, Action: "deny", Reason: "command not in allow list"}
	}

	return Decision{Allowed: true, Action: "allow"}
}

// DefaultAllowedCommands returns the safe command allowlist for the
// code review sandbox.
func DefaultAllowedCommands() []string {
	return []string{"go", "checkrunner"}
}

// DefaultDeniedCommands returns the command denylist for the
// code review sandbox.
func DefaultDeniedCommands() []string {
	return []string{"rm", "curl", "wget", "ssh", "eval", "sudo", "bash", "sh"}
}
