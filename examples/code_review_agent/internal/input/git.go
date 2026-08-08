//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// CommandRunner executes one command directly with a fixed argument vector.
type CommandRunner interface {
	Run(
		ctx context.Context,
		name string,
		args []string,
		stdin io.Reader,
		stdout io.Writer,
	) error
}

// ExecCommandRunner executes commands directly without a shell.
type ExecCommandRunner struct{}

// Run executes name with args, writes stdout, and honors context cancellation.
func (ExecCommandRunner) Run(
	ctx context.Context,
	name string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	if ctx == nil {
		return errors.New("run command: nil context")
	}
	if name == "" {
		return errors.New("run command: empty name")
	}
	if stdout == nil {
		return errors.New("run command: nil stdout")
	}
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	command.Stdin = stdin
	command.Stdout = stdout
	err := command.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return &commandExitError{code: exitError.ExitCode(), err: err}
	}
	return err
}

type commandExitError struct {
	code int
	err  error
}

func (e *commandExitError) Error() string {
	return fmt.Sprintf("command exited with code %d: %v", e.code, e.err)
}

func (e *commandExitError) Unwrap() error {
	return e.err
}

func isCommandExitCode(err error, codes ...int) bool {
	var exitError *commandExitError
	if !errors.As(err, &exitError) {
		return false
	}
	for _, code := range codes {
		if exitError.code == code {
			return true
		}
	}
	return false
}
