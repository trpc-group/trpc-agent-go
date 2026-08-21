//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	reviewRuleID     string
	reviewEvidence   string
	executableStdin  string
	sensitiveContent []string
	paths            []string
	outputGlobs      []string
	additionalArgs   json.RawMessage
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
	frameworkOwned := frameworkOwnedExecutionTool(req.Tool)
	if isCodeExecutionDeclaration(name, declaration) {
		decoded, ok, err := wrapDecodedPermissionRequest(
			decodeCodeExecution(base, req.Arguments),
		)
		return attachAdditionalArguments(
			decoded, ok, err, req.Arguments, codeExecutionArguments{}, declaration,
			frameworkOwned,
		)
	}

	var (
		decoded decodedPermissionRequest
		ok      bool
		err     error
		known   any
	)
	switch name {
	case "workspace_exec":
		decoded, ok, err = decodeWorkspaceExecution(base, req.Arguments)
		known = workspaceExecutionArguments{}
	case "exec_command":
		decoded, ok, err = decodeHostExecution(base, req.Arguments)
		known = hostExecutionArguments{}
	case "write_stdin":
		decoded, ok, err = decodeSessionWrite(base, req.Arguments, BackendHostExec)
		known = sessionWriteArguments{}
	case "workspace_write_stdin":
		decoded, ok, err = decodeSessionWrite(
			base, req.Arguments, BackendWorkspaceExec,
		)
		known = sessionWriteArguments{}
	case "skill_run":
		decoded, ok, err = decodeSkillExecution(base, req.Arguments, false)
		known = skillExecutionArguments{}
	case "skill_exec":
		decoded, ok, err = decodeSkillExecution(base, req.Arguments, true)
		known = skillExecutionArguments{}
	case "skill_write_stdin":
		decoded, ok, err = decodeSkillWrite(base, req.Arguments)
		known = skillWriteArguments{}
	case "skill_poll_session":
		decoded, ok, err = decodeSkillPoll(base, req.Arguments)
		known = skillPollArguments{}
	default:
		return wrapDecodedPermissionRequest(
			decodeUnknownExecution(base, declaration, req.Arguments),
		)
	}
	return attachAdditionalArguments(
		decoded, ok, err, req.Arguments, known, declaration, frameworkOwned,
	)
}

func frameworkOwnedExecutionTool(candidate tool.Tool) bool {
	semantic := internaltool.ResolveSemantic(candidate)
	if semantic == nil {
		return false
	}
	typeOf := reflect.TypeOf(semantic)
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	switch typeOf.PkgPath() {
	case "trpc.group/trpc-go/trpc-agent-go/tool/codeexec",
		"trpc.group/trpc-go/trpc-agent-go/tool/hostexec",
		"trpc.group/trpc-go/trpc-agent-go/tool/skill",
		"trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec":
		return true
	default:
		return false
	}
}

func attachAdditionalArguments(
	decoded decodedPermissionRequest,
	ok bool,
	err error,
	raw []byte,
	known any,
	declaration *tool.Declaration,
	frameworkOwned bool,
) (decodedPermissionRequest, bool, error) {
	if err != nil || !ok {
		return decoded, ok, err
	}
	remaining, err := remainingArguments(
		raw, known, declaration, frameworkOwned,
	)
	if err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode additional tool arguments: %w", err)
	}
	decoded.additionalArgs = remaining
	return decoded, true, nil
}

// remainingArguments prevents a specialized decoder from silently discarding
// declared open-world fields while keeping the raw payload out of public
// reports. Undeclared fields are ignored consistently with the owning tool's
// schema and JSON decoder.
func remainingArguments(
	raw []byte,
	known any,
	declaration *tool.Declaration,
	frameworkOwned bool,
) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	typeOf := reflect.TypeOf(known)
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	for index := 0; index < typeOf.NumField(); index++ {
		name := strings.SplitN(typeOf.Field(index).Tag.Get("json"), ",", 2)[0]
		if name != "" && name != "-" {
			deleteFoldedJSONFields(fields, name)
		}
	}
	if declaration != nil && declaration.InputSchema != nil {
		allowsAdditional, configured := declaration.InputSchema.
			AdditionalProperties.(bool)
		if frameworkOwned || configured && !allowsAdditional {
			for name := range fields {
				if !schemaDeclaresJSONField(declaration.InputSchema, name) {
					delete(fields, name)
				}
			}
		}
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return json.Marshal(fields)
}

func deleteFoldedJSONFields(fields map[string]json.RawMessage, name string) {
	for candidate := range fields {
		if strings.EqualFold(candidate, name) {
			delete(fields, candidate)
		}
	}
}

func schemaDeclaresJSONField(schema *tool.Schema, name string) bool {
	if _, exact := schema.Properties[name]; exact {
		return true
	}
	for declared := range schema.Properties {
		if strings.EqualFold(declared, name) {
			return true
		}
	}
	return false
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
	YieldTimeMS   *int              `json:"yield_time_ms,omitempty"`
	YieldMs       *int              `json:"yieldMs,omitempty"`
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
	decoded := decodedPermissionRequest{
		Request: base, executableStdin: in.Stdin,
		sensitiveContent: []string{in.Stdin},
	}
	if yield := firstIntPointer(in.YieldTimeMS, in.YieldMs); yield != nil && *yield != 0 {
		decoded.needsHumanReview = true
		decoded.reviewRuleID = "workspace.session"
		decoded.reviewEvidence = "workspace execution requested an explicit session yield"
	}
	return decoded, true, nil
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
	YieldTimeMS   *int              `json:"yield_time_ms,omitempty"`
	YieldMs       *int              `json:"yieldMs,omitempty"`
}

func decodeHostExecution(
	base Request,
	raw []byte,
) (decodedPermissionRequest, bool, error) {
	var in hostExecutionArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode host execution arguments: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return decodedPermissionRequest{}, false,
			errors.New("decode host execution arguments: command is required")
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
	decoded := decodedPermissionRequest{Request: base}
	if yield := firstIntPointer(in.YieldTimeMS, in.YieldMs); yield == nil || *yield != 0 {
		decoded.needsHumanReview = true
		decoded.reviewRuleID = "host.session"
		decoded.reviewEvidence = "host execution may persist after its session yield window"
	}
	return decoded, true, nil
}

func isCodeExecutionDeclaration(name string, declaration *tool.Declaration) bool {
	if name == "execute_code" {
		return true
	}
	return declaration != nil && declaration.InputSchema != nil &&
		declaration.InputSchema.Properties["code_blocks"] != nil
}

type codeExecutionArguments struct {
	CodeBlocks  json.RawMessage `json:"code_blocks"`
	ExecutionID string          `json:"execution_id,omitempty"`
}

func decodeCodeExecution(base Request, raw []byte) (Request, bool, error) {
	var outer codeExecutionArguments
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
	Skill          string                   `json:"skill"`
	Command        string                   `json:"command"`
	Cwd            string                   `json:"cwd,omitempty"`
	Env            map[string]string        `json:"env,omitempty"`
	Stdin          string                   `json:"stdin,omitempty"`
	EditorText     string                   `json:"editor_text,omitempty"`
	Inputs         []codeexecutor.InputSpec `json:"inputs,omitempty"`
	OutputFiles    []string                 `json:"output_files,omitempty"`
	Outputs        *codeexecutor.OutputSpec `json:"outputs,omitempty"`
	Timeout        int                      `json:"timeout,omitempty"`
	TTY            bool                     `json:"tty,omitempty"`
	YieldMS        json.RawMessage          `json:"yield_ms,omitempty"`
	PollLines      int                      `json:"poll_lines,omitempty"`
	SaveArtifacts  bool                     `json:"save_as_artifacts,omitempty"`
	OmitInline     bool                     `json:"omit_inline_content,omitempty"`
	ArtifactPrefix string                   `json:"artifact_prefix,omitempty"`
}

func decodeSkillExecution(
	base Request,
	raw []byte,
	interactive bool,
) (decodedPermissionRequest, bool, error) {
	var in skillExecutionArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode skill execution arguments: %w", err)
	}
	if strings.TrimSpace(in.Skill) == "" || strings.TrimSpace(in.Command) == "" {
		return decodedPermissionRequest{}, false,
			errors.New("decode skill execution arguments: skill and command are required")
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = skillExecutionDefaultTimeoutSeconds
	}
	yieldTimeout, err := sessionWriteTimeoutSeconds(in.YieldMS)
	if err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode skill execution duration: %w", err)
	}
	if yieldTimeout > timeout {
		timeout = yieldTimeout
	}
	base.Backend = BackendWorkspaceExec
	base.Command = in.Command
	base.Cwd = in.Cwd
	base.Env = in.Env
	base.TimeoutSeconds = timeout
	base.TTY = interactive && in.TTY
	decoded := decodedPermissionRequest{
		Request:          base,
		executableStdin:  in.Stdin,
		sensitiveContent: []string{in.Stdin, in.EditorText},
	}
	if interactive {
		decoded.needsHumanReview = true
		decoded.reviewRuleID = "skill.session"
		decoded.reviewEvidence = "skill_exec creates a persistent interactive session"
	}
	for _, input := range in.Inputs {
		decoded.paths = append(
			decoded.paths, skillInputPath(input.From), input.To,
		)
	}
	decoded.outputGlobs = append(decoded.outputGlobs, in.OutputFiles...)
	if in.Outputs != nil {
		decoded.outputGlobs = append(decoded.outputGlobs, in.Outputs.Globs...)
	}
	return decoded, true, nil
}

func skillInputPath(value string) string {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "host://") {
		return trimmed[len("host://"):]
	}
	for _, scheme := range []string{"workspace://", "skill://"} {
		if strings.HasPrefix(lower, scheme) {
			return strings.TrimPrefix(trimmed[len(scheme):], "/")
		}
	}
	return trimmed
}

type skillWriteArguments struct {
	SessionID string          `json:"session_id"`
	Chars     string          `json:"chars,omitempty"`
	Submit    bool            `json:"submit,omitempty"`
	YieldMS   json.RawMessage `json:"yield_ms,omitempty"`
	PollLines int             `json:"poll_lines,omitempty"`
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
	timeoutSeconds, err := sessionWriteTimeoutSeconds(in.YieldMS)
	if err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode skill write duration: %w", err)
	}
	base.TimeoutSeconds = timeoutSeconds
	if in.Chars == "" && !in.Submit {
		return decodedPermissionRequest{Request: base}, true, nil
	}
	return decodedPermissionRequest{
		Request:          base,
		needsHumanReview: true,
		sensitiveContent: []string{in.Chars},
	}, true, nil
}

type skillPollArguments struct {
	SessionID string          `json:"session_id"`
	YieldMS   json.RawMessage `json:"yield_ms,omitempty"`
	PollLines int             `json:"poll_lines,omitempty"`
}

func decodeSkillPoll(
	base Request,
	raw []byte,
) (decodedPermissionRequest, bool, error) {
	var in skillPollArguments
	if err := json.Unmarshal(raw, &in); err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode skill poll arguments: %w", err)
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return decodedPermissionRequest{}, false,
			errors.New("decode skill poll arguments: session_id is required")
	}
	base.Backend = BackendWorkspaceExec
	timeoutSeconds, err := sessionWriteTimeoutSeconds(in.YieldMS)
	if err != nil {
		return decodedPermissionRequest{}, false,
			fmt.Errorf("decode skill poll duration: %w", err)
	}
	base.TimeoutSeconds = timeoutSeconds
	return decodedPermissionRequest{Request: base}, true, nil
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
		sensitiveContent: []string{in.Chars},
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
	additionalFindings := scanRawArguments(guard.policy, req.additionalArgs)
	for _, finding := range additionalFindings {
		report = appendDecodedFinding(report, finding)
	}
	if len(bytes.TrimSpace(req.additionalArgs)) > 0 &&
		len(additionalFindings) == 0 {
		report = appendDecodedFinding(report, newFinding(
			DecisionNeedsHumanReview,
			RiskHigh,
			"arguments.additional_fields",
			"tool arguments contain fields outside the selected execution schema",
			"review the complete tool schema and all execution-affecting fields",
		))
	}
	for _, finding := range scanExecutableStdin(
		guard.policy, req.Command, req.executableStdin,
	) {
		report = appendDecodedFinding(report, finding)
	}
	for _, content := range req.sensitiveContent {
		contentFindings := scanSensitiveContent(content)
		if len(contentFindings) > 0 {
			report.Redacted = true
		}
		for _, finding := range contentFindings {
			report = appendDecodedFinding(report, finding)
		}
	}
	for _, candidate := range req.paths {
		if finding, denied := deniedPathFinding(
			guard.policy.DeniedPaths, candidate,
		); denied {
			report = appendDecodedFinding(report, finding)
		}
	}
	for _, pattern := range req.outputGlobs {
		if finding, unsafe := outputGlobFinding(
			guard.policy.DeniedPaths, pattern,
		); unsafe {
			report = appendDecodedFinding(report, finding)
		}
	}
	if req.needsHumanReview {
		ruleID := req.reviewRuleID
		if ruleID == "" {
			ruleID = "session.interactive_input"
		}
		evidence := req.reviewEvidence
		if evidence == "" {
			evidence = "interactive session input can compose with prior process state"
		}
		finding := newFinding(
			DecisionNeedsHumanReview,
			RiskHigh,
			ruleID,
			evidence,
			"review the complete session state before sending additional input",
		)
		report = appendDecodedFinding(report, finding)
	}
	redactReport(&report)
	return report
}

func appendDecodedFinding(report Report, finding Finding) Report {
	report.Findings = append(report.Findings, finding)
	if findingContainsSensitiveInput(finding) {
		report.Redacted = true
	}
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
		if pathKey(name) {
			return true, true
		}
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
