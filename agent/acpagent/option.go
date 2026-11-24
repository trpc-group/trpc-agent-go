//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import "io"

// Option configures an ACPAgent.
type Option func(*options)

type options struct {
	terminalAuthHandler TerminalAuthHandler
	stderr              io.Writer
}

// WithTerminalAuthHandler sets the required handler for terminal authentication.
//
// The handler owns the interactive terminal or application UI used for login.
func WithTerminalAuthHandler(handler TerminalAuthHandler) Option {
	return func(options *options) {
		options.terminalAuthHandler = handler
	}
}

// WithStderrWriter sets the destination for the ACP agent's standard error.
//
// A nil writer discards standard error.
func WithStderrWriter(writer io.Writer) Option {
	return func(options *options) {
		options.stderr = writer
	}
}
