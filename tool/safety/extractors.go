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
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func defaultExtractors() map[string]Extractor {
	return map[string]Extractor{
		"workspace_exec":        ExtractorFunc(extractWorkspaceExec),
		"exec_command":          ExtractorFunc(extractHostExec),
		"execute_code":          ExtractorFunc(extractCode),
		"skill_exec":            ExtractorFunc(extractSkillExec),
		"skill_run":             ExtractorFunc(extractSkillRun),
		"write_stdin":           ExtractorFunc(extractHostWriteStdin),
		"kill_session":          ExtractorFunc(extractHostKillSession),
		"skill_write_stdin":     ExtractorFunc(extractSkillWriteStdin),
		"skill_poll_session":    ExtractorFunc(extractSkillPollSession),
		"skill_kill_session":    ExtractorFunc(extractSkillKillSession),
		"workspace_write_stdin": ExtractorFunc(extractWorkspaceWriteStdin),
		"workspace_kill_session": ExtractorFunc(
			extractWorkspaceKillSession,
		),
	}
}

type commandArguments struct {
	Command       string            `json:"command"`
	Stdin         string            `json:"stdin,omitempty"`
	CWD           string            `json:"cwd,omitempty"`
	Workdir       string            `json:"workdir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	Background    bool              `json:"background,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
	YieldTimeMS   *int              `json:"yield-time_ms,omitempty"`
	YieldMSOld    *int              `json:"yieldMs,omitempty"`
}

func extractWorkspaceExec(req *tool.PermissionRequest) (Request, bool, error) {
	return extractCommand(req, "workspace_exec", BackendWorkspace, false)
}

func extractHostExec(req *tool.PermissionRequest) (Request, bool, error) {
	return extractCommand(req, "exec_command", BackendHost, true)
}

func extractCommand(
	req *tool.PermissionRequest,
	name string,
	backend Backend,
	useWorkdir bool,
) (Request, bool, error) {
	if req == nil || req.ToolName != name {
		return Request{}, false, nil
	}
	var args commandArguments
	if err := decodeStrictToolArguments(
		req.Arguments,
		&args,
		commandArgumentFields(name),
	); err != nil {
		return Request{}, true, fmt.Errorf("decode %s arguments: %w", name, err)
	}
	timeoutSec := args.Timeout
	if args.TimeoutSec != nil {
		timeoutSec = *args.TimeoutSec
	} else if args.TimeoutSecOld != nil {
		timeoutSec = *args.TimeoutSecOld
	}
	cwd := args.CWD
	if useWorkdir {
		cwd = args.Workdir
	}
	return Request{
		ToolName:     req.ToolName,
		ToolCallID:   req.ToolCallID,
		Backend:      backend,
		Command:      args.Command,
		CWD:          cwd,
		SessionInput: args.Stdin,
		YieldMS:      firstIntPointer(args.YieldTimeMS, args.YieldMSOld),
		Env:          args.Env,
		Timeout:      saturatedDuration(int64(timeoutSec), time.Second),
		Background:   args.Background,
		TTY:          firstBool(args.TTY, args.PTY),
		Metadata:     req.Metadata,
	}, true, nil
}

type skillRunArguments struct {
	Skill          string                   `json:"skill"`
	Command        string                   `json:"command"`
	CWD            string                   `json:"cwd,omitempty"`
	Stdin          string                   `json:"stdin,omitempty"`
	Env            map[string]string        `json:"env,omitempty"`
	Timeout        int                      `json:"timeout,omitempty"`
	TTY            bool                     `json:"tty,omitempty"`
	YieldMS        *int                     `json:"yield_ms,omitempty"`
	PollLines      int                      `json:"poll_lines,omitempty"`
	EditorText     string                   `json:"editor_text,omitempty"`
	Inputs         []codeexecutor.InputSpec `json:"inputs,omitempty"`
	OutputFiles    []string                 `json:"output_files,omitempty"`
	Outputs        *codeexecutor.OutputSpec `json:"outputs,omitempty"`
	SaveArtifacts  bool                     `json:"save_as_artifacts,omitempty"`
	OmitInline     bool                     `json:"omit_inline_content,omitempty"`
	ArtifactPrefix string                   `json:"artifact_prefix,omitempty"`
}

func extractSkillRun(req *tool.PermissionRequest) (Request, bool, error) {
	return extractSkillCommand(req, "skill_run", false)
}

func extractSkillExec(req *tool.PermissionRequest) (Request, bool, error) {
	return extractSkillCommand(req, "skill_exec", true)
}

func extractSkillCommand(
	req *tool.PermissionRequest,
	name string,
	interactive bool,
) (Request, bool, error) {
	if req == nil || req.ToolName != name {
		return Request{}, false, nil
	}
	var args skillRunArguments
	if err := validateSkillNestedArguments(req.Arguments); err != nil {
		return Request{}, true, fmt.Errorf("decode %s arguments: %w", name, err)
	}
	if err := decodeStrictToolArguments(
		req.Arguments,
		&args,
		skillArgumentFields(interactive),
	); err != nil {
		return Request{}, true, fmt.Errorf("decode %s arguments: %w", name, err)
	}
	normalized := Request{
		ToolName:       req.ToolName,
		ToolCallID:     req.ToolCallID,
		Backend:        BackendSkill,
		Skill:          args.Skill,
		Command:        args.Command,
		CWD:            args.CWD,
		SessionInput:   args.Stdin,
		EditorText:     args.EditorText,
		Inputs:         normalizeInputSpecs(args.Inputs),
		OutputFiles:    append([]string(nil), args.OutputFiles...),
		Outputs:        normalizeOutputSpec(args.Outputs),
		SaveArtifacts:  args.SaveArtifacts,
		OmitInline:     args.OmitInline,
		ArtifactPrefix: args.ArtifactPrefix,
		Env:            args.Env,
		Timeout:        saturatedDuration(int64(args.Timeout), time.Second),
		TTY:            interactive && args.TTY,
		Metadata:       req.Metadata,
	}
	if interactive {
		normalized.YieldMS = args.YieldMS
		normalized.PollLines = args.PollLines
	}
	return normalized, true, nil
}

func normalizeInputSpecs(specs []codeexecutor.InputSpec) []InputSpec {
	if len(specs) == 0 {
		return nil
	}
	normalized := make([]InputSpec, len(specs))
	for index, spec := range specs {
		normalized[index] = InputSpec{
			From: spec.From,
			To:   spec.To,
			Mode: spec.Mode,
			Pin:  spec.Pin,
		}
	}
	return normalized
}

func normalizeOutputSpec(spec *codeexecutor.OutputSpec) *OutputSpec {
	if spec == nil {
		return nil
	}
	return &OutputSpec{
		Globs:         append([]string(nil), spec.Globs...),
		MaxFiles:      spec.MaxFiles,
		MaxFileBytes:  spec.MaxFileBytes,
		MaxTotalBytes: spec.MaxTotalBytes,
		Save:          spec.Save,
		NameTemplate:  spec.NameTemplate,
		Inline:        spec.Inline,
	}
}

func cloneOutputSpec(spec *OutputSpec) *OutputSpec {
	if spec == nil {
		return nil
	}
	clone := *spec
	clone.Globs = append([]string(nil), spec.Globs...)
	return &clone
}

type codeArguments struct {
	CodeBlocks  json.RawMessage `json:"code_blocks"`
	ExecutionID string          `json:"execution_id,omitempty"`
}

func extractCode(req *tool.PermissionRequest) (Request, bool, error) {
	if req == nil || req.ToolName != "execute_code" {
		return Request{}, false, nil
	}
	var args codeArguments
	if err := decodeStrictToolArguments(
		req.Arguments,
		&args,
		[]string{"code_blocks", "execution_id"},
	); err != nil {
		return Request{}, true, fmt.Errorf("decode execute_code arguments: %w", err)
	}
	blocks, err := decodeCodeBlocks(args.CodeBlocks)
	if err != nil {
		return Request{}, true, err
	}
	return Request{
		ToolName:    req.ToolName,
		ToolCallID:  req.ToolCallID,
		Backend:     BackendCode,
		ExecutionID: args.ExecutionID,
		CodeBlocks:  blocks,
		Metadata:    req.Metadata,
	}, true, nil
}

func decodeCodeBlocks(raw json.RawMessage) ([]CodeBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("decode execute_code arguments: code_blocks is required")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode execute_code code_blocks: %w", err)
	}
	if encoded, ok := value.(string); ok {
		raw = json.RawMessage(encoded)
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode encoded execute_code code_blocks: %w", err)
		}
	}
	switch value.(type) {
	case []any:
		var blocks []CodeBlock
		if err := decodeStrictJSON(raw, &blocks); err != nil {
			return nil, fmt.Errorf("decode execute_code code_blocks: %w", err)
		}
		return blocks, nil
	case map[string]any:
		var block CodeBlock
		if err := decodeStrictJSON(raw, &block); err != nil {
			return nil, fmt.Errorf("decode execute_code code block: %w", err)
		}
		return []CodeBlock{block}, nil
	default:
		return nil, fmt.Errorf("decode execute_code code_blocks: expected array, object, or encoded JSON, got %T", value)
	}
}

type writeStdinArguments struct {
	SessionID     string `json:"session_id,omitempty"`
	SessionIDOld  string `json:"sessionId,omitempty"`
	Chars         string `json:"chars,omitempty"`
	AppendNewline *bool  `json:"append_newline,omitempty"`
	Submit        *bool  `json:"submit,omitempty"`
	YieldTimeMS   *int   `json:"yield-time_ms,omitempty"`
	SkillYieldMS  *int   `json:"yield_ms,omitempty"`
	YieldMSOld    *int   `json:"yieldMs,omitempty"`
	PollLines     int    `json:"poll_lines,omitempty"`
}

func extractHostWriteStdin(req *tool.PermissionRequest) (Request, bool, error) {
	return extractWriteStdin(req, "write_stdin", BackendHost)
}

func extractSkillWriteStdin(req *tool.PermissionRequest) (Request, bool, error) {
	return extractWriteStdin(req, "skill_write_stdin", BackendSkill)
}

func extractWorkspaceWriteStdin(req *tool.PermissionRequest) (Request, bool, error) {
	return extractWriteStdin(req, "workspace_write_stdin", BackendWorkspace)
}

func extractHostKillSession(req *tool.PermissionRequest) (Request, bool, error) {
	return extractSessionControl(req, "kill_session", BackendHost)
}

func extractWorkspaceKillSession(req *tool.PermissionRequest) (Request, bool, error) {
	return extractSessionControl(req, "workspace_kill_session", BackendWorkspace)
}

func extractSkillPollSession(req *tool.PermissionRequest) (Request, bool, error) {
	return extractSessionControl(req, "skill_poll_session", BackendSkill)
}

func extractSkillKillSession(req *tool.PermissionRequest) (Request, bool, error) {
	return extractSessionControl(req, "skill_kill_session", BackendSkill)
}

func extractWriteStdin(
	req *tool.PermissionRequest,
	name string,
	backend Backend,
) (Request, bool, error) {
	if req == nil || req.ToolName != name {
		return Request{}, false, nil
	}
	var args writeStdinArguments
	if err := decodeStrictToolArguments(
		req.Arguments,
		&args,
		writeStdinArgumentFields(name),
	); err != nil {
		return Request{}, true, fmt.Errorf("decode %s arguments: %w", name, err)
	}
	input := args.Chars
	if firstBool(args.AppendNewline, args.Submit) {
		input += "\n"
	}
	return Request{
		ToolName:     req.ToolName,
		ToolCallID:   req.ToolCallID,
		Backend:      backend,
		SessionID:    firstString(args.SessionID, args.SessionIDOld),
		SessionInput: input,
		YieldMS:      firstIntPointer(args.YieldTimeMS, args.SkillYieldMS, args.YieldMSOld),
		PollLines:    args.PollLines,
		Metadata:     req.Metadata,
	}, true, nil
}

func extractSessionControl(
	req *tool.PermissionRequest,
	name string,
	backend Backend,
) (Request, bool, error) {
	if req == nil || req.ToolName != name {
		return Request{}, false, nil
	}
	var args writeStdinArguments
	if err := decodeStrictToolArguments(
		req.Arguments,
		&args,
		sessionControlArgumentFields(name),
	); err != nil {
		return Request{}, true, fmt.Errorf("decode %s arguments: %w", name, err)
	}
	return Request{
		ToolName:   req.ToolName,
		ToolCallID: req.ToolCallID,
		Backend:    backend,
		SessionID:  firstString(args.SessionID, args.SessionIDOld),
		YieldMS: firstIntPointer(
			args.YieldTimeMS,
			args.SkillYieldMS,
			args.YieldMSOld,
		),
		PollLines: args.PollLines,
		Metadata:  req.Metadata,
	}, true, nil
}

func commandArgumentFields(name string) []string {
	if name == "exec_command" {
		return []string{
			"command", "workdir", "env", "yield-time_ms", "yieldMs",
			"background", "timeout_sec", "timeoutSec", "tty", "pty",
		}
	}
	return []string{
		"command", "cwd", "env", "stdin", "yield-time_ms", "yieldMs",
		"background", "timeout", "timeout_sec", "timeoutSec", "tty", "pty",
	}
}

func skillArgumentFields(interactive bool) []string {
	fields := []string{
		"skill", "command", "cwd", "env", "stdin", "editor_text",
		"output_files", "timeout", "save_as_artifacts",
		"omit_inline_content", "artifact_prefix", "inputs", "outputs",
	}
	if interactive {
		fields = append(fields, "tty", "yield_ms", "poll_lines")
	}
	return fields
}

func writeStdinArgumentFields(name string) []string {
	if name == "skill_write_stdin" {
		return []string{
			"session_id", "chars", "submit", "yield_ms", "poll_lines",
		}
	}
	return []string{
		"session_id", "sessionId", "chars", "yield-time_ms", "yieldMs",
		"append_newline", "submit",
	}
}

func sessionControlArgumentFields(name string) []string {
	switch name {
	case "skill_poll_session":
		return []string{"session_id", "yield_ms", "poll_lines"}
	case "skill_kill_session":
		return []string{"session_id"}
	default:
		return []string{"session_id", "sessionId"}
	}
}

func validateSkillNestedArguments(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if inputs, ok := fields["inputs"]; ok && string(inputs) != "null" {
		var items []json.RawMessage
		if err := json.Unmarshal(inputs, &items); err != nil {
			return fmt.Errorf("decode inputs: %w", err)
		}
		for index, item := range items {
			if err := validateJSONObjectFields(
				item, []string{"from", "to", "mode", "pin"},
			); err != nil {
				return fmt.Errorf("decode inputs[%d]: %w", index, err)
			}
		}
	}
	outputs, ok := fields["outputs"]
	if !ok || string(outputs) == "null" {
		return nil
	}
	return validateJSONObjectFields(outputs, []string{
		"globs", "max_files", "max_file_bytes", "max_total_bytes",
		"save", "name_template", "inline",
		"Globs", "MaxFiles", "MaxFileBytes", "MaxTotalBytes",
		"Save", "NameTemplate", "Inline",
	})
}

func decodeStrictToolArguments(
	raw []byte,
	target any,
	allowedFields []string,
) error {
	if err := validateJSONObjectFields(raw, allowedFields); err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func validateJSONObjectFields(raw []byte, allowedFields []string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	allowed := make(map[string]struct{}, len(allowedFields))
	for _, field := range allowedFields {
		allowed[field] = struct{}{}
	}
	unknown := make([]string, 0)
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("json: unknown field %q", unknown[0])
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func firstIntPointer(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			cloned := *value
			return &cloned
		}
	}
	return nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstBool(values ...*bool) bool {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return false
}

func normalizedToolName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
