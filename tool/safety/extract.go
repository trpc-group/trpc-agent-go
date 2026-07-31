//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Backend identifies which execution surface a request targets.
type Backend string

const (
	BackendWorkspace Backend = "workspaceexec"
	BackendHost      Backend = "hostexec"
	BackendCode      Backend = "codeexec"
	BackendUnknown   Backend = "unknown"
)

// Extracted is the normalized payload Guard scans.
type Extracted struct {
	Backend    Backend
	ToolName   string
	Command    string
	Stdin      string
	Cwd        string
	CodeBlocks []string
	RawText    string
}

// Extract pulls command / stdin / code_blocks from a permission request.
//
// It is intentionally tool-aware for the first-party exec tools so that
// codeexec payloads are not ignored (a bypass repeatedly found in #2002 PRs).
func Extract(req *tool.PermissionRequest) Extracted {
	out := Extracted{Backend: BackendUnknown}
	if req == nil {
		return out
	}
	out.ToolName = req.ToolName
	if req.Declaration != nil && req.Declaration.Name != "" {
		out.ToolName = req.Declaration.Name
	}
	out.Backend = classifyBackend(out.ToolName)

	var payload map[string]json.RawMessage
	if len(req.Arguments) > 0 {
		_ = json.Unmarshal(req.Arguments, &payload)
	}
	out.Command = stringField(payload, "command")
	out.Stdin = firstStringField(payload, "stdin", "chars")
	out.Cwd = firstStringField(payload, "cwd", "workdir", "working_directory")
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
	return out
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
			return out
		}
		return []string{asString}
	}
	return nil
}
