//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package claudecode provides an agent that invokes a locally installed Claude Code CLI.
package claudecode

import (
	"bytes"
	"context"
	"os"
	"os/exec"
)

// command describes one CLI invocation.
type command struct {
	// Bin is the executable path.
	bin string
	// Args are the CLI arguments excluding argv[0].
	args []string
	// Env is the process environment in KEY=VALUE form.
	env []string
	// Dir is the working directory for the command.
	dir string
}

// commandRunner executes external commands and captures stdout/stderr output.
type commandRunner interface {
	// Run executes cmd and returns captured stdout, stderr, and the process error.
	Run(ctx context.Context, cmd command) ([]byte, []byte, error)
}

// execCommandRunner executes commands via os/exec.
type execCommandRunner struct{}

// Run executes cmd with exec.CommandContext and captures stdout/stderr.
func (execCommandRunner) Run(ctx context.Context, cmd command) ([]byte, []byte, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command
	// This agent intentionally executes a locally installed Claude Code CLI binary.
	c := exec.CommandContext(ctx, cmd.bin, cmd.args...) //nolint:gosec // The executable path is provided via agent options.
	env := os.Environ()
	if len(cmd.env) > 0 {
		env = append(env, cmd.env...)
	}
	c.Env = env
	c.Dir = cmd.dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	runErr := c.Run()
	return stdout.Bytes(), stderr.Bytes(), runErr
}
