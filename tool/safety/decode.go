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
	"errors"
	"fmt"
	"strings"
	"time"
)

// decodeRequest decodes a tool.PermissionRequest payload into a ScanInput
// using the registered profile. A known profile with malformed JSON or a
// required field of the wrong type returns a decode error; the scanner
// converts that into a deny finding so malformed inputs never silently
// become an empty scan.
//
// For unknown tools the decoder returns a best-effort ScanInput with
// Backend=BackendUnknown and a flag indicating the tool is unrecognized.
// The scanner then maps command-shaped unknown tools to DecisionAsk.
func decodeRequest(
	toolName string,
	arguments []byte,
	profiles profileRegistry,
) (ScanInput, error) {
	in := ScanInput{ToolName: toolName}
	if len(arguments) == 0 {
		return in, nil
	}
	profile, ok := profiles.lookup(toolName)
	if !ok {
		return decodeUnknownTool(in, arguments)
	}
	in.Backend = profile.Backend
	in.ToolProfile = profile.Name

	var raw map[string]any
	if err := json.Unmarshal(arguments, &raw); err != nil {
		return in, fmt.Errorf("tool %q: invalid arguments: %w", toolName, err)
	}

	if err := decodeRequiredFields(&in, profile, raw, toolName); err != nil {
		return in, err
	}
	decodeSessionFields(&in, profile, raw)
	if err := decodeOptionalFields(&in, profile, raw, toolName); err != nil {
		return in, err
	}
	return in, nil
}

// decodeUnknownTool peeks for a command-shaped field so unknown MCP tools
// with command arguments are scanned rather than skipped. Malformed JSON
// in an unknown tool returns an error so the guard can ask rather than
// silently allow. This fixes the P1 regression where malformed JSON was
// swallowed and the tool was allowed.
func decodeUnknownTool(in ScanInput, arguments []byte) (ScanInput, error) {
	// First check if the JSON is valid at all.
	var raw map[string]any
	if err := json.Unmarshal(arguments, &raw); err != nil {
		// Malformed JSON from an unknown tool: return an error so
		// the guard can produce an ask finding (unknown shape, cannot
		// decode). The previous implementation swallowed this error
		// and returned an empty ScanInput, which caused the guard to
		// allow the call.
		return in, fmt.Errorf("unknown tool: malformed arguments: %w", err)
	}
	peaked, _ := peekCommand(arguments)
	if peaked == "" {
		// No command field: this is a non-execution MCP tool. Return
		// the input unchanged so the guard allows it through (the MCP
		// server's behavior is outside the local process boundary).
		return in, nil
	}
	in.Backend = BackendUnknown
	in.Command = peaked
	return in, nil
}

// decodeRequiredFields handles the command and code_blocks fields that
// known profiles require.
func decodeRequiredFields(
	in *ScanInput,
	profile ToolProfile,
	raw map[string]any,
	toolName string,
) error {
	if profile.CommandField != "" {
		cmd, err := requiredString(raw, profile.CommandField)
		if err != nil {
			return fmt.Errorf("tool %q: %w", toolName, err)
		}
		in.Command = cmd
	}
	if profile.CodeBlocksField != "" {
		blocks, err := decodeCodeBlocks(raw, profile.CodeBlocksField)
		if err != nil {
			return fmt.Errorf("tool %q: %w", toolName, err)
		}
		in.CodeBlocks = blocks
	}
	return nil
}

// decodeSessionFields handles the session-tool argument shapes
// (write_stdin, kill_session, and their workspace_ prefixed variants).
func decodeSessionFields(in *ScanInput, profile ToolProfile, raw map[string]any) {
	switch profile.Name {
	case "write_stdin", "workspace_write_stdin":
		in.SessionID = firstString(raw, "session_id", "sessionId")
		in.SessionInput = firstString(raw, "chars")
	case "kill_session", "workspace_kill_session":
		in.SessionID = firstString(raw, "session_id", "sessionId")
	}
}

// decodeOptionalFields handles cwd, env, timeout, background, and PTY
// fields when the profile declares them.
func decodeOptionalFields(
	in *ScanInput,
	profile ToolProfile,
	raw map[string]any,
	toolName string,
) error {
	for _, f := range profile.WorkingDirFields {
		if v, ok := rawString(raw, f); ok {
			in.Cwd = v
			break
		}
	}
	if profile.EnvironmentField != "" {
		env, err := decodeEnvMap(raw, profile.EnvironmentField)
		if err != nil {
			return fmt.Errorf("tool %q: %w", toolName, err)
		}
		if env != nil {
			in.Env = env
		}
	}
	for _, f := range profile.TimeoutFields {
		if v, ok := rawInt(raw, f); ok {
			in.Timeout = time.Duration(v) * time.Second
			break
		}
	}
	for _, f := range profile.BackgroundFields {
		if v, ok := rawBool(raw, f); ok {
			in.Background = v
			break
		}
	}
	for _, f := range profile.PTYFields {
		if v, ok := rawBool(raw, f); ok {
			in.PTY = v
			break
		}
	}
	return nil
}

// peekCommand returns the value of a top-level "command" field if present,
// used for unknown tools that expose a command surface.
func peekCommand(arguments []byte) (string, error) {
	var raw map[string]any
	if err := json.Unmarshal(arguments, &raw); err != nil {
		return "", err
	}
	if cmd, ok := rawString(raw, "command"); ok {
		return cmd, nil
	}
	return "", nil
}

// requiredString returns the string value at key, or an error if the key is
// missing or has the wrong type.
func requiredString(raw map[string]any, key string) (string, error) {
	v, ok := raw[key]
	if !ok {
		return "", fmt.Errorf("field %q is required", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string, got %T", key, v)
	}
	return s, nil
}

// firstString returns the first non-empty string value found at any of the
// candidate keys.
func firstString(raw map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := rawString(raw, k); ok {
			return v
		}
	}
	return ""
}

// rawString returns the string value at key, accepting only string values.
func rawString(raw map[string]any, key string) (string, bool) {
	v, ok := raw[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// rawInt returns the int value at key, accepting json numbers and strings
// that parse as integers.
func rawInt(raw map[string]any, key string) (int, bool) {
	v, ok := raw[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case string:
		// Best-effort parse; not currently used by canonical profiles
		// but helps custom MCP profiles that encode ints as strings.
		var parsed int
		if _, err := fmt.Sscanf(n, "%d", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// rawBool returns the bool value at key.
func rawBool(raw map[string]any, key string) (bool, bool) {
	v, ok := raw[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// decodeEnvMap returns the env map at key, or nil if absent. Returns an
// error if the value is present but not a string-keyed map of strings.
func decodeEnvMap(raw map[string]any, key string) (map[string]string, error) {
	v, ok := raw[key]
	if !ok {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("field %q must be an object of string values", key)
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		s, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("field %q: env %q must be a string, got %T", key, k, val)
		}
		out[k] = s
	}
	return out, nil
}

// decodeCodeBlocks returns the code blocks at key. The accepted shapes are
// the ones the codeexec tool already supports: an array of
// {language, code} objects, a single such object, or a string (treated as
// a bash block). A wrong type yields an error.
func decodeCodeBlocks(raw map[string]any, key string) ([]CodeBlock, error) {
	v, ok := raw[key]
	if !ok {
		return nil, fmt.Errorf("field %q is required", key)
	}
	switch val := v.(type) {
	case []any:
		blocks := make([]CodeBlock, 0, len(val))
		for i, item := range val {
			b, err := decodeOneBlock(item)
			if err != nil {
				return nil, fmt.Errorf("field %q[%d]: %w", key, i, err)
			}
			blocks = append(blocks, b)
		}
		return blocks, nil
	case map[string]any:
		b, err := decodeOneBlock(val)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		return []CodeBlock{b}, nil
	case string:
		if strings.TrimSpace(val) == "" {
			return nil, errors.New("field \"code_blocks\" is empty")
		}
		return []CodeBlock{{Language: "bash", Code: val}}, nil
	}
	return nil, fmt.Errorf("field %q must be array, object, or string, got %T", key, v)
}

func decodeOneBlock(item any) (CodeBlock, error) {
	m, ok := item.(map[string]any)
	if !ok {
		return CodeBlock{}, fmt.Errorf("must be object, got %T", item)
	}
	lang, _ := m["language"].(string)
	code, _ := m["code"].(string)
	if lang == "" {
		return CodeBlock{}, errors.New("language is required")
	}
	if code == "" {
		return CodeBlock{}, errors.New("code is required")
	}
	return CodeBlock{Language: lang, Code: code}, nil
}
