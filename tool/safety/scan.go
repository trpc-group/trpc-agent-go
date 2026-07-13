// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

// ScanInput is the data the Safety Guard needs to inspect a tool call.
// It is designed to be backend-agnostic: callers that use shellsafe should
// populate ShellCommand with the Stage 2A adapter result. Callers that use a
// different parser (PowerShell, Python, etc.) can leave ShellCommand nil and
// rely on raw Command and Args fields.
//
// [shellsafe.Pipeline]: ../../internal/shellsafe/parser.go
type ScanInput struct {
	// ToolName is the model-visible name of the tool being inspected.
	// It corresponds to [Report.ToolName] in the output.
	ToolName string `json:"tool_name"`

	// Backend identifies the execution backend, e.g. "shellsafe",
	// "powershell", "codeexec". It corresponds to [Report.Backend].
	Backend string `json:"backend"`

	// Command is the raw command string as received from the model
	// or the tool adapter. It is the primary input for backends that
	// perform their own parsing.
	Command string `json:"command"`

	// ShellCommand is the trusted structured command view produced by
	// AdaptShellCommand. Shell rules should prefer this field over raw text.
	// When absent, Scanner derives one from Command with the fail-closed
	// shellsafe adapter.
	ShellCommand *ShellCommandView `json:"shell_command,omitempty"`

	// HostExec mirrors the canonical hostexec exec_command request fields that
	// affect session lifetime. It is meaningful only when Backend is exactly
	// "hostexec"; Scanner never treats tool names as a backend identity.
	//
	// This metadata deliberately contains no cleanup result or isolation claim:
	// those are enforced by the hostexec integration, not static scanning.
	HostExec *HostExecRequest `json:"hostexec,omitempty"`

	// ParsedCommands is the legacy pre-parsed pipeline representation, compatible
	// with [internal/shellsafe.Pipeline.Commands] (a [][]string where
	// each inner slice is one argv). When non-empty, the scanner can
	// skip re-parsing and operate directly on the structured form.
	//
	// New shellsafe callers should use ShellCommand instead. This field remains
	// for compatibility with callers that already hold a Pipeline:
	//
	//	pipe, err := shellsafe.Parse(cmd)
	//	if err != nil { ... }
	//	input := ScanInput{
	//	    Command:        cmd,
	//	    ParsedCommands: pipe.Commands,
	//	}
	ParsedCommands [][]string `json:"parsed_commands,omitempty"`

	// Args is the raw argument list for non-shell tool calls (e.g.
	// a function-call style tool that passes argv directly without
	// a shell). It is used when Command is empty and ParsedCommands
	// is nil.
	Args []string `json:"args,omitempty"`

	// WorkDir is the working directory the tool would execute in.
	// The guard uses it to resolve relative paths in ForbiddenPaths
	// checks.
	WorkDir string `json:"work_dir,omitempty"`

	// Env is the environment variable map the tool would receive.
	// The guard inspects it against EnvWhitelist.
	Env map[string]string `json:"env,omitempty"`

	// NetworkAccess indicates whether the tool may initiate network
	// connections. When true, Scanner requires a verifiable destination even
	// if it could not infer one from a structured network-client command.
	NetworkAccess bool `json:"network_access,omitempty"`

	// NetworkDestinations lists destinations the execution adapter intends to
	// contact. Each entry may be a host, host:port, IP address, or URL. It is
	// metadata for static analysis only; Scanner never opens a connection.
	NetworkDestinations []string `json:"network_destinations,omitempty"`
}

// HostExecRequest is the safety-relevant subset of tool/hostexec exec_command
// input. It mirrors its real background, tty/pty aliases, yield_time_ms, and
// timeout_sec controls without adding execution behavior.
// A nil pointer means the corresponding optional hostexec field was absent.
type HostExecRequest struct {
	Background  bool  `json:"background,omitempty"`
	TTY         *bool `json:"tty,omitempty"`
	PTY         *bool `json:"pty,omitempty"`
	YieldTimeMS *int  `json:"yield_time_ms,omitempty"`
	TimeoutSec  *int  `json:"timeout_sec,omitempty"`
}
