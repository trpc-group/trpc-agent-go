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
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SandboxRunStatus represents the outcome of a sandbox execution.
type SandboxRunStatus string

const (
	SandboxStatusSuccess SandboxRunStatus = "success"
	SandboxStatusTimeout SandboxRunStatus = "timeout"
	SandboxStatusFailed  SandboxRunStatus = "failed"
	SandboxStatusBlocked SandboxRunStatus = "blocked"
	SandboxStatusError   SandboxRunStatus = "error"
)

// SandboxRun captures the result of a single sandbox execution.
type SandboxRun struct {
	ID                 string           `json:"id"`
	TaskID             string           `json:"task_id"`
	Command            string           `json:"command"`
	PermissionDecision Decision         `json:"permission_decision"`
	PermissionReason   string           `json:"permission_reason"`
	Status             SandboxRunStatus `json:"status"`
	Stdout             string           `json:"stdout"`
	Stderr             string           `json:"stderr"`
	ExitCode           int              `json:"exit_code"`
	Duration           time.Duration    `json:"duration"`
	TimedOut           bool             `json:"timed_out"`
	Error              string           `json:"error,omitempty"`
}

// SandboxConfig holds sandbox execution parameters.
type SandboxConfig struct {
	Timeout        time.Duration
	MaxOutputBytes int
	WorkDir        string
	AllowedEnvVars []string // whitelist of env vars to pass through
}

// DefaultSandboxConfig returns safe default sandbox settings.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Timeout:        30 * time.Second,
		MaxOutputBytes: 1 * 1024 * 1024, // 1MB
		WorkDir:        "",
		AllowedEnvVars: []string{"PATH", "HOME", "GOROOT", "GOPATH", "LANG"},
	}
}

// Sandbox executes commands in a controlled environment with
// timeout, output limits, and env var whitelisting.
type Sandbox struct {
	config SandboxConfig
}

// SandboxExecutor is implemented by isolated production runtimes and by the
// local development fallback.
type SandboxExecutor interface {
	Execute(context.Context, string, string, Decision, string) *SandboxRun
}

// NewSandbox creates a Sandbox with the given config.
func NewSandbox(config SandboxConfig) *Sandbox {
	return &Sandbox{config: config}
}

// NewDefaultSandbox creates a Sandbox with default settings.
func NewDefaultSandbox() *Sandbox {
	return NewSandbox(DefaultSandboxConfig())
}

// Execute runs a command in the sandbox. It never panics — all errors
// are captured in the returned SandboxRun.
func (s *Sandbox) Execute(
	ctx context.Context,
	taskID string,
	command string,
	decision Decision,
	reason string,
) *SandboxRun {
	run := &SandboxRun{
		ID:                 uuid.NewString(),
		TaskID:             taskID,
		Command:            command,
		PermissionDecision: decision,
		PermissionReason:   reason,
	}

	// If blocked by permission policy, do not execute.
	if IsBlocked(decision) {
		run.Status = SandboxStatusBlocked
		run.Error = "command blocked by permission policy: " + reason
		return run
	}

	start := time.Now()
	defer func() {
		run.Duration = time.Since(start)
	}()

	// Create timeout context.
	timeoutCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()

	parts := strings.Fields(command)
	if len(parts) == 0 {
		run.Status = SandboxStatusError
		run.Error = "empty command"
		return run
	}

	// #nosec G204 — command is validated by permission policy
	cmd := exec.CommandContext(timeoutCtx, parts[0], parts[1:]...)

	// Set working directory.
	if s.config.WorkDir != "" {
		cmd.Dir = s.config.WorkDir
	}

	// Build whitelisted environment.
	cmd.Env = s.buildEnv()

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()

	// Apply output size limits.
	run.Stdout = RedactSensitiveInfo(truncateOutput(stdoutBuf.String(), s.config.MaxOutputBytes))
	run.Stderr = RedactSensitiveInfo(truncateOutput(stderrBuf.String(), s.config.MaxOutputBytes))
	run.ExitCode = -1
	if cmd.ProcessState != nil {
		run.ExitCode = cmd.ProcessState.ExitCode()
	}

	if timeoutCtx.Err() == context.DeadlineExceeded {
		run.Status = SandboxStatusTimeout
		run.TimedOut = true
		run.Error = fmt.Sprintf("command timed out after %s", s.config.Timeout)
		return run
	}

	if err != nil {
		run.Status = SandboxStatusFailed
		run.Error = err.Error()
		return run
	}

	run.Status = SandboxStatusSuccess
	return run
}

// buildEnv returns a filtered environment containing only whitelisted
// variables.
func (s *Sandbox) buildEnv() []string {
	if len(s.config.AllowedEnvVars) == 0 {
		return os.Environ()
	}
	var env []string
	for _, key := range s.config.AllowedEnvVars {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	return env
}

func truncateOutput(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n... [output truncated at " +
		fmt.Sprintf("%d bytes]", maxBytes)
}
