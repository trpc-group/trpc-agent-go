//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import "context"

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
