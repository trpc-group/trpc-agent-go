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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ tool.PermissionPolicy = (*Scanner)(nil)

type inputFields struct {
	backend      Backend
	command      []string
	arguments    []string
	workingDir   []string
	environment  []string
	timeout      []string
	outputBytes  []string
	background   []string
	pty          []string
	codeBlocks   []string
	expectsInput bool
	sessionWrite bool
}

// CheckToolPermission implements tool.PermissionPolicy by scanning the
// finalized JSON arguments immediately before tool execution. It rejects a nil
// context and returns audit write failures so the framework skips execution.
func (s *Scanner) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if s == nil {
		return tool.PermissionDecision{}, fmt.Errorf("nil safety scanner")
	}
	if ctx == nil {
		return tool.PermissionDecision{}, fmt.Errorf("nil context")
	}
	if err := ctx.Err(); err != nil {
		return tool.PermissionDecision{}, err
	}
	input := s.scanInputFromPermissionRequest(req)
	report, err := s.Scan(ctx, input)
	if err != nil {
		return tool.PermissionDecision{}, err
	}
	reason := fmt.Sprintf("%s: %s", report.RuleID, report.Evidence)
	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission(), nil
	case DecisionAsk:
		return tool.AskPermission(reason), nil
	case DecisionDeny:
		return tool.DenyPermission(reason), nil
	default:
		return tool.PermissionDecision{}, fmt.Errorf(
			"unknown safety decision %q",
			report.Decision,
		)
	}
}

func (s *Scanner) scanInputFromPermissionRequest(req *tool.PermissionRequest) ScanInput {
	if req == nil {
		return ScanInput{
			Backend: BackendUnknown,
			initialFindings: []Finding{finding(
				DecisionDeny,
				RiskLevelHigh,
				RuleInvalidInput,
				"permission request is nil",
				"provide a complete tool permission request",
			)},
		}
	}
	input := ScanInput{
		ToolName: req.ToolName,
		Metadata: req.Metadata,
	}
	if len(req.Arguments) > maxScanInputBytes {
		input.initialFindings = append(input.initialFindings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleResourceAbuse,
			fmt.Sprintf("tool arguments exceed %d bytes", maxScanInputBytes),
			"reduce the JSON argument payload before requesting execution",
		))
		return input
	}
	root, err := decodeArguments(req.Arguments)
	if err != nil {
		input.initialFindings = append(input.initialFindings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			fmt.Sprintf("tool arguments are not valid JSON: %v", err),
			"provide one JSON object matching the tool declaration",
		))
		return input
	}
	input.extraValues = collectStrings(root)
	fields := s.fieldsForRequest(req, root)
	input.Backend = fields.backend
	s.extractPermissionFields(&input, root, fields)
	return input
}

func decodeArguments(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("arguments must contain one JSON object")
		}
		return nil, err
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

func (s *Scanner) fieldsForRequest(
	req *tool.PermissionRequest,
	root map[string]any,
) inputFields {
	if profile, ok := s.policy.ToolProfiles[req.ToolName]; ok {
		return fieldsFromProfile(profile)
	}
	props := map[string]struct{}{}
	if req.Declaration != nil && req.Declaration.InputSchema != nil {
		for name := range req.Declaration.InputSchema.Properties {
			props[name] = struct{}{}
		}
	}
	for name := range root {
		props[name] = struct{}{}
	}
	_, hasCode := props["code_blocks"]
	_, hasCommand := props["command"]
	_, hasCWD := props["cwd"]
	_, hasWorkdir := props["workdir"]
	_, hasChars := props["chars"]
	switch {
	case hasCode:
		return conventionalFields(BackendCodeExecutor, true)
	case hasCommand && hasCWD:
		return conventionalFields(BackendWorkspace, true)
	case hasCommand && hasWorkdir:
		return conventionalFields(BackendHost, true)
	case hasCommand:
		return conventionalFields(BackendGeneric, true)
	case hasChars && strings.Contains(strings.ToLower(req.ToolName), "write_stdin"):
		fields := conventionalFields(sessionBackend(req.ToolName), false)
		fields.command = []string{"chars"}
		fields.sessionWrite = true
		return fields
	default:
		return inputFields{backend: BackendUnknown}
	}
}

func fieldsFromProfile(profile ToolProfile) inputFields {
	fields := conventionalFields(profile.Backend, true)
	fields.command = overrideField(fields.command, profile.CommandField)
	fields.arguments = overrideField(fields.arguments, profile.ArgumentsField)
	fields.workingDir = overrideField(fields.workingDir, profile.WorkingDirectoryField)
	fields.environment = overrideField(fields.environment, profile.EnvironmentField)
	fields.timeout = overrideField(fields.timeout, profile.TimeoutSecondsField)
	fields.outputBytes = overrideField(fields.outputBytes, profile.OutputBytesField)
	fields.background = overrideField(fields.background, profile.BackgroundField)
	fields.pty = overrideField(fields.pty, profile.PTYField)
	fields.codeBlocks = overrideField(fields.codeBlocks, profile.CodeBlocksField)
	return fields
}

func conventionalFields(backend Backend, expectsInput bool) inputFields {
	fields := inputFields{
		backend:      backend,
		command:      []string{"command"},
		arguments:    []string{"arguments", "args"},
		workingDir:   []string{"cwd", "workdir"},
		environment:  []string{"env"},
		timeout:      []string{"timeout_sec", "timeoutSec", "timeout"},
		outputBytes:  []string{"max_output_bytes", "output_max_bytes"},
		background:   []string{"background"},
		pty:          []string{"tty", "pty"},
		codeBlocks:   []string{"code_blocks"},
		expectsInput: expectsInput,
	}
	if backend == BackendCodeExecutor {
		fields.command = nil
	} else {
		fields.codeBlocks = nil
	}
	return fields
}

func overrideField(current []string, configured string) []string {
	if configured == "" {
		return current
	}
	return []string{configured}
}

func sessionBackend(toolName string) Backend {
	toolName = strings.ToLower(toolName)
	if strings.Contains(toolName, "workspace") {
		return BackendWorkspace
	}
	if strings.Contains(toolName, "skill") {
		return BackendCodeExecutor
	}
	return BackendHost
}

func (s *Scanner) extractPermissionFields(
	input *ScanInput,
	root map[string]any,
	fields inputFields,
) {
	var errs []error
	input.Command, errs = stringField(root, fields.command, errs)
	input.Arguments, errs = stringSliceField(root, fields.arguments, errs)
	input.WorkingDirectory, errs = stringField(root, fields.workingDir, errs)
	input.Environment, errs = stringMapField(root, fields.environment, errs)
	input.TimeoutSeconds, errs = intField(root, fields.timeout, errs)
	input.RequestedOutputBytes, errs = intField(root, fields.outputBytes, errs)
	input.Background, errs = boolField(root, fields.background, errs)
	input.PTY, errs = boolField(root, fields.pty, errs)
	input.CodeBlocks, errs = codeBlocksField(root, fields.codeBlocks, errs)
	if fields.sessionWrite {
		input.sessionWrite = true
		var submit, appendNewline bool
		submit, errs = boolField(root, []string{"submit"}, errs)
		appendNewline, errs = boolField(root, []string{"append_newline"}, errs)
		if input.Command != "" || submit || appendNewline {
			input.initialFindings = append(input.initialFindings, finding(
				DecisionAsk,
				RiskLevelHigh,
				RuleInteractiveInput,
				"interactive stdin can complete command text retained by an existing session",
				"review the complete session transcript or start a fixed non-interactive command",
			))
		}
	}
	for _, err := range errs {
		input.initialFindings = append(input.initialFindings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			err.Error(),
			"provide arguments matching the configured tool profile",
		))
	}
	if fields.expectsInput && strings.TrimSpace(input.Command) == "" &&
		len(input.CodeBlocks) == 0 {
		input.initialFindings = append(input.initialFindings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			"tool execution payload is missing",
			"provide the required command or code_blocks field",
		))
	}
}

func firstValue(root map[string]any, paths []string) (any, string, bool) {
	for _, fieldPath := range paths {
		value, ok := valueAtPath(root, fieldPath)
		if ok {
			return value, fieldPath, true
		}
	}
	return nil, "", false
}

func valueAtPath(root map[string]any, fieldPath string) (any, bool) {
	var current any = root
	for _, segment := range strings.Split(fieldPath, ".") {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = mapping[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func stringField(
	root map[string]any,
	paths []string,
	errs []error,
) (string, []error) {
	value, fieldPath, ok := firstValue(root, paths)
	if !ok || value == nil {
		return "", errs
	}
	text, ok := value.(string)
	if !ok {
		return "", append(errs, fmt.Errorf("field %q must be a string", fieldPath))
	}
	return text, errs
}

func stringSliceField(
	root map[string]any,
	paths []string,
	errs []error,
) ([]string, []error) {
	value, fieldPath, ok := firstValue(root, paths)
	if !ok || value == nil {
		return nil, errs
	}
	items, ok := value.([]any)
	if !ok {
		return nil, append(errs, fmt.Errorf("field %q must be an array of strings", fieldPath))
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, append(errs, fmt.Errorf("field %q must contain only strings", fieldPath))
		}
		out = append(out, text)
	}
	return out, errs
}

func stringMapField(
	root map[string]any,
	paths []string,
	errs []error,
) (map[string]string, []error) {
	value, fieldPath, ok := firstValue(root, paths)
	if !ok || value == nil {
		return nil, errs
	}
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil, append(errs, fmt.Errorf("field %q must be an object", fieldPath))
	}
	out := make(map[string]string, len(mapping))
	for key, value := range mapping {
		text, ok := value.(string)
		if !ok {
			return nil, append(errs, fmt.Errorf("field %q value %q must be a string", fieldPath, key))
		}
		out[key] = text
	}
	return out, errs
}

func intField(
	root map[string]any,
	paths []string,
	errs []error,
) (int, []error) {
	value, fieldPath, ok := firstValue(root, paths)
	if !ok || value == nil {
		return 0, errs
	}
	parsed, err := integerValue(value)
	if err != nil {
		return 0, append(errs, fmt.Errorf("field %q must be an integer", fieldPath))
	}
	return parsed, errs
}

func integerValue(value any) (int, error) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return int(parsed), err
	case int:
		return typed, nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("not an integer")
		}
		return int(typed), nil
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func boolField(
	root map[string]any,
	paths []string,
	errs []error,
) (bool, []error) {
	value, fieldPath, ok := firstValue(root, paths)
	if !ok || value == nil {
		return false, errs
	}
	parsed, ok := value.(bool)
	if !ok {
		return false, append(errs, fmt.Errorf("field %q must be a boolean", fieldPath))
	}
	return parsed, errs
}

func codeBlocksField(
	root map[string]any,
	paths []string,
	errs []error,
) ([]CodeBlock, []error) {
	value, fieldPath, ok := firstValue(root, paths)
	if !ok || value == nil {
		return nil, errs
	}
	if encoded, ok := value.(string); ok {
		dec := json.NewDecoder(strings.NewReader(encoded))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return nil, append(errs, fmt.Errorf("field %q contains invalid encoded JSON", fieldPath))
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, append(errs, fmt.Errorf("field %q contains trailing encoded JSON", fieldPath))
		}
	}
	if single, ok := value.(map[string]any); ok {
		value = []any{single}
	}
	items, ok := value.([]any)
	if !ok {
		return nil, append(errs, fmt.Errorf("field %q must be an array", fieldPath))
	}
	out := make([]CodeBlock, 0, len(items))
	for _, item := range items {
		mapping, ok := item.(map[string]any)
		if !ok {
			return nil, append(errs, fmt.Errorf("field %q contains a non-object code block", fieldPath))
		}
		language, languageOK := mapping["language"].(string)
		code, codeOK := mapping["code"].(string)
		if !languageOK || !codeOK {
			return nil, append(errs, fmt.Errorf("field %q code blocks require string language and code", fieldPath))
		}
		out = append(out, CodeBlock{Language: language, Code: code})
	}
	return out, errs
}

func collectStrings(value any) []string {
	var out []string
	collectStringsInto(value, &out)
	return out
}

func collectStringsInto(value any, out *[]string) {
	switch typed := value.(type) {
	case string:
		*out = append(*out, typed)
	case []any:
		for _, item := range typed {
			collectStringsInto(item, out)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectStringsInto(typed[key], out)
		}
	}
}

func (s *Scanner) scanOpenWorldValues(
	metadata tool.ToolMetadata,
	values []string,
) []Finding {
	if !metadata.OpenWorld {
		return nil
	}
	for _, value := range values {
		for _, rawURL := range sourceURLPattern.FindAllString(value, -1) {
			parsed, err := url.Parse(strings.TrimRight(rawURL, ".,;:)\"'"))
			if err != nil || parsed.Hostname() == "" || !s.domainAllowed(parsed.Hostname()) {
				host := rawURL
				if err == nil && parsed.Hostname() != "" {
					host = parsed.Hostname()
				}
				return []Finding{finding(
					DecisionDeny,
					RiskLevelCritical,
					RuleNetworkEgress,
					fmt.Sprintf("open-world tool target %q is not allowlisted", host),
					"use an explicitly allowlisted network destination",
				)}
			}
		}
	}
	return nil
}
