//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sandbox contains runtime adapters for deterministic and isolated checks.
package sandbox

import (
	"context"
	"strings"
	"time"
)

const (
	// OutcomeSuccess means the command completed successfully.
	OutcomeSuccess = "success"
	// OutcomeNonZero means the command returned a non-zero exit status.
	OutcomeNonZero = "nonzero"
	// OutcomeTimeout means the command exceeded its deadline.
	OutcomeTimeout = "timeout"
	// OutcomeDependencyUnavailable means an optional checker is not installed.
	OutcomeDependencyUnavailable = "dependency_unavailable"
)

// Snapshot is the immutable workspace input.
type Snapshot struct {
	Path         string
	Digest       string
	SkillPath    string
	SkillDigest  string
	ScriptDigest string
}

// Command is a fixed bundled-script command.
type Command struct {
	ID             string
	Args           []string
	Cwd            string
	Timeout        time.Duration
	MaxStdoutBytes int
	MaxStderrBytes int
}

// Result captures one sandbox run.
type Result struct {
	CommandID        string `json:"command_id"`
	Stdout           string `json:"stdout"`
	Stderr           string `json:"stderr"`
	ExitCode         int    `json:"exit_code"`
	TimedOut         bool   `json:"timed_out"`
	Truncated        bool   `json:"truncated"`
	TruncationReason string `json:"truncation_reason"`
	DurationMS       int64  `json:"duration_ms"`
	Outcome          string `json:"outcome"`
}

func classifyResult(r Result) Result {
	r = classifyOutputMarkers(r)
	switch {
	case r.TimedOut:
		r.Outcome = OutcomeTimeout
	case r.CommandID == "staticcheck" && r.ExitCode == 3 && strings.Contains(r.Stderr, "dependency_unavailable: staticcheck"):
		r.Outcome = OutcomeDependencyUnavailable
	case r.ExitCode != 0:
		r.Outcome = OutcomeNonZero
	default:
		r.Outcome = OutcomeSuccess
	}
	return r
}

func classifyOutputMarkers(r Result) Result {
	stdout := strings.Contains(r.Stdout, "output_truncated: stdout_limit")
	stderr := strings.Contains(r.Stderr, "output_truncated: stderr_limit")
	if !stdout && !stderr {
		return r
	}
	r.Truncated = true
	switch {
	case stdout && stderr:
		r.TruncationReason = "stdout_stderr_limit"
	case stdout:
		r.TruncationReason = "stdout_limit"
	case stderr:
		r.TruncationReason = "stderr_limit"
	}
	return r
}

// Runtime stages a snapshot and runs fixed commands.
type Runtime interface {
	Stage(context.Context, Snapshot) error
	Run(context.Context, Command) (Result, error)
	Cleanup(context.Context) error
	Close() error
}

func truncateResult(r Result, stdoutLimit, stderrLimit int) Result {
	stdoutTruncated := false
	stderrTruncated := false
	if stdoutLimit > 0 && len(r.Stdout) > stdoutLimit {
		r.Stdout = r.Stdout[:stdoutLimit]
		r.Truncated = true
		stdoutTruncated = true
	}
	if stderrLimit > 0 && len(r.Stderr) > stderrLimit {
		r.Stderr = r.Stderr[:stderrLimit]
		r.Truncated = true
		stderrTruncated = true
	}
	switch {
	case stdoutTruncated && stderrTruncated:
		r.TruncationReason = "stdout_stderr_limit"
	case stdoutTruncated:
		r.TruncationReason = "stdout_limit"
	case stderrTruncated:
		r.TruncationReason = "stderr_limit"
	}
	return r
}

func cleanOutput(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}
