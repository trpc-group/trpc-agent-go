//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"strings"
)

// Decision represents the outcome of a permission check.
type Decision string

const (
	DecisionAllow            Decision = "allow"
	DecisionDeny             Decision = "deny"
	DecisionAsk              Decision = "ask"
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

// String returns the string representation of the decision.
func (d Decision) String() string { return string(d) }

// PermissionRecord captures a single permission decision for audit.
type PermissionRecord struct {
	ID        string
	TaskID    string
	Command   string
	Decision  Decision
	Reason    string
	Timestamp string
}

// PermissionPolicy decides whether a command is allowed to execute.
type PermissionPolicy struct {
	allowedCmds map[string]bool
	deniedCmds  map[string]bool
	reviewCmds  map[string]bool
}

// NewDefaultPermissionPolicy creates a policy with the default allow,
// deny, and review lists.
func NewDefaultPermissionPolicy() *PermissionPolicy {
	return &PermissionPolicy{
		allowedCmds: map[string]bool{
			"go":          true,
			"gofmt":       true,
			"goimports":   true,
			"staticcheck": true,
			"echo":        true,
			"cat":         true,
			"ls":          true,
			"grep":        true,
			"git":         true, // read-only git operations
			"bash":        true,
			"diff":        true,
			"wc":          true,
			"head":        true,
			"tail":        true,
		},
		deniedCmds: map[string]bool{
			"rm":       true,
			"rmdir":    true,
			"curl":     true,
			"wget":     true,
			"chmod":    true,
			"chown":    true,
			"mkfs":     true,
			"dd":       true,
			"kill":     true,
			"pkill":    true,
			"shutdown": true,
			"reboot":   true,
		},
		reviewCmds: map[string]bool{
			"docker":   true,
			"npm":      true,
			"pip":      true,
			"apt":      true,
			"brew":     true,
			"git-push": true,
			"make":     true,
		},
	}
}

// Decide evaluates whether the given command is allowed to execute
// in the sandbox. It returns a Decision and a reason string.
func (p *PermissionPolicy) Decide(cmd string) (Decision, string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return DecisionDeny, "empty command"
	}

	// Extract the base command (first token).
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return DecisionDeny, "could not parse command"
	}
	base := parts[0]

	// Strip path prefix: /usr/bin/go → go
	base = strings.ReplaceAll(base, "\\", "/")
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}

	// Check denied commands first (deny takes precedence).
	if p.deniedCmds[base] {
		return DecisionDeny,
			"command '" + base + "' is in the denied list (high risk)"
	}

	// Shell command strings bypass the base-command policy because the actual
	// executable appears inside the -c payload. Require review before allowing
	// any shell interpreter to evaluate such a string.
	if (base == "bash" || base == "sh" || base == "zsh") &&
		len(parts) > 1 && parts[1] == "-c" {
		return DecisionNeedsHumanReview,
			"shell -c evaluates a command string and needs human review"
	}

	// Check for specific dangerous git subcommands.
	if base == "git" && len(parts) > 1 {
		sub := parts[1]
		switch sub {
		case "push", "reset", "clean", "checkout", "rebase", "merge":
			return DecisionNeedsHumanReview,
				"git " + sub + " modifies repository state and needs review"
		}
	}

	// Check review commands.
	if p.reviewCmds[base] {
		return DecisionNeedsHumanReview,
			"command '" + base + "' requires human review"
	}

	// Check for shell metacharacters that could bypass restrictions.
	if strings.ContainsAny(cmd, "|&;`$()<>") {
		// Allow simple pipe only if both sides are in allowed list.
		return DecisionAsk,
			"command contains shell metacharacters, needs review"
	}

	// Check allowed commands.
	if p.allowedCmds[base] {
		return DecisionAllow, ""
	}

	// Default: ask.
	return DecisionAsk,
		"command '" + base + "' is not in the allowed list"
}

// IsBlocked returns true if the decision prevents sandbox execution
// (deny, ask, or needs_human_review).
func IsBlocked(d Decision) bool {
	return d == DecisionDeny ||
		d == DecisionAsk ||
		d == DecisionNeedsHumanReview
}
