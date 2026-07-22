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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/workspacefacade"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultWorkspaceTimeout = 5 * time.Minute
	defaultHostTimeout      = 1800 * time.Second
)

// AdaptRequest is the execution request passed to an InputAdapter.
type AdaptRequest struct {
	ToolName  string
	Arguments []byte
	Metadata  tool.ToolMetadata
}

// InputAdapter normalizes one execution tool's JSON arguments for scanning.
type InputAdapter interface {
	Adapt(context.Context, AdaptRequest, Binding) (ScanInput, error)
}

// Binding explicitly associates a visible tool name with its argument schema
// and execution backend.
type Binding struct {
	ToolName string
	Kind     ExecutionKind
	Backend  Backend
	Adapter  InputAdapter
}

// BindWorkspaceExec binds an initial workspaceexec command tool.
func BindWorkspaceExec(toolName string) Binding {
	return Binding{
		ToolName: toolName,
		Kind:     ExecutionKindWorkspaceExec,
		Backend:  BackendWorkspaceExec,
		Adapter:  workspaceExecAdapter{},
	}
}

// BindWorkspaceSession binds a workspaceexec session input/poll tool.
func BindWorkspaceSession(toolName string) Binding {
	return Binding{
		ToolName: toolName,
		Kind:     ExecutionKindWorkspaceSession,
		Backend:  BackendWorkspaceExec,
		Adapter:  workspaceSessionAdapter{},
	}
}

// BindHostExec binds an initial hostexec command tool. baseDir must exactly
// match the value passed to hostexec.WithBaseDir; use "." when that option was
// not set.
func BindHostExec(toolName, baseDir string) Binding {
	return Binding{
		ToolName: toolName,
		Kind:     ExecutionKindHostExec,
		Backend:  BackendHostExec,
		Adapter:  hostExecAdapter{baseDir: resolveHostBaseDir(baseDir)},
	}
}

// BindHostSession binds a hostexec session input/poll tool.
func BindHostSession(toolName string) Binding {
	return Binding{
		ToolName: toolName,
		Kind:     ExecutionKindHostSession,
		Backend:  BackendHostExec,
		Adapter:  hostSessionAdapter{},
	}
}

// BindCodeExec binds a codeexec tool to an explicitly selected backend.
func BindCodeExec(toolName string, backend Backend) Binding {
	return Binding{
		ToolName: toolName,
		Kind:     ExecutionKindCodeExec,
		Backend:  backend,
		Adapter:  codeExecAdapter{},
	}
}

// BindCustom binds a caller-provided adapter for an MCP, Skill, or custom
// execution tool.
func BindCustom(
	toolName string,
	backend Backend,
	adapter InputAdapter,
) Binding {
	return Binding{
		ToolName: toolName,
		Kind:     ExecutionKindCustom,
		Backend:  backend,
		Adapter:  adapter,
	}
}

func validateBindings(bindings []Binding) (map[string]Binding, error) {
	result := make(map[string]Binding, len(bindings))
	for _, binding := range bindings {
		if err := validateBinding(binding); err != nil {
			return nil, err
		}
		if _, ok := result[binding.ToolName]; ok {
			return nil, fmt.Errorf("tool safety: duplicate binding tool name")
		}
		result[binding.ToolName] = binding
	}
	return result, nil
}

func validateBinding(binding Binding) error {
	if binding.ToolName == "" ||
		binding.ToolName != strings.TrimSpace(binding.ToolName) {
		return errors.New("tool safety: binding tool name is required")
	}
	if isNilAdapter(binding.Adapter) {
		return errors.New("tool safety: binding adapter is required")
	}
	switch binding.Kind {
	case ExecutionKindWorkspaceExec, ExecutionKindWorkspaceSession:
		if binding.Backend != BackendWorkspaceExec {
			return errors.New("tool safety: invalid workspace binding backend")
		}
	case ExecutionKindHostExec, ExecutionKindHostSession:
		if binding.Backend != BackendHostExec {
			return errors.New("tool safety: invalid host binding backend")
		}
	case ExecutionKindCodeExec:
		switch binding.Backend {
		case BackendCodeExec, BackendLocal, BackendContainer, BackendRemoteSandbox:
		default:
			return errors.New("tool safety: invalid code binding backend")
		}
	case ExecutionKindCustom:
		switch binding.Backend {
		case BackendCustom:
		default:
			return errors.New("tool safety: invalid custom binding backend")
		}
	default:
		return errors.New("tool safety: unknown binding kind")
	}
	return nil
}

func isNilAdapter(adapter InputAdapter) bool {
	return isNilInterface(adapter)
}

type workspaceExecAdapter struct{}

type workspaceSessionAdapter struct{}

type hostExecAdapter struct {
	baseDir string
}

type hostSessionAdapter struct{}

type codeExecAdapter struct{}

type workspaceExecInput struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Stdin         string            `json:"stdin,omitempty"`
	YieldTimeMS   *int              `json:"yield-time_ms,omitempty"`
	YieldMs       *int              `json:"yieldMs,omitempty"`
	Background    bool              `json:"background,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func (workspaceExecAdapter) Adapt(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
) (ScanInput, error) {
	if err := checkAdaptRequest(ctx, req, binding, ExecutionKindWorkspaceExec); err != nil {
		return ScanInput{}, err
	}
	var input workspaceExecInput
	if err := decodeArguments(req.Arguments, &input); err != nil {
		return ScanInput{}, err
	}
	if strings.TrimSpace(input.Command) == "" {
		return ScanInput{}, errors.New("tool safety: command is required")
	}
	cwd, err := workspacefacade.NormalizeWorkspaceCWD(input.Cwd)
	if err != nil {
		return ScanInput{}, errors.New("tool safety: invalid workspace cwd")
	}
	timeoutSeconds := firstIntValue(input.TimeoutSec, input.TimeoutSecOld)
	if timeoutSeconds <= 0 {
		timeoutSeconds = input.Timeout
	}
	timeout := defaultWorkspaceTimeout
	if timeoutSeconds > 0 {
		timeout = saturatingDuration(timeoutSeconds, time.Second)
	}
	pty := firstBoolValue(input.TTY, input.PTY)
	yieldValue := firstIntPtr(input.YieldTimeMS, input.YieldMs)
	interactive := input.Background || pty ||
		(yieldValue != nil && *yieldValue != 0)
	return ScanInput{
		ToolName:     req.ToolName,
		Kind:         binding.Kind,
		Operation:    OperationExecute,
		Command:      input.Command,
		InitialStdin: input.Stdin,
		WorkingDir:   cwd,
		Env:          cloneStringMap(input.Env),
		Metadata:     req.Metadata,
		Backend:      binding.Backend,
		Timeout:      timeout,
		PTY:          pty,
		Background:   input.Background,
		Interactive:  interactive,
	}, nil
}

type sessionInput struct {
	SessionID     string `json:"session_id,omitempty"`
	SessionIDOld  string `json:"sessionId,omitempty"`
	Chars         string `json:"chars,omitempty"`
	YieldTimeMS   *int   `json:"yield-time_ms,omitempty"`
	YieldMs       *int   `json:"yieldMs,omitempty"`
	AppendNewline *bool  `json:"append_newline,omitempty"`
	Submit        *bool  `json:"submit,omitempty"`
}

func (workspaceSessionAdapter) Adapt(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
) (ScanInput, error) {
	if err := checkAdaptRequest(ctx, req, binding, ExecutionKindWorkspaceSession); err != nil {
		return ScanInput{}, err
	}
	return adaptSession(req, binding)
}

type hostExecInput struct {
	Command       string            `json:"command"`
	Workdir       string            `json:"workdir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	YieldTimeMS   *int              `json:"yield-time_ms,omitempty"`
	YieldMs       *int              `json:"yieldMs,omitempty"`
	Background    bool              `json:"background,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func (adapter hostExecAdapter) Adapt(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
) (ScanInput, error) {
	if err := checkAdaptRequest(ctx, req, binding, ExecutionKindHostExec); err != nil {
		return ScanInput{}, err
	}
	var input hostExecInput
	if err := decodeArguments(req.Arguments, &input); err != nil {
		return ScanInput{}, err
	}
	if strings.TrimSpace(input.Command) == "" {
		return ScanInput{}, errors.New("tool safety: command is required")
	}
	workdir := resolveHostWorkdir(input.Workdir, adapter.baseDir)
	yield := firstIntPtr(input.YieldTimeMS, input.YieldMs)
	timeout := defaultHostTimeout
	if raw := firstIntPtr(input.TimeoutSec, input.TimeoutSecOld); raw != nil && *raw > 0 {
		timeout = saturatingDuration(*raw, time.Second)
	}
	pty := firstBoolValue(input.TTY, input.PTY)
	return ScanInput{
		ToolName:    req.ToolName,
		Kind:        binding.Kind,
		Operation:   OperationExecute,
		Command:     input.Command,
		WorkingDir:  workdir,
		Env:         cloneStringMap(input.Env),
		Metadata:    req.Metadata,
		Backend:     binding.Backend,
		Timeout:     timeout,
		PTY:         pty,
		Background:  input.Background,
		Interactive: input.Background || pty || yield == nil || *yield != 0,
	}, nil
}

func (hostSessionAdapter) Adapt(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
) (ScanInput, error) {
	if err := checkAdaptRequest(ctx, req, binding, ExecutionKindHostSession); err != nil {
		return ScanInput{}, err
	}
	return adaptSession(req, binding)
}

func adaptSession(
	req AdaptRequest,
	binding Binding,
) (ScanInput, error) {
	var input sessionInput
	if err := decodeArguments(req.Arguments, &input); err != nil {
		return ScanInput{}, err
	}
	sessionID := firstNonEmpty(input.SessionID, input.SessionIDOld)
	if sessionID == "" {
		return ScanInput{}, errors.New("tool safety: session id is required")
	}
	submit := firstBoolValue(input.AppendNewline, input.Submit)
	operation := OperationSessionInput
	if input.Chars == "" && !submit {
		operation = OperationSessionPoll
	}
	return ScanInput{
		ToolName:     req.ToolName,
		SessionID:    sessionID,
		Kind:         binding.Kind,
		Operation:    operation,
		SessionInput: input.Chars,
		Submit:       submit,
		Metadata:     req.Metadata,
		Backend:      binding.Backend,
		Interactive:  true,
	}, nil
}

type codeExecEnvelope struct {
	CodeBlocks  json.RawMessage `json:"code_blocks"`
	ExecutionID string          `json:"execution_id,omitempty"`
}

func (codeExecAdapter) Adapt(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
) (ScanInput, error) {
	if err := checkAdaptRequest(ctx, req, binding, ExecutionKindCodeExec); err != nil {
		return ScanInput{}, err
	}
	var envelope codeExecEnvelope
	if err := decodeArguments(req.Arguments, &envelope); err != nil {
		return ScanInput{}, err
	}
	blocks, err := decodeCodeBlocks(envelope.CodeBlocks)
	if err != nil {
		return ScanInput{}, err
	}
	return ScanInput{
		ToolName:   req.ToolName,
		Kind:       binding.Kind,
		Operation:  OperationCodeExecute,
		CodeBlocks: cloneCodeBlocks(blocks),
		Metadata:   req.Metadata,
		Backend:    binding.Backend,
	}, nil
}

func decodeCodeBlocks(raw json.RawMessage) ([]CodeBlockInput, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("tool safety: code blocks are required")
	}
	if trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err != nil {
			return nil, errors.New("tool safety: invalid code blocks")
		}
		trimmed = bytes.TrimSpace([]byte(inner))
		if len(trimmed) == 0 || trimmed[0] == '"' {
			return nil, errors.New("tool safety: invalid code blocks")
		}
	}
	var rawBlocks []json.RawMessage
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &rawBlocks); err != nil {
			return nil, errors.New("tool safety: invalid code blocks")
		}
	case '{':
		rawBlocks = []json.RawMessage{append(json.RawMessage(nil), trimmed...)}
	default:
		return nil, errors.New("tool safety: invalid code blocks")
	}
	if len(rawBlocks) == 0 {
		return nil, errors.New("tool safety: code blocks are required")
	}
	blocks := make([]CodeBlockInput, len(rawBlocks))
	for i, rawBlock := range rawBlocks {
		if err := json.Unmarshal(rawBlock, &blocks[i]); err != nil {
			return nil, errors.New("tool safety: invalid code block")
		}
		if blocks[i].Language == "" {
			return nil, errors.New("tool safety: incomplete code block")
		}
	}
	return blocks, nil
}

func checkAdaptRequest(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
	wantKind ExecutionKind,
) error {
	if ctx == nil {
		return errors.New("tool safety: nil adapter context")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("tool safety: adapter context: %w", err)
	}
	if err := validateBinding(binding); err != nil {
		return err
	}
	if binding.Kind != wantKind {
		return errors.New("tool safety: adapter and binding kind do not match")
	}
	if req.ToolName != binding.ToolName {
		return errors.New("tool safety: request and binding tool name do not match")
	}
	return nil
}

func decodeArguments(arguments []byte, target any) error {
	data := bytes.Clone(arguments)
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("tool safety: tool arguments are required")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("tool safety: invalid tool argument type")
	}
	return nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneCodeBlocks(input []CodeBlockInput) []CodeBlockInput {
	if input == nil {
		return nil
	}
	return append([]CodeBlockInput(nil), input...)
}

func bindTrustedInput(
	input ScanInput,
	req AdaptRequest,
	binding Binding,
) ScanInput {
	input = cloneScanInput(input)
	input.ToolName = req.ToolName
	input.Kind = binding.Kind
	input.Backend = binding.Backend
	input.Metadata = req.Metadata
	return input
}

func validateScanInputShape(input ScanInput) error {
	if strings.TrimSpace(input.ToolName) == "" {
		return errors.New("tool safety: scan input tool name is required")
	}
	if !validKindBackend(input.Kind, input.Backend) {
		return errors.New("tool safety: scan input execution boundary is invalid")
	}
	if !validOperationForKind(input.Kind, input.Operation) {
		return errors.New("tool safety: scan input operation is invalid")
	}
	if !validOperationPayload(input) {
		return errors.New("tool safety: scan input payload is invalid")
	}
	return nil
}

func validKindBackend(kind ExecutionKind, backend Backend) bool {
	switch kind {
	case ExecutionKindWorkspaceExec, ExecutionKindWorkspaceSession:
		return backend == BackendWorkspaceExec
	case ExecutionKindHostExec, ExecutionKindHostSession:
		return backend == BackendHostExec
	case ExecutionKindCodeExec:
		return validCodeBackend(backend)
	case ExecutionKindCustom:
		return backend == BackendCustom
	default:
		return false
	}
}

func validCodeBackend(backend Backend) bool {
	switch backend {
	case BackendCodeExec, BackendLocal, BackendContainer, BackendRemoteSandbox:
		return true
	default:
		return false
	}
}

func validOperationForKind(kind ExecutionKind, operation Operation) bool {
	switch kind {
	case ExecutionKindWorkspaceExec, ExecutionKindHostExec:
		return operation == OperationExecute
	case ExecutionKindWorkspaceSession, ExecutionKindHostSession:
		return operation == OperationSessionInput || operation == OperationSessionPoll
	case ExecutionKindCodeExec:
		return operation == OperationCodeExecute
	case ExecutionKindCustom:
		return knownOperation(operation)
	default:
		return false
	}
}

func knownOperation(operation Operation) bool {
	switch operation {
	case OperationExecute, OperationSessionInput, OperationSessionPoll,
		OperationCodeExecute:
		return true
	default:
		return false
	}
}

func validOperationPayload(input ScanInput) bool {
	switch input.Operation {
	case OperationExecute:
		return validExecutePayload(input) && noSessionOrCodePayload(input)
	case OperationSessionInput:
		return validSessionInputPayload(input)
	case OperationSessionPoll:
		return validSessionPollPayload(input)
	case OperationCodeExecute:
		return validCodePayload(input.CodeBlocks) && noShellOrSessionPayload(input)
	default:
		return false
	}
}

func noSessionOrCodePayload(input ScanInput) bool {
	return strings.TrimSpace(input.SessionID) == "" &&
		strings.TrimSpace(input.SessionInput) == "" && !input.Submit &&
		len(input.CodeBlocks) == 0
}

func validSessionInputPayload(input ScanInput) bool {
	return strings.TrimSpace(input.SessionID) != "" &&
		(strings.TrimSpace(input.SessionInput) != "" || input.Submit) &&
		noExecutablePayload(input)
}

func validSessionPollPayload(input ScanInput) bool {
	return strings.TrimSpace(input.SessionID) != "" &&
		strings.TrimSpace(input.SessionInput) == "" && !input.Submit &&
		noExecutablePayload(input)
}

func noExecutablePayload(input ScanInput) bool {
	return strings.TrimSpace(input.Command) == "" && len(input.Args) == 0 &&
		strings.TrimSpace(input.Script) == "" && strings.TrimSpace(input.Language) == "" &&
		len(input.CodeBlocks) == 0 && strings.TrimSpace(input.InitialStdin) == ""
}

func noShellOrSessionPayload(input ScanInput) bool {
	return strings.TrimSpace(input.SessionID) == "" &&
		strings.TrimSpace(input.Command) == "" && len(input.Args) == 0 &&
		strings.TrimSpace(input.Script) == "" && strings.TrimSpace(input.Language) == "" &&
		strings.TrimSpace(input.InitialStdin) == "" &&
		strings.TrimSpace(input.SessionInput) == "" && !input.Submit
}

func validExecutePayload(input ScanInput) bool {
	forms := 0
	if strings.TrimSpace(input.Command) != "" {
		forms++
	}
	if len(input.Args) > 0 {
		forms++
	}
	if strings.TrimSpace(input.Script) != "" {
		forms++
	}
	if input.Kind != ExecutionKindCustom {
		return forms == 1 && strings.TrimSpace(input.Command) != ""
	}
	return forms == 1
}

func validCodePayload(blocks []CodeBlockInput) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, block := range blocks {
		if strings.TrimSpace(block.Language) == "" ||
			strings.TrimSpace(block.Code) == "" {
			return false
		}
	}
	return true
}

func saturatingDuration(value int, unit time.Duration) time.Duration {
	if value <= 0 {
		return 0
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if int64(value) > int64(maxDuration/unit) {
		return maxDuration
	}
	return time.Duration(value) * unit
}

func firstIntPtr(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstIntValue(values ...*int) int {
	if value := firstIntPtr(values...); value != nil {
		return *value
	}
	return 0
}

func firstBoolValue(values ...*bool) bool {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func resolveHostBaseDir(raw string) string {
	return resolveHostWorkdir(raw, "")
}

func resolveHostWorkdir(raw, baseDir string) string {
	value := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if value == "" {
		return baseDir
	}
	if value == "~" || strings.HasPrefix(value, "~/") || lexicalPathAbsolute(value) {
		return path.Clean(value)
	}
	baseDir = strings.ReplaceAll(strings.TrimSpace(baseDir), "\\", "/")
	if baseDir == "" {
		return path.Clean(value)
	}
	return path.Clean(path.Join(baseDir, value))
}

var (
	_ InputAdapter = workspaceExecAdapter{}
	_ InputAdapter = workspaceSessionAdapter{}
	_ InputAdapter = hostExecAdapter{}
	_ InputAdapter = hostSessionAdapter{}
	_ InputAdapter = codeExecAdapter{}
)
