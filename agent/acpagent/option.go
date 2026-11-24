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
	authMethodSelector  AuthMethodSelector
	terminalAuthHandler TerminalAuthHandler
	stderr              io.Writer
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
// A nil writer discards standard error.
func WithStderrWriter(writer io.Writer) Option {
	return func(options *options) {
		options.stderr = writer
	}
}
