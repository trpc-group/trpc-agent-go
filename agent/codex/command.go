//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codex provides an agent that invokes a locally installed Codex CLI.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
)

// command describes one CLI invocation.
type command struct {
	// Bin is the executable path.
	bin string
	// Args are the CLI arguments excluding argv[0].
	args []string
	// Stdin is the data written to the process standard input.
	stdin []byte
	// Env is the process environment in KEY=VALUE form.
	env []string
	// Dir is the working directory for the command.
	dir string
}

// commandOutputHandler observes one stdout JSONL line as soon as it is available.
type commandOutputHandler func(line []byte) error

// commandResult contains captured process output and execution errors.
type commandResult struct {
	// Stdout is the complete stdout captured from the process.
	stdout []byte
	// Stderr is the complete stderr captured from the process.
	stderr []byte
	// RunErr is the process execution error.
	runErr error
	// OutputErr is the stdout observer error.
	outputErr error
}

// err returns all errors associated with the command execution.
func (r commandResult) err() error {
	return errors.Join(r.outputErr, r.runErr)
}

// commandRunner executes external commands and captures stdout/stderr output.
type commandRunner interface {
	// Run executes cmd and optionally streams stdout JSONL lines to onStdoutLine.
	Run(ctx context.Context, cmd command, onStdoutLine commandOutputHandler) commandResult
}

// execCommandRunner executes commands via os/exec.
type execCommandRunner struct{}

// Run executes cmd with exec.CommandContext and captures stdout/stderr.
func (execCommandRunner) Run(ctx context.Context, cmd command, onStdoutLine commandOutputHandler) commandResult {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command
	// This agent intentionally executes a locally installed Codex CLI binary.
	c := exec.CommandContext(ctx, cmd.bin, cmd.args...) //nolint:gosec // The executable path is provided via agent options.
	env := os.Environ()
	if len(cmd.env) > 0 {
		env = append(env, cmd.env...)
	}
	c.Env = env
	c.Dir = cmd.dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if len(cmd.stdin) > 0 {
		c.Stdin = bytes.NewReader(cmd.stdin)
	}
	c.Stderr = &stderr
	if onStdoutLine == nil {
		c.Stdout = &stdout
		runErr := c.Run()
		return commandResult{
			stdout: stdout.Bytes(),
			stderr: stderr.Bytes(),
			runErr: runErr,
		}
	}
	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		return commandResult{runErr: err}
	}
	if err := c.Start(); err != nil {
		return commandResult{runErr: err}
	}
	outputErr := readStdoutLines(stdoutPipe, &stdout, onStdoutLine)
	if outputErr != nil && c.Process != nil {
		_ = c.Process.Kill()
	}
	runErr := c.Wait()
	return commandResult{
		stdout:    stdout.Bytes(),
		stderr:    stderr.Bytes(),
		runErr:    runErr,
		outputErr: outputErr,
	}
}

// readStdoutLines captures stdout and forwards each complete JSONL record.
func readStdoutLines(r io.Reader, capture *bytes.Buffer, onLine commandOutputHandler) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, writeErr := capture.Write(line); writeErr != nil {
				return writeErr
			}
			if onLine != nil {
				if handleErr := onLine(line); handleErr != nil {
					return handleErr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
