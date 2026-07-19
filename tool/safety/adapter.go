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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/programsession"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspacefacade"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultWorkspaceTimeout  = 5 * time.Minute
	defaultHostYield         = 10 * time.Second
	defaultHostTimeout       = 1800 * time.Second
	defaultSessionWriteYield = 200 * time.Millisecond
)

// AdaptRequest is the execution request passed to an InputAdapter.
type AdaptRequest struct {
	ToolName   string
	ToolCallID string
	Arguments  []byte
	Metadata   tool.ToolMetadata
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
	Provider Provider
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
	resolved, err := resolveHostBaseDir(baseDir)
	return Binding{
		ToolName: toolName,
		Kind:     ExecutionKindHostExec,
		Backend:  BackendHostExec,
		Adapter: hostExecAdapter{
			baseDir:        resolved,
			baseDirInvalid: err != nil,
		},
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

// BindRemoteCodeExec binds a codeexec tool to a remote sandbox provider.
func BindRemoteCodeExec(toolName string, provider Provider) Binding {
	return Binding{
		ToolName: toolName,
		Kind:     ExecutionKindCodeExec,
		Backend:  BackendRemoteSandbox,
		Provider: provider,
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
	if validator, ok := binding.Adapter.(bindingAdapterValidator); ok {
		if err := validator.validateBinding(); err != nil {
			return err
		}
	}
	if !validBackendProvider(binding.Backend, binding.Provider) {
		return errors.New("tool safety: binding provider is invalid")
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
		case BackendCodeExec, BackendLocal, BackendContainer:
		case BackendRemoteSandbox:
		default:
			return errors.New("tool safety: invalid code binding backend")
		}
	case ExecutionKindCustom:
		switch binding.Backend {
		case BackendMCP, BackendSkill, BackendCustom:
		default:
			return errors.New("tool safety: invalid custom binding backend")
		}
	default:
		return errors.New("tool safety: unknown binding kind")
	}
	return nil
}

func validBackendProvider(backend Backend, provider Provider) bool {
	if backend == BackendRemoteSandbox {
		return validProvider(provider)
	}
	return provider == ""
}

func validProvider(provider Provider) bool {
	value := string(provider)
	if value == "" || len(value) > 64 {
		return false
	}
	separator := false
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			separator = false
			continue
		}
		if separator || index == 0 || index == len(value)-1 ||
			(char != '.' && char != '_' && char != '-') {
			return false
		}
		separator = true
	}
	return true
}

func isNilAdapter(adapter InputAdapter) bool {
	if adapter == nil {
		return true
	}
	value := reflect.ValueOf(adapter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

type workspaceExecAdapter struct{}

type workspaceSessionAdapter struct{}

type hostExecAdapter struct {
	baseDir        string
	baseDirInvalid bool
}

type hostSessionAdapter struct{}

type codeExecAdapter struct{}

type bindingAdapterValidator interface {
	validateBinding() error
}

func (adapter hostExecAdapter) validateBinding() error {
	if adapter.baseDirInvalid {
		return errors.New("tool safety: invalid host base directory")
	}
	return nil
}

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
	yield := workspaceExecYield(input.Background, interactive, yieldValue)
	return ScanInput{
		ToolName:     req.ToolName,
		ToolCallID:   req.ToolCallID,
		Kind:         binding.Kind,
		Operation:    OperationExecute,
		Command:      input.Command,
		InitialStdin: input.Stdin,
		WorkingDir:   cwd,
		Env:          cloneStringMap(input.Env),
		Metadata:     req.Metadata,
		Backend:      binding.Backend,
		Provider:     binding.Provider,
		Timeout:      timeout,
		Yield:        yield,
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
	return adaptSession(req, binding, defaultSessionWriteYield)
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
	workdir, err := resolveHostWorkdir(input.Workdir, adapter.baseDir)
	if err != nil {
		return ScanInput{}, errors.New("tool safety: invalid host workdir")
	}
	yield := defaultHostYield
	if raw := firstIntPtr(input.YieldTimeMS, input.YieldMs); raw != nil && *raw >= 0 {
		yield = saturatingDuration(*raw, time.Millisecond)
	}
	timeout := defaultHostTimeout
	if raw := firstIntPtr(input.TimeoutSec, input.TimeoutSecOld); raw != nil && *raw > 0 {
		timeout = saturatingDuration(*raw, time.Second)
	}
	pty := firstBoolValue(input.TTY, input.PTY)
	return ScanInput{
		ToolName:    req.ToolName,
		ToolCallID:  req.ToolCallID,
		Kind:        binding.Kind,
		Operation:   OperationExecute,
		Command:     input.Command,
		WorkingDir:  workdir,
		Env:         cloneStringMap(input.Env),
		Metadata:    req.Metadata,
		Backend:     binding.Backend,
		Provider:    binding.Provider,
		Timeout:     timeout,
		Yield:       yield,
		PTY:         pty,
		Background:  input.Background,
		Interactive: input.Background || pty || yield != 0,
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
	return adaptSession(req, binding, defaultSessionWriteYield)
}

func adaptSession(
	req AdaptRequest,
	binding Binding,
	defaultYield time.Duration,
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
	yield := defaultYield
	if raw := firstIntPtr(input.YieldTimeMS, input.YieldMs); raw != nil && *raw >= 0 {
		yield = saturatingDuration(*raw, time.Millisecond)
	}
	return ScanInput{
		ToolName:     req.ToolName,
		ToolCallID:   req.ToolCallID,
		SessionID:    sessionID,
		Kind:         binding.Kind,
		Operation:    operation,
		SessionInput: input.Chars,
		Submit:       submit,
		Metadata:     req.Metadata,
		Backend:      binding.Backend,
		Provider:     binding.Provider,
		Yield:        yield,
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
		ToolName:    req.ToolName,
		ToolCallID:  req.ToolCallID,
		ExecutionID: envelope.ExecutionID,
		Kind:        binding.Kind,
		Operation:   OperationCodeExecute,
		CodeBlocks:  cloneCodeBlocks(blocks),
		Metadata:    req.Metadata,
		Backend:     binding.Backend,
		Provider:    binding.Provider,
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

func workspaceExecYield(
	background bool,
	interactive bool,
	raw *int,
) time.Duration {
	if !interactive {
		return 0
	}
	if background {
		if raw != nil && *raw > 0 {
			return saturatingDuration(*raw, time.Millisecond)
		}
		return 0
	}
	value := 0
	if raw != nil {
		value = *raw
	}
	if value <= 0 {
		value = programsession.DefaultExecYieldMS
	}
	return saturatingDuration(value, time.Millisecond)
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

func resolveHostBaseDir(raw string) (string, error) {
	baseDir, err := resolveHostWorkdir(raw, "")
	if err != nil {
		return "", err
	}
	if baseDir == "" {
		return "", nil
	}
	return filepath.Abs(baseDir)
}

func resolveHostWorkdir(raw, baseDir string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return baseDir, nil
	}
	if value == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	if baseDir != "" && !filepath.IsAbs(value) {
		return filepath.Join(baseDir, value), nil
	}
	if filepath.IsAbs(value) {
		return value, nil
	}
	return filepath.Abs(value)
}

var (
	_ InputAdapter = workspaceExecAdapter{}
	_ InputAdapter = workspaceSessionAdapter{}
	_ InputAdapter = hostExecAdapter{}
	_ InputAdapter = hostSessionAdapter{}
	_ InputAdapter = codeExecAdapter{}
)
