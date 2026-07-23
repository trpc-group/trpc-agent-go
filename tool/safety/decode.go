//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	internaltool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	workspaceExecutionDefaultTimeoutSeconds = 300
	skillExecutionDefaultTimeoutSeconds     = 300
)

// requestFromPermissionRequest translates framework tool arguments into the
// safety guard's execution-oriented input. It deliberately remains private so
// the owning tools' argument schemas stay authoritative.
func requestFromPermissionRequest(req *tool.PermissionRequest) (Request, bool, error) {
	if req == nil {
		return Request{}, false, nil
	}

	declaration := semanticDeclaration(req)
	name := ""
	if declaration != nil {
		name = declaration.Name
	}

	base := Request{
		ToolName: permissionToolName(req),
		Metadata: req.Metadata,
	}
	if isCodeExecutionDeclaration(name, declaration) {
		return decodeCodeExecution(base, req.Arguments)
	}

	switch name {
	case "workspace_exec":
		return decodeWorkspaceExecution(base, req.Arguments)
	case "exec_command":
		return decodeHostExecution(base, req.Arguments)
	case "skill_run":
		return decodeSkillExecution(base, req.Arguments, false)
	case "skill_exec":
		return decodeSkillExecution(base, req.Arguments, true)
	case "skill_write_stdin":
		return decodeSkillWrite(base, req.Arguments)
	default:
		return decodeUnknownExecution(base, declaration, req.Arguments)
	}
}

func semanticDeclaration(req *tool.PermissionRequest) *tool.Declaration {
	semantic := internaltool.ResolveSemantic(req.Tool)
	if semantic != nil {
		return semantic.Declaration()
	}
	if req.Declaration != nil {
		return req.Declaration
	}
	if req.ToolName != "" {
		return &tool.Declaration{Name: req.ToolName}
	}
	return nil
}

func permissionToolName(req *tool.PermissionRequest) string {
	if req.ToolName != "" {
		return req.ToolName
	}
	if req.Declaration != nil && req.Declaration.Name != "" {
		return req.Declaration.Name
	}
	if req.Tool != nil {
		if declaration := req.Tool.Declaration(); declaration != nil {
			return declaration.Name
		}
	}
	return ""
}

type workspaceExecutionArguments struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Background    bool              `json:"background,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func decodeWorkspaceExecution(base Request, raw []byte) (Request, bool, error) {
	var in workspaceExecutionArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return Request{}, false, fmt.Errorf("decode workspace execution arguments: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return Request{}, false, errors.New("decode workspace execution arguments: command is required")
	}
	timeout := firstIntValue(in.TimeoutSec, in.TimeoutSecOld)
	if timeout <= 0 {
		timeout = in.Timeout
	}
	if timeout <= 0 {
		timeout = workspaceExecutionDefaultTimeoutSeconds
	}
	base.Backend = BackendWorkspaceExec
	base.Command = in.Command
	base.Cwd = in.Cwd
	base.Env = in.Env
	base.TimeoutSeconds = timeout
	base.Background = in.Background
	base.TTY = firstBoolValue(in.TTY, in.PTY)
	return base, true, nil
}

type hostExecutionArguments struct {
	Command       string            `json:"command"`
	Workdir       string            `json:"workdir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Background    bool              `json:"background,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func decodeHostExecution(base Request, raw []byte) (Request, bool, error) {
	var in hostExecutionArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return Request{}, false, fmt.Errorf("decode host execution arguments: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return Request{}, false, errors.New("decode host execution arguments: command is required")
	}
	timeout := hostExecDefaultTimeoutSeconds
	if configured := firstIntPointer(in.TimeoutSec, in.TimeoutSecOld); configured != nil && *configured > 0 {
		timeout = *configured
	}
	base.Backend = BackendHostExec
	base.Command = in.Command
	base.Cwd = in.Workdir
	base.Env = in.Env
	base.TimeoutSeconds = timeout
	base.Background = in.Background
	base.TTY = firstBoolValue(in.TTY, in.PTY)
	return base, true, nil
}

func isCodeExecutionDeclaration(name string, declaration *tool.Declaration) bool {
	if name == "execute_code" {
		return true
	}
	return declaration != nil && declaration.InputSchema != nil &&
		declaration.InputSchema.Properties["code_blocks"] != nil
}

func decodeCodeExecution(base Request, raw []byte) (Request, bool, error) {
	var outer struct {
		CodeBlocks json.RawMessage `json:"code_blocks"`
	}
	if err := json.Unmarshal(raw, &outer); err != nil {
		return Request{}, false, fmt.Errorf("decode code execution arguments: %w", err)
	}
	blocks, err := decodeCodeBlocks(outer.CodeBlocks)
	if err != nil {
		return Request{}, false, fmt.Errorf("decode code execution arguments: %w", err)
	}
	if len(blocks) == 0 {
		return Request{}, false, errors.New("decode code execution arguments: code_blocks is required")
	}
	base.Backend = BackendCodeExec
	base.CodeBlocks = blocks
	return base, true, nil
}

func decodeCodeBlocks(raw json.RawMessage) ([]codeexecutor.CodeBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	value, err := decodeRawValue(raw)
	if err != nil {
		return nil, err
	}
	if encoded, ok := value.(string); ok {
		raw = json.RawMessage(encoded)
		value, err = decodeRawValue(raw)
		if err != nil {
			return nil, err
		}
	}

	switch value.(type) {
	case []any:
		var blocks []codeexecutor.CodeBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	case map[string]any:
		var block codeexecutor.CodeBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		return []codeexecutor.CodeBlock{block}, nil
	default:
		return nil, fmt.Errorf("code_blocks must be an array, object, or encoded JSON string")
	}
}

type skillExecutionArguments struct {
	Skill   string            `json:"skill"`
	Command string            `json:"command"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
	TTY     bool              `json:"tty,omitempty"`
}

func decodeSkillExecution(base Request, raw []byte, interactive bool) (Request, bool, error) {
	var in skillExecutionArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return Request{}, false, fmt.Errorf("decode skill execution arguments: %w", err)
	}
	if strings.TrimSpace(in.Skill) == "" || strings.TrimSpace(in.Command) == "" {
		return Request{}, false, errors.New("decode skill execution arguments: skill and command are required")
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = skillExecutionDefaultTimeoutSeconds
	}
	base.Backend = BackendWorkspaceExec
	base.Command = in.Command
	base.Cwd = in.Cwd
	base.Env = in.Env
	base.TimeoutSeconds = timeout
	base.TTY = interactive && in.TTY
	return base, true, nil
}

type skillWriteArguments struct {
	SessionID string `json:"session_id"`
	Chars     string `json:"chars,omitempty"`
	Submit    bool   `json:"submit,omitempty"`
}

func decodeSkillWrite(base Request, raw []byte) (Request, bool, error) {
	var in skillWriteArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return Request{}, false, fmt.Errorf("decode skill write arguments: %w", err)
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return Request{}, false, errors.New("decode skill write arguments: session_id is required")
	}
	base.Backend = BackendWorkspaceExec
	if in.Chars == "" && !in.Submit {
		return base, true, nil
	}
	base.Command = in.Chars
	if in.Submit {
		base.Command += "\n"
	}
	// Writing arbitrary text resumes an already-running interactive process.
	// Model it as the stricter persistent-session boundary so Guard requires
	// review even when the text itself resembles an innocuous command.
	base.Backend = BackendHostExec
	base.TimeoutSeconds = skillExecutionDefaultTimeoutSeconds
	base.TTY = true
	return base, true, nil
}

func decodeUnknownExecution(
	base Request,
	declaration *tool.Declaration,
	raw []byte,
) (Request, bool, error) {
	if _, err := decodeRawValue(raw); err != nil {
		return Request{}, false, fmt.Errorf("decode unknown tool arguments: %w", err)
	}
	if closedWorldNonExecution(base.Metadata, declaration) {
		return Request{}, false, nil
	}
	base.Backend = BackendUnknown
	base.RawArguments = append(json.RawMessage(nil), raw...)
	return base, true, nil
}

func closedWorldNonExecution(metadata tool.ToolMetadata, declaration *tool.Declaration) bool {
	if !metadata.ReadOnly || metadata.OpenWorld || metadata.Destructive ||
		declaration == nil || declaration.InputSchema == nil {
		return false
	}
	return schemaIsClosed(declaration.InputSchema) &&
		!schemaHasExecutionProperty(declaration.InputSchema)
}

func schemaIsClosed(schema *tool.Schema) bool {
	if schema == nil || schema.Ref != "" {
		return false
	}
	switch schema.Type {
	case "object":
		allowsAdditional, explicitlyConfigured := schema.AdditionalProperties.(bool)
		if !explicitlyConfigured || allowsAdditional {
			return false
		}
		for _, property := range schema.Properties {
			if !schemaIsClosed(property) {
				return false
			}
		}
		return true
	case "array":
		return schemaIsClosed(schema.Items)
	case "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

func schemaHasExecutionProperty(schema *tool.Schema) bool {
	if schema == nil {
		return false
	}
	for name, property := range schema.Properties {
		switch strings.ToLower(name) {
		case "command", "commands", "cmd", "script", "scripts", "shell",
			"args", "argv", "code", "code_blocks", "url", "uri", "endpoint",
			"destination", "cwd", "workdir", "working_directory", "env", "environment":
			return true
		}
		if schemaHasExecutionProperty(property) {
			return true
		}
	}
	return schemaHasExecutionProperty(schema.Items)
}

func firstIntValue(values ...*int) int {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}

func firstIntPointer(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstBoolValue(values ...*bool) bool {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return false
}
