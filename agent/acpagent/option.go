//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"io"
	"time"
)

// Option configures an ACPAgent.
type Option func(*options)

type options struct {
	description         string
	command             string
	args                []string
	env                 []string
	workDir             string
	idleTimeout         time.Duration
	authMethodSelector  AuthMethodSelector
	terminalAuthHandler TerminalAuthHandler
	stderr              io.Writer
}

var defaultOptions = options{
	stderr: platformStderrWriter{},
}

// WithDescription sets the agent description reported to the framework.
func WithDescription(description string) Option {
	return func(options *options) {
		options.description = description
	}
}

// WithCommand sets the ACP agent executable.
func WithCommand(command string) Option {
	return func(options *options) {
		options.command = command
	}
}

// WithArgs appends arguments passed to the ACP agent executable.
func WithArgs(args ...string) Option {
	return func(options *options) {
		options.args = append(options.args, args...)
	}
}

// WithEnv appends process environment entries in KEY=VALUE form.
func WithEnv(env ...string) Option {
	return func(options *options) {
		options.env = append(options.env, env...)
	}
}

// WithWorkDir sets the absolute working directory for the ACP agent process.
func WithWorkDir(workDir string) Option {
	return func(options *options) {
		options.workDir = workDir
	}
}

// WithIdleTimeout sets the shared process idle recycling timeout.
//
// Zero disables idle recycling.
func WithIdleTimeout(timeout time.Duration) Option {
	return func(options *options) {
		options.idleTimeout = timeout
	}
}

// WithAuthMethodSelector sets the selector used to choose authentication methods.
//
// A nil selector leaves auth_required errors available to the caller as
// AuthRequiredError without attempting authentication.
func WithAuthMethodSelector(selector AuthMethodSelector) Option {
	return func(options *options) {
		options.authMethodSelector = selector
	}
}

// WithAuthMethodSelectorFunc adapts fn into an authentication method selector.
func WithAuthMethodSelectorFunc(selector AuthMethodSelectorFunc) Option {
	return func(options *options) {
		if selector == nil {
			options.authMethodSelector = nil
			return
		}
		options.authMethodSelector = selector
	}
}

// WithTerminalAuthHandler sets the handler for terminal authentication.
//
// The handler owns the interactive terminal or application UI used for login.
// A nil handler disables terminal authentication capability advertisement.
func WithTerminalAuthHandler(handler TerminalAuthHandler) Option {
	return func(options *options) {
		options.terminalAuthHandler = handler
	}
}

// WithStderrWriter sets the destination for the ACP agent's standard error.
//
// By default, standard error is written line by line to the framework log at
// info level. A non-nil writer replaces that default and receives the raw
// process output. A nil writer disables standard error forwarding.
//
// The framework does not close or flush the writer. The writer may be called
// from an internal goroutine while the process is running and should not block
// indefinitely.
func WithStderrWriter(writer io.Writer) Option {
	return func(options *options) {
		options.stderr = writer
	}
}
