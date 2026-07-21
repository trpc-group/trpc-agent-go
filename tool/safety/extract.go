//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
)

// ExecRequest is a normalized representation of a tool execution request.
// It captures the fields relevant to safety scanning across different
// execution backends (workspaceexec, hostexec, codeexec).
type ExecRequest struct {
	// Command is the shell command to execute (workspaceexec/hostexec).
	Command string
	// Stdin is additional stdin content written before the command runs.
	Stdin string
	// CodeBlocks is the list of code blocks to execute (codeexec).
	CodeBlocks []string
	// Args are additional command-line arguments.
	Args []string
	// WorkDir is the working directory for the command.
	WorkDir string
	// Env is the environment variables for the command.
	Env map[string]string
	// Timeout is the requested execution timeout in seconds.
	Timeout int
	// Background reports whether the command runs in the background.
	Background bool
	// PTY reports whether a pseudo-terminal is requested.
	PTY bool
	// Backend identifies the execution backend (workspaceexec, hostexec, codeexec).
	Backend string
}

// ToScanInput converts an ExecRequest into a ScanInput for the scanner.
func (r ExecRequest) ToScanInput(toolName string) ScanInput {
	return ScanInput{
		Command:    r.Command,
		Stdin:      r.Stdin,
		CodeBlocks: r.CodeBlocks,
		Args:       r.Args,
		WorkDir:    r.WorkDir,
		Env:        r.Env,
		ToolName:   toolName,
		Backend:    r.Backend,
		Timeout:    r.Timeout,
		Background: r.Background,
		PTY:        r.PTY,
	}
}

// Extractor extracts an ExecRequest from raw tool arguments.
// The toolName identifies which tool is being called, and args is the
// JSON-encoded argument payload.
type Extractor func(toolName string, args []byte) (ExecRequest, error)

// defaultExtractors maps tool names to their extractors.
var defaultExtractors = map[string]Extractor{
	"workspace_exec":        extractWorkspaceExec,
	"workspace_write_stdin": extractUnsupportedStdinWrite,
	"exec_command":          extractHostExec,
	"write_stdin":           extractUnsupportedStdinWrite,
	"hostexec_write_stdin":  extractUnsupportedStdinWrite,
	"execute_code":          extractCodeExec,
}

// extractRequest finds the right extractor for the tool and returns an ExecRequest.
// If no extractor is registered for the tool, it returns a generic request
// with just the raw args as the command.
func extractRequest(toolName string, args []byte, extractors map[string]Extractor) (ExecRequest, error) {
	if ext, ok := extractors[toolName]; ok {
		return ext(toolName, args)
	}
	// No extractor registered; return a generic request with raw args as command.
	return ExecRequest{
		Command: string(args),
		Backend: "unknown",
	}, nil
}

// extractWorkspaceExec extracts an ExecRequest from workspace_exec arguments.
//
// Expected JSON shape:
//
//	{"command":"...","cwd":"...","env":{...},"timeout":30,"background":false,"tty":false,"pty":false}
func extractWorkspaceExec(toolName string, args []byte) (ExecRequest, error) {
	var in struct {
		Command    string            `json:"command"`
		Cwd        string            `json:"cwd,omitempty"`
		Env        map[string]string `json:"env,omitempty"`
		Stdin      string            `json:"stdin,omitempty"`
		Timeout    int               `json:"timeout,omitempty"`
		TimeoutSec *int              `json:"timeout_sec,omitempty"`
		TimeoutOld *int              `json:"timeoutSec,omitempty"`
		Background bool              `json:"background,omitempty"`
		TTY        *bool             `json:"tty,omitempty"`
		PTY        *bool             `json:"pty,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ExecRequest{}, fmt.Errorf("workspace_exec: invalid args: %w", err)
	}
	timeout := in.Timeout
	if v, ok := firstPresentInt(in.TimeoutSec, in.TimeoutOld); ok {
		timeout = v
	}
	pty := false
	if in.PTY != nil {
		pty = *in.PTY
	}
	if in.TTY != nil && *in.TTY {
		pty = true
	}
	return ExecRequest{
		Command:    in.Command,
		Stdin:      in.Stdin,
		WorkDir:    in.Cwd,
		Env:        in.Env,
		Timeout:    timeout,
		Background: in.Background,
		PTY:        pty,
		Backend:    "workspaceexec",
	}, nil
}

// extractHostExec extracts an ExecRequest from exec_command (hostexec) arguments.
//
// Expected JSON shape:
//
//	{"command":"...","workdir":"...","env":{...},"timeout_sec":30,"background":false,"tty":false,"pty":false}
func extractHostExec(toolName string, args []byte) (ExecRequest, error) {
	var in struct {
		Command    string            `json:"command"`
		Workdir    string            `json:"workdir,omitempty"`
		Env        map[string]string `json:"env,omitempty"`
		TimeoutSec *int              `json:"timeout_sec,omitempty"`
		TimeoutOld *int              `json:"timeoutSec,omitempty"`
		Background bool              `json:"background,omitempty"`
		TTY        *bool             `json:"tty,omitempty"`
		PTY        *bool             `json:"pty,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ExecRequest{}, fmt.Errorf("exec_command: invalid args: %w", err)
	}
	timeout := 0
	if v, ok := firstPresentInt(in.TimeoutSec, in.TimeoutOld); ok {
		timeout = v
	}
	pty := false
	if in.PTY != nil {
		pty = *in.PTY
	}
	if in.TTY != nil && *in.TTY {
		pty = true
	}
	return ExecRequest{
		Command:    in.Command,
		WorkDir:    in.Workdir,
		Env:        in.Env,
		Timeout:    timeout,
		Background: in.Background,
		PTY:        pty,
		Backend:    "hostexec",
	}, nil
}

// extractCodeExec extracts an ExecRequest from execute_code (codeexec) arguments.
//
// Expected JSON shape:
//
//	{"code_blocks":[{"language":"python","code":"..."}],"execution_id":"..."}
func extractCodeExec(toolName string, args []byte) (ExecRequest, error) {
	var in struct {
		CodeBlocks  json.RawMessage `json:"code_blocks"`
		ExecutionID string          `json:"execution_id,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ExecRequest{}, fmt.Errorf("execute_code: invalid args: %w", err)
	}
	blocks, err := unmarshalCodeExecBlocks(in.CodeBlocks)
	if err != nil {
		return ExecRequest{}, fmt.Errorf("execute_code: invalid args: %w", err)
	}
	codeBlocks := make([]string, len(blocks))
	for i, block := range blocks {
		codeBlocks[i] = block.Code
	}
	return ExecRequest{
		CodeBlocks: codeBlocks,
		Backend:    "codeexec",
	}, nil
}

func extractUnsupportedStdinWrite(toolName string, args []byte) (ExecRequest, error) {
	_ = args
	return ExecRequest{}, fmt.Errorf("%s: incremental stdin session writes are denied by default", toolName)
}

func firstPresentInt(values ...*int) (int, bool) {
	for _, value := range values {
		if value != nil {
			return *value, true
		}
	}
	return 0, false
}

func unmarshalCodeExecBlocks(raw json.RawMessage) ([]struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	if s, ok := value.(string); ok {
		raw = json.RawMessage(s)
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
	}

	switch value.(type) {
	case []any:
		var blocks []struct {
			Language string `json:"language"`
			Code     string `json:"code"`
		}
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	case map[string]any:
		var block struct {
			Language string `json:"language"`
			Code     string `json:"code"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		return []struct {
			Language string `json:"language"`
			Code     string `json:"code"`
		}{block}, nil
	default:
		return nil, fmt.Errorf("code_blocks: expected array, object, or string, got %T", value)
	}
}

// RegisterExtractor adds a custom extractor for a tool name.
// This allows extending the guard with safety scanning for custom tools.
func RegisterExtractor(extractors map[string]Extractor, toolName string, ext Extractor) {
	extractors[toolName] = ext
}
