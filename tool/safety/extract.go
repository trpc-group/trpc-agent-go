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
	"strings"
)

// ExecRequest is the backend-agnostic view of a tool call that the rule engine
// scans. It is produced by extract from the raw tool arguments.
type ExecRequest struct {
	// Command is the shell command (workspace_exec / exec_command) or the
	// concatenated source code (execute_code).
	Command string
	// Cwd is the working directory (cwd for workspace_exec, workdir for
	// exec_command).
	Cwd string
	// Env holds environment overrides supplied by the model.
	Env map[string]string
	// Background is true when the command is started detached.
	Background bool
	// PTY is true when a TTY/PTY is requested (tty or pty alias).
	PTY bool
	// TimeoutSec is the requested timeout in seconds, 0 when unset.
	TimeoutSec int
}

// execArgs is the union of the workspace_exec and exec_command argument
// schemas. workspace_exec uses "cwd" while exec_command uses "workdir"; both
// carry the same background / tty / pty / timeout fields, so a single struct
// covers both backends.
type execArgs struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd"`     // workspace_exec
	Workdir       string            `json:"workdir"` // exec_command
	Env           map[string]string `json:"env"`
	Background    bool              `json:"background"`
	Timeout       int               `json:"timeout"`
	TimeoutSec    *int              `json:"timeout_sec"`
	TimeoutSecOld *int              `json:"timeoutSec"`
	TTY           *bool             `json:"tty"`
	PTY           *bool             `json:"pty"`
}

// backendOf returns the backend identifier configured for toolName. A tool not
// listed under any backend (e.g. webfetch, file tools) returns "", which the
// guard treats as "allow without scanning".
func backendOf(toolName string, p *Policy) string {
	if p == nil {
		return ""
	}
	return p.backendFor(toolName)
}

// extract turns the raw tool arguments into an ExecRequest for the given
// backend. A JSON error is returned to the caller, which fails closed via the
// policy's unparseable_action.
func extract(args []byte, backend string) (ExecRequest, error) {
	if backend == BackendCode {
		return extractCode(args)
	}
	var a execArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return ExecRequest{}, fmt.Errorf("parse exec args: %w", err)
		}
	}
	return ExecRequest{
		Command:    a.Command,
		Cwd:        firstNonEmpty(a.Cwd, a.Workdir),
		Env:        a.Env,
		Background: a.Background,
		PTY:        derefBool(a.PTY) || derefBool(a.TTY),
		TimeoutSec: pickTimeout(a.Timeout, a.TimeoutSec, a.TimeoutSecOld),
	}, nil
}

// extractCode handles the execute_code schema. Its payload is a code_blocks
// array (not a shell command), so the source is concatenated into Command for
// the secret / resource rules; the shell-structure rules see no pipeline. Code
// execution relies primarily on the sandbox for isolation (see README).
func extractCode(args []byte) (ExecRequest, error) {
	var a struct {
		CodeBlocks json.RawMessage `json:"code_blocks"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return ExecRequest{}, fmt.Errorf("parse code args: %w", err)
		}
	}
	return ExecRequest{Command: joinCodeBlocks(a.CodeBlocks)}, nil
}

// codeBlock mirrors the subset of codeexecutor.CodeBlock that the guard needs.
type codeBlock struct {
	Code     string `json:"code"`
	Language string `json:"language"`
}

// joinCodeBlocks extracts and concatenates the source from a code_blocks value.
// It accepts the same shapes as codeexec (array, single object, or a
// double-encoded JSON string) and falls back to the raw bytes so the secret
// scan still has something to inspect.
func joinCodeBlocks(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return string(raw)
	}
	if s, ok := val.(string); ok {
		// Double-encoded array: unwrap and re-parse.
		raw = json.RawMessage(s)
		if err := json.Unmarshal(raw, &val); err != nil {
			return s
		}
	}
	switch val.(type) {
	case []any:
		var blocks []codeBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return string(raw)
		}
		var sb strings.Builder
		for _, b := range blocks {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(b.Code)
		}
		return sb.String()
	case map[string]any:
		var b codeBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return string(raw)
		}
		return b.Code
	default:
		return string(raw)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func derefBool(b *bool) bool {
	return b != nil && *b
}

// pickTimeout mirrors workspaceexec/hostexec precedence: the explicit
// timeout_sec / timeoutSec aliases win over the bare timeout field.
func pickTimeout(timeout int, sec, secOld *int) int {
	if sec != nil && *sec > 0 {
		return *sec
	}
	if secOld != nil && *secOld > 0 {
		return *secOld
	}
	if timeout > 0 {
		return timeout
	}
	return 0
}
