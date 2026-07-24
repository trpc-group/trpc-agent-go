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

const (
	workspaceExecDefaultTimeoutSec = 300
	hostExecDefaultTimeoutSec      = 1800
	skillDefaultTimeoutSec         = 300
)

// requestsFromToolCall parses a PermissionRequest-like tool call payload into
// one or more scan requests. execute_code can produce one request per code block.
func requestsFromToolCall(
	toolName, toolCallID string,
	backend Backend,
	args []byte,
	metadata map[string]any,
) ([]ScanRequest, error) {
	canonicalToolName := normalizeToolName(toolName)
	if backend == "" {
		backend = inferBackend(canonicalToolName)
	}
	switch canonicalToolName {
	case "workspace_exec":
		return parseExecArgs(toolName, "workspace_exec", toolCallID, backend, args, "cwd", metadata)
	case "exec_command":
		return parseExecArgs(toolName, "exec_command", toolCallID, backend, args, "workdir", metadata)
	case "skill_run":
		return parseExecArgs(toolName, "skill_run", toolCallID, backend, args, "cwd", metadata)
	case "skill_exec":
		return parseExecArgs(toolName, "skill_exec", toolCallID, backend, args, "cwd", metadata)
	case "workspace_write_stdin", "write_stdin", "skill_write_stdin":
		return parseWriteStdinArgs(toolName, toolCallID, backend, args, metadata)
	case "workspace_kill_session", "kill_session":
		return []ScanRequest{{
			ToolName:     toolName,
			ToolCallID:   toolCallID,
			Backend:      backend,
			RawArguments: append([]byte(nil), args...),
			Metadata:     metadata,
		}}, nil
	case "execute_code":
		return parseCodeExecArgs(toolName, toolCallID, backend, args, metadata)
	default:
		return []ScanRequest{{
			ToolName:     toolName,
			ToolCallID:   toolCallID,
			Backend:      backend,
			RawArguments: append([]byte(nil), args...),
			Metadata:     metadata,
		}}, nil
	}
}

func normalizeToolName(toolName string) string {
	const hostexecPrefix = "hostexec_"
	if !strings.HasPrefix(toolName, hostexecPrefix) {
		return toolName
	}
	switch strings.TrimPrefix(toolName, hostexecPrefix) {
	case "exec_command", "write_stdin", "kill_session":
		return strings.TrimPrefix(toolName, hostexecPrefix)
	default:
		return toolName
	}
}

func inferBackend(toolName string) Backend {
	switch toolName {
	case "workspace_exec", "workspace_write_stdin", "workspace_kill_session":
		return BackendWorkspace
	case "exec_command", "write_stdin", "kill_session",
		"skill_run", "skill_exec", "skill_write_stdin":
		return BackendHost
	case "execute_code":
		return BackendCodeExec
	default:
		return BackendUnknown
	}
}

func parseExecArgs(
	toolName, toolKind, toolCallID string,
	backend Backend,
	args []byte,
	cwdField string,
	metadata map[string]any,
) ([]ScanRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	command, err := stringField(raw, "command")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("command is required")
	}
	timeout, err := timeoutField(toolKind, raw)
	if err != nil {
		return nil, err
	}
	cwd, err := stringField(raw, cwdField)
	if err != nil {
		return nil, err
	}
	env, err := stringMapField(raw, "env")
	if err != nil {
		return nil, err
	}
	stdin, err := stringField(raw, "stdin")
	if err != nil {
		return nil, err
	}
	background, err := boolField(raw, "background")
	if err != nil {
		return nil, err
	}
	tty, err := boolAnyField(raw, "tty", "pty")
	if err != nil {
		return nil, err
	}
	req := ScanRequest{
		ToolName:     toolName,
		ToolCallID:   toolCallID,
		Backend:      backend,
		Command:      command,
		Cwd:          cwd,
		Env:          env,
		Stdin:        stdin,
		TimeoutSec:   timeout,
		Background:   background,
		TTY:          tty,
		RawArguments: append([]byte(nil), args...),
		Metadata:     metadata,
	}
	return []ScanRequest{req}, nil
}

func parseWriteStdinArgs(
	toolName, toolCallID string,
	backend Backend,
	args []byte,
	metadata map[string]any,
) ([]ScanRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	chars, err := stringField(raw, "chars")
	if err != nil {
		return nil, err
	}
	submit, err := boolAnyField(raw, "append_newline", "submit")
	if err != nil {
		return nil, err
	}
	if chars == "" && !submit {
		return []ScanRequest{{
			ToolName:     toolName,
			ToolCallID:   toolCallID,
			Backend:      backend,
			RawArguments: append([]byte(nil), args...),
			Metadata:     metadata,
		}}, nil
	}
	return []ScanRequest{{
		ToolName:     toolName,
		ToolCallID:   toolCallID,
		Backend:      backend,
		Stdin:        chars,
		RawArguments: append([]byte(nil), args...),
		Metadata:     metadata,
	}}, nil
}

type codeExecArgs struct {
	CodeBlocks  json.RawMessage `json:"code_blocks"`
	ExecutionID string          `json:"execution_id,omitempty"`
}

type codeBlock struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

func parseCodeExecArgs(
	toolName, toolCallID string,
	backend Backend,
	args []byte,
	metadata map[string]any,
) ([]ScanRequest, error) {
	var in codeExecArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	blocks, err := unmarshalCodeBlocks(in.CodeBlocks)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("code_blocks is required")
	}
	reqs := make([]ScanRequest, 0, len(blocks))
	for _, block := range blocks {
		reqs = append(reqs, ScanRequest{
			ToolName:     toolName,
			ToolCallID:   toolCallID,
			Backend:      backend,
			Language:     block.Language,
			Code:         block.Code,
			RawArguments: append([]byte(nil), args...),
			Metadata:     metadata,
		})
	}
	return reqs, nil
}

func unmarshalCodeBlocks(raw json.RawMessage) ([]codeBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, err
	}
	if s, ok := val.(string); ok {
		raw = json.RawMessage(s)
		if err := json.Unmarshal(raw, &val); err != nil {
			return nil, err
		}
	}
	switch val.(type) {
	case []any:
		var blocks []codeBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	case map[string]any:
		var block codeBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		return []codeBlock{block}, nil
	default:
		return nil, fmt.Errorf("code_blocks: expected array, object, or string, got %T", val)
	}
}

func stringField(raw map[string]json.RawMessage, key string) (string, error) {
	var out string
	if b, ok := raw[key]; ok {
		if err := json.Unmarshal(b, &out); err != nil {
			return "", fmt.Errorf("%s: expected string: %w", key, err)
		}
	}
	return out, nil
}

func stringMapField(raw map[string]json.RawMessage, key string) (map[string]string, error) {
	var out map[string]string
	if b, ok := raw[key]; ok {
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("%s: expected string map: %w", key, err)
		}
	}
	return out, nil
}

func intField(raw map[string]json.RawMessage, keys ...string) (int, error) {
	for _, key := range keys {
		b, ok := raw[key]
		if !ok {
			continue
		}
		var out int
		if err := json.Unmarshal(b, &out); err != nil {
			return 0, fmt.Errorf("%s: expected integer: %w", key, err)
		}
		return out, nil
	}
	return 0, nil
}

func timeoutField(toolName string, raw map[string]json.RawMessage) (int, error) {
	var timeout int
	var err error
	switch toolName {
	case "workspace_exec":
		// workspace_exec first selects timeout_sec/timeoutSec, then falls
		// back to timeout when the selected value is non-positive.
		timeout, err = intField(raw, "timeout_sec", "timeoutSec")
		if err != nil {
			return 0, err
		}
		if timeout <= 0 {
			timeout, err = intField(raw, "timeout")
		}
	case "exec_command":
		// exec_command does not expose the workspace_exec timeout alias.
		timeout, err = intField(raw, "timeout_sec", "timeoutSec")
	case "skill_run", "skill_exec":
		// Skill tools use timeout directly and ignore timeout_sec aliases.
		timeout, err = intField(raw, "timeout")
	default:
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if timeout > 0 {
		return timeout, nil
	}
	switch toolName {
	case "workspace_exec":
		return workspaceExecDefaultTimeoutSec, nil
	case "exec_command":
		return hostExecDefaultTimeoutSec, nil
	case "skill_run", "skill_exec":
		return skillDefaultTimeoutSec, nil
	default:
		return 0, nil
	}
}

func boolField(raw map[string]json.RawMessage, key string) (bool, error) {
	var out bool
	if b, ok := raw[key]; ok {
		if err := json.Unmarshal(b, &out); err != nil {
			return false, fmt.Errorf("%s: expected boolean: %w", key, err)
		}
	}
	return out, nil
}

func boolAnyField(raw map[string]json.RawMessage, keys ...string) (bool, error) {
	for _, key := range keys {
		if _, ok := raw[key]; !ok {
			continue
		}
		value, err := boolField(raw, key)
		if err != nil {
			return false, err
		}
		if value {
			return true, nil
		}
	}
	return false, nil
}
