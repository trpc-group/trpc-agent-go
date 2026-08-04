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
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	internaltool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	workspaceExecDefaultTimeoutSec = 300
	hostExecDefaultTimeoutSec      = 1800
	skillDefaultTimeoutSec         = 300
	skillDefaultOutputFileBytes    = 4 * 1024 * 1024
	skillDefaultOutputTotalBytes   = 64 * 1024 * 1024
	skillDefaultOutputGlob         = "out/**"
)

type parserKind string

const (
	parserUnknown       parserKind = "unknown"
	parserWorkspaceExec parserKind = "workspace_exec"
	parserHostExec      parserKind = "exec_command"
	parserSkillExec     parserKind = "skill_exec"
	parserWriteStdin    parserKind = "write_stdin"
	parserKillSession   parserKind = "kill_session"
	parserCodeExec      parserKind = "execute_code"
)

// requestsFromToolCall parses a PermissionRequest-like tool call payload into
// one or more scan requests. execute_code can produce one request per code block.
func requestsFromToolCall(
	toolName, toolCallID string,
	backend Backend,
	args []byte,
	metadata map[string]any,
) ([]ScanRequest, error) {
	return requestsFromToolCallWithParser(
		toolName,
		toolCallID,
		backend,
		args,
		metadata,
		parserKindFromToolName(toolName),
		nil,
	)
}

func requestsFromPermissionRequest(
	req *tool.PermissionRequest,
	backend Backend,
	metadata map[string]any,
) ([]ScanRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("permission request is required")
	}
	return requestsFromToolCallWithParser(
		req.ToolName,
		req.ToolCallID,
		backend,
		req.Arguments,
		metadata,
		parserKindForPermissionRequest(req),
		req.Tool,
	)
}

func requestsFromToolCallWithParser(
	toolName, toolCallID string,
	backend Backend,
	args []byte,
	metadata map[string]any,
	kind parserKind,
	parserTool tool.Tool,
) ([]ScanRequest, error) {
	canonicalToolName := normalizeToolName(toolName)
	if backend == "" {
		backend = inferBackendForParser(canonicalToolName, kind)
	}
	switch kind {
	case parserWorkspaceExec, parserHostExec, parserSkillExec:
		cwdField := "cwd"
		if kind == parserHostExec {
			cwdField = "workdir"
		}
		return parseExecArgs(toolName, kind, toolCallID, backend, args, cwdField, metadata, parserTool)
	case parserWriteStdin:
		return parseWriteStdinArgs(toolName, toolCallID, backend, args, metadata)
	case parserKillSession:
		return []ScanRequest{{
			ToolName:     toolName,
			ToolCallID:   toolCallID,
			Backend:      backend,
			RawArguments: append([]byte(nil), args...),
			Metadata:     metadata,
		}}, nil
	case parserCodeExec:
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

func parserKindFromToolName(toolName string) parserKind {
	switch normalizeToolName(toolName) {
	case "workspace_exec":
		return parserWorkspaceExec
	case "exec_command", "skill_run":
		if normalizeToolName(toolName) == "skill_run" {
			return parserSkillExec
		}
		return parserHostExec
	case "skill_exec":
		return parserSkillExec
	case "workspace_write_stdin", "write_stdin", "skill_write_stdin":
		return parserWriteStdin
	case "workspace_kill_session", "kill_session", "skill_kill_session":
		return parserKillSession
	case "execute_code":
		return parserCodeExec
	default:
		return parserUnknown
	}
}

func parserKindForPermissionRequest(req *tool.PermissionRequest) parserKind {
	if req == nil {
		return parserUnknown
	}
	if req.Tool != nil {
		return parserKindFromSemanticTool(internaltool.ResolveSemantic(req.Tool))
	}
	return parserKindFromToolName(req.ToolName)
}

func parserKindFromSemanticTool(t tool.Tool) parserKind {
	provider, ok := t.(tool.SafetyParserKindProvider)
	if !ok {
		return parserUnknown
	}
	kind := parserKind(provider.SafetyParserKind())
	switch kind {
	case parserWorkspaceExec, parserHostExec, parserSkillExec,
		parserWriteStdin, parserKillSession, parserCodeExec:
		return kind
	default:
		return parserUnknown
	}
}

func inferBackendForParser(toolName string, kind parserKind) Backend {
	switch kind {
	case parserWorkspaceExec:
		return BackendWorkspace
	case parserHostExec, parserSkillExec:
		return BackendHost
	case parserWriteStdin, parserKillSession:
		if strings.HasPrefix(toolName, "workspace_") {
			return BackendWorkspace
		}
		return BackendHost
	case parserCodeExec:
		return BackendCodeExec
	default:
		return inferBackend(toolName)
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
	toolName string, toolKind parserKind, toolCallID string,
	backend Backend,
	args []byte,
	cwdField string,
	metadata map[string]any,
	parserTool tool.Tool,
) ([]ScanRequest, error) {
	raw, err := normalizedJSONObject(args)
	if err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	command, err := stringField(raw, "command")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("command is required")
	}
	timeout, err := timeoutField(string(toolKind), raw)
	if err != nil {
		return nil, err
	}
	cwd, err := stringField(raw, cwdField)
	if err != nil {
		return nil, err
	}
	cwdResolved := true
	resolveHostCwd := toolKind == parserHostExec && parserTool != nil
	if toolKind == parserHostExec {
		cwd, cwdResolved, err = resolveEffectiveWorkdir(parserTool, cwd)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cwdField, err)
		}
	}
	var collectionPaths []string
	var inputPaths []string
	var requestedOutputBytes int64
	if toolKind == parserSkillExec {
		collectionPaths, err = collectionPathsField(raw)
		if err != nil {
			return nil, err
		}
		inputPaths, err = inputPathsField(raw)
		if err != nil {
			return nil, err
		}
		requestedOutputBytes, err = outputLimitField(raw)
		if err != nil {
			return nil, err
		}
		if skillUsesImplicitOutputCollection(raw) {
			collectionPaths = []string{skillDefaultOutputGlob}
			requestedOutputBytes = skillDefaultOutputTotalBytes
		}
	}
	env, err := stringMapField(raw, "env")
	if err != nil {
		return nil, err
	}
	stdin, err := stringField(raw, "stdin")
	if err != nil {
		return nil, err
	}
	editorText, err := stringField(raw, "editor_text")
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
		ToolName:              toolName,
		ToolCallID:            toolCallID,
		Backend:               backend,
		Command:               command,
		Cwd:                   cwd,
		Env:                   env,
		Stdin:                 stdin,
		EditorText:            editorText,
		TimeoutSec:            timeout,
		Background:            background,
		TTY:                   tty,
		RawArguments:          append([]byte(nil), args...),
		CollectionPaths:       collectionPaths,
		InputPaths:            inputPaths,
		RequestedOutputBytes:  requestedOutputBytes,
		Metadata:              metadata,
		cwdResolutionRequired: resolveHostCwd,
		cwdResolved:           cwdResolved,
	}
	return []ScanRequest{req}, nil
}

func outputLimitField(raw map[string]json.RawMessage) (int64, error) {
	outputFiles, err := stringSliceField(raw, "output_files")
	if err != nil {
		return 0, err
	}
	limit := int64(0)
	if len(outputFiles) > 0 {
		limit = skillDefaultOutputTotalBytes
	}
	outputs, ok, err := outputSpecField(raw)
	if err != nil {
		return 0, err
	}
	if !ok || len(outputs.Globs) == 0 {
		return limit, nil
	}
	maxFileBytes := outputs.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = skillDefaultOutputFileBytes
	}
	maxTotalBytes := outputs.MaxTotalBytes
	if maxTotalBytes <= 0 {
		maxTotalBytes = skillDefaultOutputTotalBytes
	}
	if int64(maxFileBytes) > limit {
		limit = int64(maxFileBytes)
	}
	if int64(maxTotalBytes) > limit {
		limit = int64(maxTotalBytes)
	}
	return limit, nil
}

func skillUsesImplicitOutputCollection(raw map[string]json.RawMessage) bool {
	outputFiles, err := stringSliceField(raw, "output_files")
	if err != nil || len(outputFiles) > 0 {
		return false
	}
	outputs, ok := raw["outputs"]
	return !ok || string(outputs) == "null"
}

type inputPathSpec struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func inputPathsField(raw map[string]json.RawMessage) ([]string, error) {
	b, ok := raw["inputs"]
	if !ok || string(b) == "null" {
		return nil, nil
	}
	var inputs []inputPathSpec
	if err := json.Unmarshal(b, &inputs); err != nil {
		return nil, fmt.Errorf("inputs: expected input spec array: %w", err)
	}
	paths := make([]string, 0, len(inputs)*2)
	for _, input := range inputs {
		if strings.TrimSpace(input.From) != "" {
			paths = append(paths, input.From)
		}
		if strings.TrimSpace(input.To) != "" {
			paths = append(paths, input.To)
		}
	}
	return dedupeStrings(paths), nil
}

func resolveEffectiveWorkdir(parserTool tool.Tool, raw string) (string, bool, error) {
	semantic := internaltool.ResolveSemantic(parserTool)
	if resolver, ok := semantic.(interface {
		EffectiveWorkdir(string) (string, error)
	}); ok {
		resolved, err := resolver.EffectiveWorkdir(raw)
		return resolved, err == nil, err
	}
	return raw, filepath.IsAbs(strings.TrimSpace(raw)), nil
}

func collectionPathsField(raw map[string]json.RawMessage) ([]string, error) {
	paths, err := stringSliceField(raw, "output_files")
	if err != nil {
		return nil, err
	}
	if outputs, ok, err := outputSpecField(raw); err != nil {
		return nil, err
	} else if ok {
		paths = append(paths, outputs.Globs...)
	}
	return dedupeStrings(paths), nil
}

func outputSpecField(raw map[string]json.RawMessage) (codeexecutor.OutputSpec, bool, error) {
	outputRaw, ok := raw["outputs"]
	if !ok || strings.TrimSpace(string(outputRaw)) == "null" {
		return codeexecutor.OutputSpec{}, false, nil
	}
	var outputs codeexecutor.OutputSpec
	if err := json.Unmarshal(outputRaw, &outputs); err != nil {
		return codeexecutor.OutputSpec{}, false, fmt.Errorf("outputs: expected object: %w", err)
	}
	return outputs, true, nil
}

func stringSliceField(raw map[string]json.RawMessage, key string) ([]string, error) {
	b, ok := raw[key]
	if !ok || string(b) == "null" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: expected string array: %w", key, err)
	}
	return out, nil
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func parseWriteStdinArgs(
	toolName, toolCallID string,
	backend Backend,
	args []byte,
	metadata map[string]any,
) ([]ScanRequest, error) {
	raw, err := normalizedJSONObject(args)
	if err != nil {
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
		timeout, err = intField(raw, "timeout_sec", "timeoutsec")
		if err != nil {
			return 0, err
		}
		if timeout <= 0 {
			timeout, err = intField(raw, "timeout")
		}
	case "exec_command":
		// exec_command does not expose the workspace_exec timeout alias.
		timeout, err = intField(raw, "timeout_sec", "timeoutsec")
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

func normalizedJSONObject(data []byte) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	normalized := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		canonical := strings.ToLower(key)
		if _, exists := normalized[canonical]; exists {
			return nil, fmt.Errorf("duplicate case-insensitive field %q", key)
		}
		normalized[canonical] = value
	}
	return normalized, nil
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
