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
	"math"
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
	executionFields := []string{
		"command", "cmd", "script", "shell",
		"code", "code_blocks", "argv",
	}
	codeLanguage, codeIsExecution, codeLanguageErr :=
		unknownCodeLanguage(raw)
	presentFields := make([]string, 0, len(executionFields))
	shapeErr := codeLanguageErr
	for _, key := range executionFields {
		value, present := raw[key]
		if !present || value == nil {
			continue
		}
		if key == "code" && !codeIsExecution {
			continue
		}
		valid, ignored, err := unknownExecutionFieldShape(key, value)
		if err != nil && shapeErr == nil {
			shapeErr = err
		}
		if valid {
			presentFields = append(presentFields, key)
		}
		if ignored {
			continue
		}
	}
	if len(presentFields) > 1 {
		return in, fmt.Errorf(
			"unknown tool: multiple execution fields are ambiguous: %s",
			strings.Join(presentFields, ", "),
		)
	}
	if shapeErr != nil {
		return in, shapeErr
	}
	if len(presentFields) == 0 {
		// No execution-shaped field: allow ordinary query/read payloads.
		return in, nil
	}
	key := presentFields[0]
	switch key {
	case "command", "cmd", "script", "shell":
		command := raw[key].(string)
		in.Backend = BackendUnknown
		in.Command = command
		return in, nil
	case "code":
		code := raw[key].(string)
		in.Backend = BackendUnknown
		in.CodeBlocks = []CodeBlock{{
			Language: codeLanguage,
			Code:     code,
		}}
		return in, nil
	case "code_blocks":
		blocks, err := decodeCodeBlocks(raw, "code_blocks")
		if err != nil {
			return in, fmt.Errorf("unknown tool: %w", err)
		}
		in.Backend = BackendUnknown
		in.CodeBlocks = blocks
		return in, nil
	case "argv":
		args, err := decodeStringSlice(raw[key])
		if err != nil {
			return in, fmt.Errorf("unknown tool: field %q: %w", "argv", err)
		}
		in.Backend = BackendUnknown
		in.Args = args
		return in, nil
	}
	return in, errors.New("unknown tool: unsupported execution field")
}

func unknownCodeLanguage(
	raw map[string]any,
) (language string, execution bool, err error) {
	code, present := raw["code"]
	if !present || code == nil {
		return "", false, nil
	}
	languageValue, present := raw["language"]
	if !present || languageValue == nil {
		return "", false, nil
	}
	language, ok := languageValue.(string)
	if !ok {
		return "", false, fmt.Errorf(
			"unknown tool: field %q must be a string, got %T",
			"language",
			languageValue,
		)
	}
	if strings.TrimSpace(language) == "" {
		return "", false, nil
	}
	return language, true, nil
}

func unknownExecutionFieldShape(
	key string,
	value any,
) (valid bool, ignored bool, err error) {
	switch key {
	case "command", "cmd", "script", "shell", "code":
		text, ok := value.(string)
		if !ok {
			if key == "shell" {
				if _, modifier := value.(bool); modifier {
					return false, true, nil
				}
			}
			return false, false, fmt.Errorf(
				"unknown tool: field %q must be a string, got %T",
				key,
				value,
			)
		}
		if strings.TrimSpace(text) == "" {
			return false, false, fmt.Errorf(
				"unknown tool: field %q must not be empty",
				key,
			)
		}
		return true, false, nil
	case "code_blocks":
		switch value.(type) {
		case string, []any, map[string]any:
			return true, false, nil
		default:
			return false, false, fmt.Errorf(
				"unknown tool: field %q must be an array, object, or string, got %T",
				key,
				value,
			)
		}
	case "argv":
		if _, ok := value.([]any); !ok {
			return false, false, fmt.Errorf(
				"unknown tool: field %q must be an array of strings, got %T",
				key,
				value,
			)
		}
		return true, false, nil
	}
	return false, true, nil
}

func decodeStringSlice(value any) ([]string, error) {
	raw, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of strings, got %T", value)
	}
	out := make([]string, 0, len(raw))
	for i, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("item %d must be a string, got %T", i, item)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errors.New("must not be empty")
	}
	return out, nil
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
	if profile.CodeField != "" {
		block, err := decodeCodeField(raw, profile)
		if err != nil {
			return fmt.Errorf("tool %q: %w", toolName, err)
		}
		in.CodeBlocks = []CodeBlock{block}
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

func decodeCodeField(
	raw map[string]any,
	profile ToolProfile,
) (CodeBlock, error) {
	code, err := requiredString(raw, profile.CodeField)
	if err != nil {
		return CodeBlock{}, err
	}
	language := strings.TrimSpace(profile.DefaultLanguage)
	if profile.LanguageField != "" {
		if value, present := raw[profile.LanguageField]; present &&
			value != nil {
			explicit, ok := value.(string)
			if !ok {
				return CodeBlock{}, fmt.Errorf(
					"field %q must be a string, got %T",
					profile.LanguageField,
					value,
				)
			}
			if explicit = strings.TrimSpace(explicit); explicit != "" {
				language = explicit
			}
		}
	}
	if language == "" {
		if profile.LanguageField != "" {
			return CodeBlock{}, fmt.Errorf(
				"field %q is required",
				profile.LanguageField,
			)
		}
		return CodeBlock{}, fmt.Errorf(
			"profile %q must declare a default language or a language field",
			profile.Name,
		)
	}
	return CodeBlock{Language: language, Code: code}, nil
}

// decodeSessionFields handles declarative session-tool argument shapes.
func decodeSessionFields(in *ScanInput, profile ToolProfile, raw map[string]any) {
	applySessionProfile(in, profile)
	if len(profile.SessionIDFields) > 0 {
		in.SessionID = firstNonEmptyString(
			raw,
			profile.SessionIDFields...,
		)
	}
	if profile.SessionInputField != "" {
		in.SessionInput = firstString(raw, profile.SessionInputField)
	}
	for _, field := range profile.SessionSubmitFields {
		if submit, ok := rawBool(raw, field); ok {
			in.sessionSubmit = submit
			break
		}
	}
}

func applySessionProfile(in *ScanInput, profile ToolProfile) {
	in.sessionCreates = profile.CreatesSession
	in.sessionWrites = len(profile.SessionIDFields) > 0 &&
		profile.SessionInputField != ""
	in.sessionTerminates = profile.TerminatesSession
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
	if profile.Name == "workspace_exec" {
		timeout, field, ok := workspaceTimeoutSeconds(raw)
		if ok {
			if err := setDecodedTimeout(in, timeout, toolName, field); err != nil {
				return err
			}
		}
	} else {
		timeoutDecoded := false
		for _, f := range profile.TimeoutFields {
			if v, ok := rawInt(raw, f); ok {
				if err := setDecodedTimeout(in, v, toolName, f); err != nil {
					return err
				}
				timeoutDecoded = true
				break
			}
		}
		if !timeoutDecoded {
			for _, f := range profile.TimeoutMillisecondsFields {
				if v, ok := rawInt(raw, f); ok {
					if err := setDecodedTimeoutMilliseconds(
						in,
						v,
						toolName,
						f,
					); err != nil {
						return err
					}
					break
				}
			}
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

func setDecodedTimeoutMilliseconds(
	in *ScanInput,
	milliseconds int,
	toolName string,
	field string,
) error {
	if milliseconds < 0 ||
		int64(milliseconds) > math.MaxInt64/int64(time.Millisecond) {
		return fmt.Errorf(
			"tool %q: field %q must be between 0 and %d milliseconds",
			toolName,
			field,
			math.MaxInt64/int64(time.Millisecond),
		)
	}
	in.Timeout = time.Duration(milliseconds) * time.Millisecond
	return nil
}

func workspaceTimeoutSeconds(raw map[string]any) (int, string, bool) {
	for _, field := range []string{"timeout_sec", "timeoutSec"} {
		if value, ok := rawInt(raw, field); ok {
			if value < 0 {
				return value, field, true
			}
			if value > 0 {
				return value, field, true
			}
			break
		}
	}
	if value, ok := rawInt(raw, "timeout"); ok {
		return value, "timeout", true
	}
	return 0, "", false
}

func setDecodedTimeout(
	in *ScanInput,
	seconds int,
	toolName string,
	field string,
) error {
	if seconds < 0 ||
		int64(seconds) > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf(
			"tool %q: field %q must be between 0 and %d seconds",
			toolName, field, math.MaxInt64/int64(time.Second),
		)
	}
	in.Timeout = time.Duration(seconds) * time.Second
	return nil
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

func firstNonEmptyString(
	raw map[string]any,
	keys ...string,
) string {
	for _, key := range keys {
		if value, ok := rawString(raw, key); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
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
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
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

// decodeCodeBlocks returns the code blocks at key. The accepted shapes
// mirror the codeexec tool's unmarshalCodeBlocks exactly: an array of
// {language, code} objects, a single such object, or a string holding
// double-encoded JSON for either of the above. A wrong type or malformed
// double-encoded JSON yields an error so the scanner denies rather than
// inspecting the wrong artifact.
func decodeCodeBlocks(raw map[string]any, key string) ([]CodeBlock, error) {
	v, ok := raw[key]
	if !ok {
		return nil, fmt.Errorf("field %q is required", key)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("field %q: %w", key, err)
	}

	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("field %q: %w", key, err)
	}
	if decoded == nil {
		return nil, nil
	}

	// The codeexec tool treats a string value as double-encoded JSON and
	// unwraps it into the declared code blocks. Mirror that here so the
	// permission check analyzes what will actually execute; labeling the
	// JSON text as a bash block would scan the wrapper, not the payload.
	if s, isString := decoded.(string); isString {
		encoded = []byte(s)
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return nil, fmt.Errorf("field %q: invalid double-encoded JSON: %w", key, err)
		}
	}
	switch decoded.(type) {
	case []any:
		var blocks []CodeBlock
		if err := json.Unmarshal(encoded, &blocks); err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		return blocks, nil
	case map[string]any:
		var block CodeBlock
		if err := json.Unmarshal(encoded, &block); err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		return []CodeBlock{block}, nil
	}
	return nil, fmt.Errorf(
		"field %q must be an array, an object, or a double-encoded JSON string, got %T",
		key, decoded,
	)
}
