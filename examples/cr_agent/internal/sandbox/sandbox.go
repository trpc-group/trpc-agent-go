//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sandbox provides a permission policy and a local command
// executor that enforces it, acting as the CR agent's controlled
// execution surface for static checks and test runs.
//
// The policy is deny-by-default: only commands on an explicit
// allowlist execute. Commands on the denylist are always blocked
// regardless of allowlist membership. The executor captures stdout,
// stderr, exit code, timing, and truncation so callers can persist
// a full audit trail.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Decision is the outcome of a permission check.
type Decision string

const (
	// DecisionAllow means the command is permitted.
	DecisionAllow Decision = "allow"

	// DecisionDeny means the command is blocked by policy.
	DecisionDeny Decision = "deny"
)

// PermissionPolicy decides whether a command may execute.
//
// The policy is intentionally simple for the example: an allowlist of
// command prefixes and a denylist of dangerous commands. Production
// deployments would integrate with the framework's
// codeexecutor.PermissionProfile for sandbox-level isolation.
type PermissionPolicy struct {
	allowedCmds map[string]bool
	deniedCmds  map[string]bool
}

// NewDefaultPermissionPolicy returns a policy that allows the safe
// set of commands the CR agent needs (go, cat, grep, etc.) and denies
// dangerous ones (rm -rf, curl, wget, etc.).
func NewDefaultPermissionPolicy() *PermissionPolicy {
	p := &PermissionPolicy{
		allowedCmds: make(map[string]bool),
		deniedCmds:  make(map[string]bool),
	}
	// Allowlist: commands the CR agent legitimately needs.
	for _, cmd := range []string{
		"go", "cat", "echo", "grep", "find", "ls",
		"head", "tail", "wc", "diff", "git",
	} {
		p.allowedCmds[cmd] = true
	}
	// Denylist: commands that could damage the host or exfiltrate data.
	for _, cmd := range []string{
		"rm", "rmdir", "del", "format", "mkfs",
		"curl", "wget", "scp", "rsync", "ssh",
		"chmod", "chown", "kill", "killall",
		"sudo", "su", "eval", "exec", "source",
	} {
		p.deniedCmds[cmd] = true
	}
	return p
}

// Check evaluates whether the given command string is permitted.
func (p *PermissionPolicy) Check(command string) Decision {
	cmdName := strings.TrimSpace(command)
	// Extract the command name (first token).
	if idx := strings.IndexAny(cmdName, " \t"); idx > 0 {
		cmdName = cmdName[:idx]
	}
	// Strip path prefix.
	if idx := strings.LastIndex(cmdName, "/"); idx >= 0 {
		cmdName = cmdName[idx+1:]
	}
	if idx := strings.LastIndex(cmdName, `\`); idx >= 0 {
		cmdName = cmdName[idx+1:]
	}
	if cmdName == "" {
		return DecisionDeny
	}
	// Denylist takes precedence.
	if p.deniedCmds[cmdName] {
		return DecisionDeny
	}
	// Check for rm -rf patterns specifically.
	if cmdName == "rm" && strings.Contains(command, "-rf") {
		return DecisionDeny
	}
	if p.allowedCmds[cmdName] {
		return DecisionAllow
	}
	return DecisionDeny
}

// Allow adds a command to the allowlist.
func (p *PermissionPolicy) Allow(cmd string) { p.allowedCmds[cmd] = true }

// Deny adds a command to the denylist.
func (p *PermissionPolicy) Deny(cmd string) { p.deniedCmds[cmd] = true }

// ExecResult captures the full output and metadata of a sandbox run.
type ExecResult struct {
	// Command is the logical command that was executed.
	Command string

	// Stdout is the raw stdout, truncated to OutputLimit.
	Stdout string

	// Stderr is the raw stderr, truncated to OutputLimit.
	Stderr string

	// ExitCode is the process exit status; -1 when the command was
	// denied or timed out before producing an exit.
	ExitCode int

	// Duration is the wall-clock execution time.
	Duration time.Duration

	// TimedOut reports whether the command was killed by the
	// timeout.
	TimedOut bool

	// Denied reports whether the command was blocked by policy
	// (never executed).
	Denied bool

	// OutputBytes counts total bytes produced (stdout + stderr)
	// before truncation.
	OutputBytes int
}

// Config controls the sandbox executor's runtime constraints.
type Config struct {
	// Timeout is the maximum wall-clock time a command may run.
	// Defaults to 60 seconds.
	Timeout time.Duration

	// OutputLimit caps the captured stdout/stderr to this many bytes
	// each. Defaults to 64 KB.
	OutputLimit int

	// EnvAllowlist is the set of environment variables that are
	// forwarded to the child process. All others are stripped.
	EnvAllowlist []string
}

// DefaultConfig returns a Config with safe defaults.
func DefaultConfig() Config {
	return Config{
		Timeout:     60 * time.Second,
		OutputLimit: 64 * 1024,
		EnvAllowlist: []string{
			"PATH", "HOME", "USER", "GOROOT", "GOPATH",
			"GOCACHE", "GOMODCACHE", "LANG", "LC_ALL",
		},
	}
}

// Executor runs commands subject to a PermissionPolicy and Config.
type Executor struct {
	policy *PermissionPolicy
	config Config
}

// NewExecutor creates a sandbox Executor with the given policy and
// config. If policy is nil, the default policy is used. If config
// is the zero value, DefaultConfig is used.
func NewExecutor(policy *PermissionPolicy, config Config) *Executor {
	if policy == nil {
		policy = NewDefaultPermissionPolicy()
	}
	if config.Timeout == 0 {
		config = DefaultConfig()
	}
	if config.OutputLimit == 0 {
		if config.Timeout == 0 {
			config = DefaultConfig()
		} else {
			config.OutputLimit = 64 * 1024
		}
	}
	return &Executor{policy: policy, config: config}
}

// Run executes a command in the sandbox.
//
// If the command is denied by policy, Run returns immediately with
// Denied=true and ExitCode=-1. Otherwise, the command is executed
// with a timeout and the output is captured and truncated.
func (e *Executor) Run(ctx context.Context, command string, workDir string) ExecResult {
	decision := e.policy.Check(command)
	if decision == DecisionDeny {
		return ExecResult{
			Command:  command,
			ExitCode: -1,
			Denied:   true,
		}
	}

	parts := parseCommand(command)
	if len(parts) == 0 {
		return ExecResult{Command: command, ExitCode: -1, Denied: true}
	}

	cmdCtx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, parts[0], parts[1:]...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = e.filterEnv()

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	result := ExecResult{
		Command:  command,
		Duration: duration,
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if cmdCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.ExitCode = -1
	}

	stdoutStr := stdoutBuf.String()
	stderrStr := stderrBuf.String()
	result.OutputBytes = len(stdoutStr) + len(stderrStr)
	result.Stdout = truncate(stdoutStr, e.config.OutputLimit)
	result.Stderr = truncate(stderrStr, e.config.OutputLimit)

	_ = err // error is reflected in ExitCode

	return result
}

// filterEnv returns only the environment variables in the allowlist.
func (e *Executor) filterEnv() []string {
	var env []string
	for _, key := range e.config.EnvAllowlist {
		if val, ok := lookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	return env
}

// truncate cuts s to at most maxBytes, appending a truncation marker
// if truncation occurred.
func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n...[truncated]"
}

// parseCommand splits a command string into program and arguments.
// It handles simple quoting with double quotes. It does not support
// shell operators, pipelines, or variable expansion — the sandbox
// executes single commands only.
func parseCommand(cmd string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	for _, ch := range cmd {
		switch {
		case ch == '"':
			inQuotes = !inQuotes
		case (ch == ' ' || ch == '\t') && !inQuotes:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// lookupEnv wraps os.LookupEnv so it can be overridden in tests.
var lookupEnv = os.LookupEnv

// DenialCount counts how many commands were denied by the policy
// across a set of ExecResults.
func DenialCount(results []ExecResult) int {
	count := 0
	for _, r := range results {
		if r.Denied {
			count++
		}
	}
	return count
}

// FormatDeniedMessage returns a human-readable string explaining why
// a command was denied.
func FormatDeniedMessage(command string) string {
	return fmt.Sprintf("command '%s' blocked by permission policy", command)
}
