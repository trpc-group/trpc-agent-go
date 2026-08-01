//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Backend identifies which execution surface a request targets.
type Backend string

const (
	// BackendWorkspace is workspace_exec style isolation.
	BackendWorkspace Backend = "workspaceexec"
	// BackendHost is hostexec / exec_command on the machine.
	BackendHost Backend = "hostexec"
	// BackendCode is codeexec / execute_code payloads.
	BackendCode Backend = "codeexec"
	// BackendUnknown is used when the tool name is not recognized.
	BackendUnknown Backend = "unknown"
)

// Extracted is the normalized payload Guard scans.
type Extracted struct {
	Backend    Backend
	ToolName   string
	Command    string
	Stdin      string
	Cwd        string
	Env        map[string]string
	CodeBlocks []string
	RawText    string
}

// Extract pulls command / stdin / code_blocks / env from a permission request.
//
// It is intentionally tool-aware for the first-party exec tools so that
// codeexec payloads are not ignored (a bypass repeatedly found in #2002 PRs).
// Malformed JSON arguments, including JSON null, return an error so Guard can
// fail closed instead of treating garbage as an empty/allow payload.
func Extract(req *tool.PermissionRequest) (Extracted, error) {
	out := Extracted{Backend: BackendUnknown}
	if req == nil {
		return out, nil
	}
	out.ToolName = req.ToolName
	if req.Declaration != nil && req.Declaration.Name != "" {
		out.ToolName = req.Declaration.Name
	}
	out.Backend = classifyBackend(out.ToolName)

	rawArgs := bytes.TrimSpace(req.Arguments)
	if len(rawArgs) == 0 {
		return out, nil
	}
	if bytes.Equal(rawArgs, []byte("null")) {
		return out, fmt.Errorf("safety: tool arguments must be a JSON object, got null")
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rawArgs, &payload); err != nil {
		return out, fmt.Errorf("safety: decode tool arguments: %w", err)
	}
	if payload == nil {
		return out, fmt.Errorf("safety: tool arguments must be a JSON object")
	}

	out.Command = stringField(payload, "command")
	out.Stdin = firstStringField(payload, "stdin", "chars")
	out.Cwd = firstStringField(payload, "cwd", "workdir", "working_directory")
	env, err := stringMapField(payload, "env")
	if err != nil {
		return out, err
	}
	out.Env = env
	out.CodeBlocks = codeBlockTexts(payload["code_blocks"])

	parts := make([]string, 0, 4)
	if out.Command != "" {
		parts = append(parts, out.Command)
	}
	if out.Stdin != "" {
		parts = append(parts, out.Stdin)
	}
	if out.Cwd != "" {
		parts = append(parts, out.Cwd)
	}
	parts = append(parts, out.CodeBlocks...)
	out.RawText = strings.Join(parts, "\n")
	return out, nil
}

func classifyBackend(name string) Backend {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "workspace_exec" || strings.HasPrefix(n, "workspace_"):
		return BackendWorkspace
	case n == "exec_command" || n == "host_exec" || strings.Contains(n, "hostexec"):
		return BackendHost
	case n == "execute_code" || n == "code_execution" ||
		strings.Contains(n, "code_exec") || strings.Contains(n, "codeexec"):
		return BackendCode
	default:
		// Heuristic: argument shape still drives scanning even if the name is custom.
		return BackendUnknown
	}
}

func stringField(payload map[string]json.RawMessage, key string) string {
	if payload == nil {
		return ""
	}
	raw, ok := payload[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func firstStringField(payload map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if s := stringField(payload, k); s != "" {
			return s
		}
	}
	return ""
}

func stringMapField(payload map[string]json.RawMessage, key string) (map[string]string, error) {
	if payload == nil {
		return nil, nil
	}
	raw, ok := payload[key]
	if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("safety: decode %s: must be a string map: %w", key, err)
	}
	return m, nil
}

func codeBlockTexts(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	// Array of objects with "code".
	var blocks []struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		out := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if strings.TrimSpace(b.Code) != "" {
				out = append(out, b.Code)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	// Single object {"language":"...","code":"..."}.
	var one struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &one); err == nil && strings.TrimSpace(one.Code) != "" {
		return []string{one.Code}
	}
	// Plain string array.
	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		out := make([]string, 0, len(strs))
		for _, s := range strs {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	// Double-encoded string form used by some model outputs.
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil && strings.TrimSpace(asString) != "" {
		var inner []struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(asString), &inner); err == nil {
			out := make([]string, 0, len(inner))
			for _, b := range inner {
				if strings.TrimSpace(b.Code) != "" {
					out = append(out, b.Code)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		return []string{asString}
	}
	return nil
}
