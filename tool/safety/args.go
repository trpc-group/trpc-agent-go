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

// RequestsFromToolCall parses a PermissionRequest-like tool call payload into
// one or more scan requests. execute_code can produce one request per code block.
func RequestsFromToolCall(
	toolName, toolCallID string,
	backend Backend,
	args []byte,
	metadata map[string]any,
) ([]ScanRequest, error) {
	if backend == "" {
		backend = InferBackend(toolName)
	}
	switch toolName {
	case "workspace_exec":
		return parseExecArgs(toolName, toolCallID, backend, args, "cwd", metadata)
	case "exec_command":
		return parseExecArgs(toolName, toolCallID, backend, args, "workdir", metadata)
	case "workspace_write_stdin", "write_stdin":
		return parseWriteStdinArgs(toolName, toolCallID, backend, args, metadata)
	case "workspace_kill_session", "kill_session":
		return []ScanRequest{{
			ToolName:   toolName,
			ToolCallID: toolCallID,
			Backend:    backend,
			Metadata:   metadata,
		}}, nil
	case "execute_code":
		return parseCodeExecArgs(toolName, toolCallID, backend, args, metadata)
	default:
		return []ScanRequest{{
			ToolName:   toolName,
			ToolCallID: toolCallID,
			Backend:    backend,
			Arguments:  append([]byte(nil), args...),
			Metadata:   metadata,
		}}, nil
	}
}

// InferBackend maps well-known tool names to safety backends.
func InferBackend(toolName string) Backend {
	switch toolName {
	case "workspace_exec", "workspace_write_stdin", "workspace_kill_session":
		return BackendWorkspace
	case "exec_command", "write_stdin", "kill_session":
		return BackendHost
	case "execute_code":
		return BackendCodeExec
	default:
		return BackendUnknown
	}
}

func parseExecArgs(
	toolName, toolCallID string,
	backend Backend,
	args []byte,
	cwdField string,
	metadata map[string]any,
) ([]ScanRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	command := stringField(raw, "command")
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("command is required")
	}
	timeout := intField(raw, "timeout_sec", "timeoutSec", "timeout")
	req := ScanRequest{
		ToolName:   toolName,
		ToolCallID: toolCallID,
		Backend:    backend,
		Command:    command,
		Cwd:        stringField(raw, cwdField),
		Env:        stringMapField(raw, "env"),
		Stdin:      stringField(raw, "stdin"),
		TimeoutSec: timeout,
		Background: boolField(raw, "background"),
		TTY:        boolField(raw, "tty") || boolField(raw, "pty"),
		Arguments:  append([]byte(nil), args...),
		Metadata:   metadata,
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
	chars := stringField(raw, "chars")
	submit := boolField(raw, "append_newline") || boolField(raw, "submit")
	if chars == "" && !submit {
		return []ScanRequest{{
			ToolName:   toolName,
			ToolCallID: toolCallID,
			Backend:    backend,
			Arguments:  append([]byte(nil), args...),
			Metadata:   metadata,
		}}, nil
	}
	return []ScanRequest{{
		ToolName:   toolName,
		ToolCallID: toolCallID,
		Backend:    backend,
		Command:    chars,
		Stdin:      chars,
		Arguments:  append([]byte(nil), args...),
		Metadata:   metadata,
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
			ToolName:   toolName,
			ToolCallID: toolCallID,
			Backend:    backend,
			Language:   block.Language,
			Code:       block.Code,
			Arguments:  append([]byte(nil), args...),
			Metadata:   metadata,
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

func stringField(raw map[string]json.RawMessage, key string) string {
	var out string
	if b, ok := raw[key]; ok {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func stringMapField(raw map[string]json.RawMessage, key string) map[string]string {
	var out map[string]string
	if b, ok := raw[key]; ok {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func intField(raw map[string]json.RawMessage, keys ...string) int {
	for _, key := range keys {
		b, ok := raw[key]
		if !ok {
			continue
		}
		var out int
		if err := json.Unmarshal(b, &out); err == nil {
			return out
		}
	}
	return 0
}

func boolField(raw map[string]json.RawMessage, key string) bool {
	var out bool
	if b, ok := raw[key]; ok {
		_ = json.Unmarshal(b, &out)
	}
	return out
}
