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
	Paths      []string
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
	if args := stringSliceField(payload, "args", "argv", "arguments"); len(args) > 0 {
		// Mentors repeatedly flag PRs that scan `command` but drop argv.
		joined := strings.Join(args, " ")
		if out.Command == "" {
			out.Command = joined
		} else {
			out.Command = strings.TrimSpace(out.Command + " " + joined)
		}
	}
	out.Stdin = firstStringField(payload, "stdin", "chars")
	out.Cwd = firstStringField(payload, "cwd", "workdir", "working_directory")
	env, err := stringMapField(payload, "env")
	if err != nil {
		return out, err
	}
	out.Env = env
	blocks, err := codeBlockTexts(payload["code_blocks"], 0)
	if err != nil {
		return out, err
	}
	out.CodeBlocks = blocks
	out.Paths = pathFields(payload)

	parts := make([]string, 0, 8)
	if out.Command != "" {
		parts = append(parts, out.Command)
	}
	if out.Stdin != "" {
		parts = append(parts, out.Stdin)
	}
	if out.Cwd != "" {
		parts = append(parts, out.Cwd)
	}
	parts = append(parts, out.Paths...)
	parts = append(parts, out.CodeBlocks...)
	parts = append(parts, secretKeyedStrings(payload)...)
	for k, v := range out.Env {
		if looksLikeSecretKey(k) && v != "" {
			parts = append(parts, k+"="+v)
		}
	}
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

func stringSliceField(payload map[string]json.RawMessage, keys ...string) []string {
	if payload == nil {
		return nil
	}
	for _, key := range keys {
		raw, ok := payload[key]
		if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var strs []string
		if err := json.Unmarshal(raw, &strs); err != nil {
			continue
		}
		out := make([]string, 0, len(strs))
		for _, s := range strs {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func pathFields(payload map[string]json.RawMessage) []string {
	if payload == nil {
		return nil
	}
	keys := []string{
		"path", "file", "filename", "file_path", "filepath",
		"target", "target_path", "src", "dst", "source", "destination",
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, key := range keys {
		if s := stringField(payload, key); s != "" {
			add(s)
		}
		for _, s := range stringSliceField(payload, key) {
			add(s)
		}
	}
	return out
}

func looksLikeSecretKey(k string) bool {
	n := strings.ToLower(strings.TrimSpace(k))
	n = strings.ReplaceAll(n, "-", "_")
	switch {
	case n == "password", n == "passwd", n == "secret", n == "token",
		n == "api_key", n == "apikey", n == "access_token", n == "refresh_token",
		n == "private_key", n == "client_secret", n == "authorization":
		return true
	case strings.Contains(n, "password"), strings.Contains(n, "secret"),
		strings.HasSuffix(n, "_token"), strings.HasSuffix(n, "_key"):
		return true
	default:
		return false
	}
}

func secretKeyedStrings(payload map[string]json.RawMessage) []string {
	if payload == nil {
		return nil
	}
	var out []string
	for k, raw := range payload {
		if !looksLikeSecretKey(k) {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || strings.TrimSpace(s) == "" {
			continue
		}
		out = append(out, k+"="+s)
	}
	return out
}

const maxCodeBlockDepth = 3

func codeBlockTexts(raw json.RawMessage, depth int) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if depth > maxCodeBlockDepth {
		return nil, fmt.Errorf("safety: code_blocks nesting exceeds %d", maxCodeBlockDepth)
	}
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
			return out, nil
		}
	}
	var one struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &one); err == nil && strings.TrimSpace(one.Code) != "" {
		return []string{one.Code}, nil
	}
	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		out := make([]string, 0, len(strs))
		for _, s := range strs {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil && strings.TrimSpace(asString) != "" {
		inner, err := codeBlockTexts(json.RawMessage(asString), depth+1)
		if err != nil {
			return nil, err
		}
		if len(inner) > 0 {
			return inner, nil
		}
		return []string{asString}, nil
	}
	return nil, nil
}
