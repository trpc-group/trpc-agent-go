//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"time"
)

// ToolProfile describes how to decode one tool's JSON arguments into a
// ScanInput. Profiles are declarative so the guard does not depend on
// unexported workspaceexec, hostexec, or codeexec types.
//
// Default profiles cover the canonical tool names: workspace_exec,
// exec_command, execute_code, write_stdin, kill_session,
// workspace_write_stdin, workspace_kill_session. Custom tools (including
// MCP tools that expose a command-shaped schema) must register a profile
// via WithToolProfile before the guard can decode them; otherwise the
// guard returns DecisionAsk for command-shaped unknown tools and
// DecisionAllow for tools with no recognized command surface.
type ToolProfile struct {
	// Name is the profile key, usually the tool name.
	Name string
	// Backend identifies the execution surface for audit and rules.
	Backend Backend
	// DefaultTimeout is applied when the request omits a timeout.
	DefaultTimeout time.Duration
	// Isolated reports whether the backend enforces filesystem isolation.
	Isolated bool
	// EnvironmentIsolated reports whether the backend filters env vars.
	EnvironmentIsolated bool
	// NetworkRestricted reports whether the backend restricts egress.
	NetworkRestricted bool
	// CommandField is the JSON key holding the shell command.
	CommandField string
	// CodeBlocksField is the JSON key holding the code blocks array.
	CodeBlocksField string
	// WorkingDirFields lists candidate JSON keys for the working directory.
	WorkingDirFields []string
	// TimeoutFields lists candidate JSON keys for the timeout in seconds.
	TimeoutFields []string
	// EnvironmentField is the JSON key for the env map.
	EnvironmentField string
	// BackgroundFields lists candidate JSON keys for the background flag.
	BackgroundFields []string
	// PTYFields lists candidate JSON keys for the PTY flag.
	PTYFields []string
}

// DefaultToolProfiles returns the profiles used when no custom profile is
// registered for a known tool name.
//
// The capability flags describe what the backend ENFORCES, not what the
// guard would like. The local workspace and local code executor provide
// a working directory but NOT a host filesystem sandbox or network
// boundary; their profiles therefore declare Isolated=true (the
// workspace is a separate working area) but NetworkRestricted=false
// (the executor can still reach the network unless the application
// configures a network namespace or proxy). Container and E2B backends
// should register custom profiles with NetworkRestricted=true when they
// enforce egress filtering.
func DefaultToolProfiles() []ToolProfile {
	return []ToolProfile{
		{
			Name:                "workspace_exec",
			Backend:             BackendWorkspaceExec,
			DefaultTimeout:      5 * time.Minute,
			Isolated:            true,
			EnvironmentIsolated: true,
			NetworkRestricted:   false,
			CommandField:        "command",
			WorkingDirFields:    []string{"cwd"},
			TimeoutFields:       []string{"timeout", "timeout_sec", "timeoutSec"},
			EnvironmentField:    "env",
			BackgroundFields:    []string{"background"},
			PTYFields:           []string{"tty", "pty"},
		},
		{
			Name:                "exec_command",
			Backend:             BackendHostExec,
			DefaultTimeout:      5 * time.Minute,
			Isolated:            false,
			EnvironmentIsolated: false,
			NetworkRestricted:   false,
			CommandField:        "command",
			WorkingDirFields:    []string{"workdir"},
			TimeoutFields:       []string{"timeout_sec", "timeoutSec"},
			EnvironmentField:    "env",
			BackgroundFields:    []string{"background"},
			PTYFields:           []string{"tty", "pty"},
		},
		{
			Name:                "execute_code",
			Backend:             BackendCodeExec,
			DefaultTimeout:      5 * time.Minute,
			Isolated:            true,
			EnvironmentIsolated: true,
			NetworkRestricted:   false,
			CodeBlocksField:     "code_blocks",
		},
		{
			Name:             "write_stdin",
			Backend:          BackendHostExec,
			DefaultTimeout:   30 * time.Second,
			CommandField:     "",
			WorkingDirFields: nil,
			EnvironmentField: "",
			BackgroundFields: nil,
			PTYFields:        nil,
		},
		{
			Name:           "kill_session",
			Backend:        BackendHostExec,
			DefaultTimeout: 5 * time.Second,
		},
		{
			Name:           "workspace_write_stdin",
			Backend:        BackendWorkspaceExec,
			DefaultTimeout: 30 * time.Second,
		},
		{
			Name:           "workspace_kill_session",
			Backend:        BackendWorkspaceExec,
			DefaultTimeout: 5 * time.Second,
		},
	}
}

// profileRegistry is a map keyed by profile Name.
type profileRegistry map[string]ToolProfile

// newProfileRegistry builds the default registry.
func newProfileRegistry() profileRegistry {
	reg := profileRegistry{}
	for _, p := range DefaultToolProfiles() {
		reg[p.Name] = p
	}
	return reg
}

// lookup returns the profile for name and whether one exists.
func (r profileRegistry) lookup(name string) (ToolProfile, bool) {
	p, ok := r[name]
	return p, ok
}

// register adds or replaces a profile.
func (r profileRegistry) register(p ToolProfile) {
	if p.Name == "" {
		return
	}
	r[p.Name] = p
}
