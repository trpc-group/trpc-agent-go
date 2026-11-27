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
	"context"
	"errors"
	"fmt"
)

// AuthMethodType identifies how an ACP authentication method is executed.
type AuthMethodType string

const (
	// AuthMethodTypeAgent delegates authentication to the remote ACP agent.
	AuthMethodTypeAgent AuthMethodType = "agent"
	// AuthMethodTypeTerminal runs a terminal-based authentication flow.
	AuthMethodTypeTerminal AuthMethodType = "terminal"
)

// AuthMethod describes one authentication method advertised by an ACP agent.
type AuthMethod struct {
	// ID is the protocol authentication method identifier.
	ID string
	// Name is the human-readable authentication method name.
	Name string
	// Description is the optional authentication method description.
	Description string
	// Type identifies how the method is executed.
	Type AuthMethodType
}

// AuthChallenge describes an authentication selection required by an ACP operation.
type AuthChallenge struct {
	// Operation is the ACP operation that requires authentication.
	Operation string
	// Methods contains the authentication methods advertised by the agent.
	Methods []AuthMethod
}

// AuthSelection identifies the authentication method selected by an application.
type AuthSelection struct {
	// MethodID is the advertised authentication method identifier.
	MethodID string
	// Meta contains method-specific opaque parameters for authenticate.
	Meta map[string]any
}

// AuthMethodSelector selects an advertised authentication method for an ACP operation.
type AuthMethodSelector interface {
	// SelectAuthMethod selects one method from the challenge.
	SelectAuthMethod(ctx context.Context, challenge AuthChallenge) (AuthSelection, error)
}

// AuthMethodSelectorFunc adapts a function into an AuthMethodSelector.
type AuthMethodSelectorFunc func(
	ctx context.Context,
	challenge AuthChallenge,
) (AuthSelection, error)

// SelectAuthMethod implements AuthMethodSelector.
func (f AuthMethodSelectorFunc) SelectAuthMethod(
	ctx context.Context,
	challenge AuthChallenge,
) (AuthSelection, error) {
	if f == nil {
		return AuthSelection{}, errors.New("acpagent: auth method selector is nil")
	}
	return f(ctx, challenge)
}

// AuthRequiredError indicates that an ACP operation requires authentication.
type AuthRequiredError struct {
	// Operation is the ACP operation that returned auth_required.
	Operation string
	// Methods contains the authentication methods advertised by the agent.
	Methods []AuthMethod
	// Data preserves the structured JSON-RPC error data.
	Data any
	// Err preserves the underlying ACP request error.
	Err error
}

// Error implements error.
func (e *AuthRequiredError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Operation == "" {
		return "acpagent: authentication required"
	}
	return fmt.Sprintf("acpagent: authentication required for %s", e.Operation)
}

// Unwrap returns the underlying ACP request error.
func (e *AuthRequiredError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// TerminalAuthRequest describes one terminal authentication invocation.
type TerminalAuthRequest struct {
	// Method is the selected terminal authentication method.
	Method AuthMethod
	// Command is the executable to run for terminal authentication.
	Command string
	// Args are passed to Command without shell expansion.
	Args []string
	// Env contains the merged environment for the login process.
	Env []string
	// WorkDir is the absolute login process working directory.
	WorkDir string
	// Meta contains method-specific opaque authentication parameters.
	Meta map[string]any
}

// TerminalAuthHandler runs a terminal authentication flow.
type TerminalAuthHandler interface {
	// RunTerminalAuth runs the requested flow and returns nil only when login succeeds.
	RunTerminalAuth(ctx context.Context, request TerminalAuthRequest) error
}
