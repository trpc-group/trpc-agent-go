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
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	internaltool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	workspaceExecutionDefaultTimeoutSeconds = 300
	skillExecutionDefaultTimeoutSeconds     = 300
	// sessionWriteDefaultYieldMilliseconds mirrors hostexec and workspaceexec.
	sessionWriteDefaultYieldMilliseconds = 200
	maxClosedSchemaDepth                 = 100
)

type decodedPermissionRequest struct {
	Request
	needsHumanReview bool
	stdin            string
}

// requestFromPermissionRequest translates framework tool arguments into the
// safety guard's execution-oriented input. It deliberately remains private so
// the owning tools' argument schemas stay authoritative.
func requestFromPermissionRequest(
	req *tool.PermissionRequest,
) (decodedPermissionRequest, bool, error) {
	if req == nil {
		return decodedPermissionRequest{}, false, nil
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
		return wrapDecodedPermissionRequest(decodeCodeExecution(base, req.Arguments))
	}

	switch name {
	case "workspace_exec":
		return decodeWorkspaceExecution(base, req.Arguments)
	case "exec_command":
		return wrapDecodedPermissionRequest(decodeHostExecution(base, req.Arguments))
	case "write_stdin":
		return decodeSessionWrite(base, req.Arguments, BackendHostExec)
	case "workspace_write_stdin":
		return decodeSessionWrite(base, req.Arguments, BackendWorkspaceExec)
	case "skill_run":
		return wrapDecodedPermissionRequest(decodeSkillExecution(base, req.Arguments, false))
	case "skill_exec":
		return wrapDecodedPermissionRequest(decodeSkillExecution(base, req.Arguments, true))
	case "skill_write_stdin":
		return decodeSkillWrite(base, req.Arguments)
	default:
		return wrapDecodedPermissionRequest(
			decodeUnknownExecution(base, declaration, req.Arguments),
		)
	}
}

func wrapDecodedPermissionRequest(
	req Request,
	ok bool,
	err error,
) (decodedPermissionRequest, bool, error) {
	return decodedPermissionRequest{Request: req}, ok, err
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
	Stdin         string            `json:"stdin,omitempty"`
	Background    bool              `json:"background,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func decodeWorkspaceExecution(
	base Request,
	raw []byte,
) (decodedPermissionRequest, bool, error) {
	var in workspaceExecutionArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode workspace execution arguments: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return decodedPermissionRequest{}, false,
			errors.New("decode workspace execution arguments: command is required")
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
	return decodedPermissionRequest{Request: base, stdin: in.Stdin}, true, nil
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

func decodeSkillWrite(
	base Request,
	raw []byte,
) (decodedPermissionRequest, bool, error) {
	var in skillWriteArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode skill write arguments: %w", err)
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return decodedPermissionRequest{}, false,
			errors.New("decode skill write arguments: session_id is required")
	}
	base.Backend = BackendWorkspaceExec
	if in.Chars == "" && !in.Submit {
		return decodedPermissionRequest{Request: base}, true, nil
	}
	return decodedPermissionRequest{
		Request:          base,
		needsHumanReview: true,
	}, true, nil
}

type sessionWriteArguments struct {
	SessionID     string          `json:"session_id,omitempty"`
	SessionIDOld  string          `json:"sessionId,omitempty"`
	Chars         string          `json:"chars,omitempty"`
	YieldOwner    json.RawMessage `json:"yield_time_ms,omitempty"`
	YieldAlias    json.RawMessage `json:"yieldMs,omitempty"`
	AppendNewline *bool           `json:"append_newline,omitempty"`
	Submit        *bool           `json:"submit,omitempty"`
}

func decodeSessionWrite(
	base Request,
	raw []byte,
	backend Backend,
) (decodedPermissionRequest, bool, error) {
	var in sessionWriteArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode session write arguments: %w", err)
	}
	if strings.TrimSpace(in.SessionID) == "" &&
		strings.TrimSpace(in.SessionIDOld) == "" {
		return decodedPermissionRequest{}, false,
			errors.New("decode session write arguments: session_id is required")
	}
	base.Backend = backend
	timeoutSeconds, err := sessionWriteTimeoutSeconds(
		in.YieldOwner,
		in.YieldAlias,
	)
	if err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode session write duration: %w", err)
	}
	base.TimeoutSeconds = timeoutSeconds
	return decodedPermissionRequest{
		Request: base,
		needsHumanReview: in.Chars != "" || firstBoolValue(
			in.AppendNewline,
			in.Submit,
		),
	}, true, nil
}

func sessionWriteTimeoutSeconds(values ...json.RawMessage) (int, error) {
	yieldMilliseconds := sessionWriteDefaultYieldMilliseconds
	selected := false
	for _, raw := range values {
		if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
			continue
		}
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		if !selected {
			yieldMilliseconds = value
			selected = true
		}
	}
	if yieldMilliseconds < 0 {
		yieldMilliseconds = sessionWriteDefaultYieldMilliseconds
	}
	if yieldMilliseconds <= 0 {
		return 0, nil
	}
	return (yieldMilliseconds-1)/1000 + 1, nil
}

func scanDecodedPermissionRequest(
	guard *Guard,
	req decodedPermissionRequest,
) Report {
	report := guard.Scan(req.Request)
	for _, finding := range scanSensitiveContent(req.stdin) {
		report = appendDecodedFinding(report, finding)
	}
	if !req.needsHumanReview {
		return report
	}
	finding := newFinding(
		DecisionNeedsHumanReview,
		RiskHigh,
		"session.interactive_input",
		"interactive session input can compose with prior process state",
		"review the complete session state before sending additional input",
	)
	return appendDecodedFinding(report, finding)
}

func appendDecodedFinding(report Report, finding Finding) Report {
	report.Findings = append(report.Findings, finding)
	if findingRank(finding) <= decisionRank(report.Decision)*10+riskRank(report.RiskLevel) {
		return report
	}
	report.Decision = finding.Decision
	report.RiskLevel = finding.RiskLevel
	report.RuleID = finding.RuleID
	report.Evidence = append([]string(nil), finding.Evidence...)
	report.Recommendation = finding.Recommendation
	report.Blocked = finding.Decision == DecisionDeny
	report.SafeSummary = "request requires safety policy action"
	return report
}

func decodeUnknownExecution(
	base Request,
	declaration *tool.Declaration,
	raw []byte,
) (Request, bool, error) {
	value, err := decodeRawValue(raw)
	if err != nil {
		return Request{}, false, fmt.Errorf("decode unknown tool arguments: %w", err)
	}
	if closedWorldNonExecution(base.Metadata, declaration, value) {
		return Request{}, false, nil
	}
	base.Backend = BackendUnknown
	base.RawArguments = append(json.RawMessage(nil), raw...)
	return base, true, nil
}

func closedWorldNonExecution(
	metadata tool.ToolMetadata,
	declaration *tool.Declaration,
	value any,
) bool {
	if !metadata.ReadOnly || metadata.OpenWorld || metadata.Destructive ||
		declaration == nil || declaration.InputSchema == nil {
		return false
	}
	return schemaIsClosed(declaration.InputSchema) &&
		!schemaHasExecutionProperty(declaration.InputSchema) &&
		closedValueMatchesSchema(declaration.InputSchema, value)
}

func schemaIsClosed(schema *tool.Schema) bool {
	return schemaIsClosedAt(schema, make(map[*tool.Schema]bool), 0)
}

func schemaIsClosedAt(
	schema *tool.Schema,
	active map[*tool.Schema]bool,
	depth int,
) bool {
	if depth >= maxClosedSchemaDepth || active[schema] {
		return false
	}
	if schema == nil || schema.Ref != "" || schema.Pattern != "" ||
		len(schema.Enum) > 0 || schema.Default != nil || len(schema.Defs) > 0 {
		return false
	}
	active[schema] = true
	defer delete(active, schema)
	switch schema.Type {
	case "object":
		allowsAdditional, explicitlyConfigured := schema.AdditionalProperties.(bool)
		if !explicitlyConfigured || allowsAdditional {
			return false
		}
		for _, property := range schema.Properties {
			if !schemaIsClosedAt(property, active, depth+1) {
				return false
			}
		}
		return true
	case "array":
		return schemaIsClosedAt(schema.Items, active, depth+1)
	case "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

func closedValueMatchesSchema(schema *tool.Schema, value any) bool {
	if !schemaIsClosed(schema) {
		return false
	}
	return closedValueMatchesSchemaAt(schema, value, 0)
}

func closedValueMatchesSchemaAt(schema *tool.Schema, value any, depth int) bool {
	if schema == nil || depth >= maxClosedSchemaDepth {
		return false
	}
	switch schema.Type {
	case "object":
		return closedObjectMatchesSchema(schema, value, depth)
	case "array":
		return closedArrayMatchesSchema(schema, value, depth)
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		return closedNumberMatchesSchema(value)
	case "integer":
		return closedIntegerMatchesSchema(value)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

func closedObjectMatchesSchema(schema *tool.Schema, value any, depth int) bool {
	object, ok := value.(map[string]any)
	if !ok || !closedObjectHasRequiredFields(schema, object) {
		return false
	}
	for name, item := range object {
		property, exists := schema.Properties[name]
		if !exists || !closedValueMatchesSchemaAt(property, item, depth+1) {
			return false
		}
	}
	return true
}

func closedObjectHasRequiredFields(schema *tool.Schema, object map[string]any) bool {
	for _, required := range schema.Required {
		if _, exists := object[required]; !exists {
			return false
		}
	}
	return true
}

func closedArrayMatchesSchema(schema *tool.Schema, value any, depth int) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if !closedValueMatchesSchemaAt(schema.Items, item, depth+1) {
			return false
		}
	}
	return true
}

func closedNumberMatchesSchema(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	_, err := strconv.ParseFloat(string(number), 64)
	return err == nil
}

func closedIntegerMatchesSchema(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	_, err := number.Int64()
	return err == nil
}

func schemaHasExecutionProperty(schema *tool.Schema) bool {
	hasExecution, safe := schemaHasExecutionPropertyAt(
		schema,
		make(map[*tool.Schema]bool),
		0,
	)
	return hasExecution || !safe
}

func schemaHasExecutionPropertyAt(
	schema *tool.Schema,
	active map[*tool.Schema]bool,
	depth int,
) (bool, bool) {
	if schema == nil {
		return false, true
	}
	if depth >= maxClosedSchemaDepth || active[schema] {
		return false, false
	}
	active[schema] = true
	defer delete(active, schema)
	for name, property := range schema.Properties {
		switch strings.ToLower(name) {
		case "command", "commands", "cmd", "script", "scripts", "shell",
			"args", "argv", "code", "code_blocks", "url", "uri", "endpoint",
			"destination", "cwd", "workdir", "working_directory", "env", "environment":
			return true, true
		}
		if hasExecution, safe := schemaHasExecutionPropertyAt(
			property, active, depth+1,
		); hasExecution || !safe {
			return hasExecution, safe
		}
	}
	return schemaHasExecutionPropertyAt(schema.Items, active, depth+1)
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
