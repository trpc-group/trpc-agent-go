//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// WrapTool wraps a callable tool with safety preflight and completion
// handling. The wrapper owns the complete call lifecycle: it scans before
// execution, returns structured deny/ask results without calling the
// underlying tool, and redacts, limits, audits, and releases resources
// after execution.
//
// The returned tool also implements tool.PermissionChecker. A successful
// framework precheck is reused by Call only when the tool call id and
// arguments are unchanged; the safety scan itself is repeated immediately
// before execution.
//
// WrapTool intentionally accepts tool.CallableTool. Streamable-only tools
// require a stream-aware wrapper so partial chunks can be redacted before
// they are observed.
func WrapTool(
	t tool.CallableTool,
	guard *Guard,
) (tool.CallableTool, error) {
	if t == nil {
		return nil, errors.New("tool is nil")
	}
	if guard == nil {
		return nil, errors.New("safety guard is nil")
	}
	decl := t.Declaration()
	if decl == nil || decl.Name == "" {
		return nil, errors.New("tool declaration name is empty")
	}
	metadata := tool.MetadataOf(t)
	return &wrappedCallableTool{
		wrappedToolBase: wrappedToolBase{
			tool:        t,
			guard:       guard,
			declaration: decl,
			metadata:    metadata,
		},
		callable: t,
	}, nil
}

type wrappedToolBase struct {
	tool        tool.Tool
	guard       *Guard
	declaration *tool.Declaration
	metadata    tool.ToolMetadata
}

func (w *wrappedToolBase) Declaration() *tool.Declaration {
	return w.declaration
}

// Original returns the wrapped tool for framework helpers that preserve
// optional tool capabilities through wrappers.
func (w *wrappedToolBase) Original() tool.Tool {
	return w.tool
}

// ToolMetadata forwards metadata from the wrapped tool.
func (w *wrappedToolBase) ToolMetadata() tool.ToolMetadata {
	return w.metadata
}

func (w *wrappedToolBase) LongRunning() bool {
	if value, ok := w.tool.(interface{ LongRunning() bool }); ok {
		return value.LongRunning()
	}
	return false
}

func (w *wrappedToolBase) SkipSummarization() bool {
	if value, ok := w.tool.(interface{ SkipSummarization() bool }); ok {
		return value.SkipSummarization()
	}
	return false
}

func (w *wrappedToolBase) StreamInner() bool {
	if value, ok := w.tool.(interface{ StreamInner() bool }); ok {
		return value.StreamInner()
	}
	return true
}

func (w *wrappedToolBase) InnerTextMode() tool.InnerTextMode {
	if value, ok := w.tool.(interface {
		InnerTextMode() tool.InnerTextMode
	}); ok {
		return value.InnerTextMode()
	}
	return tool.InnerTextModeInclude
}

func (w *wrappedToolBase) PollutesAutoMemory() bool {
	if value, ok := w.tool.(interface{ PollutesAutoMemory() bool }); ok {
		return value.PollutesAutoMemory()
	}
	return false
}

func (w *wrappedToolBase) TRPCAgentGoStructuredStreamErrorsOptIn() bool {
	if value, ok := w.tool.(interface {
		TRPCAgentGoStructuredStreamErrorsOptIn() bool
	}); ok {
		return value.TRPCAgentGoStructuredStreamErrorsOptIn()
	}
	return false
}

func (w *wrappedToolBase) ShouldDefer(ctx context.Context) bool {
	return tool.ShouldDefer(ctx, w.tool)
}

func (w *wrappedToolBase) StateDelta(
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	if provider, ok := w.tool.(interface {
		StateDelta(string, []byte, []byte) map[string][]byte
	}); ok {
		return provider.StateDelta(toolCallID, arguments, result)
	}
	return nil
}

func (w *wrappedToolBase) StateDeltaForInvocation(
	invocation *agent.Invocation,
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	if provider, ok := w.tool.(interface {
		StateDeltaForInvocation(
			*agent.Invocation,
			string,
			[]byte,
			[]byte,
		) map[string][]byte
	}); ok {
		return provider.StateDeltaForInvocation(
			invocation, toolCallID, arguments, result,
		)
	}
	return w.StateDelta(toolCallID, arguments, result)
}

type wrappedCallableTool struct {
	wrappedToolBase
	callable tool.CallableTool

	precheckMu    sync.Mutex
	prechecks     map[string][sha256.Size]byte
	precheckOrder []string
}

const maxWrapperPrechecks = 1024

// CheckPermission implements tool.PermissionChecker so framework-managed
// calls surface static safety denials before execution callbacks and state
// delta handling. Call performs the owning lifecycle check immediately before
// execution, where concurrency, audit, and session reservations are acquired.
func (w *wrappedCallableTool) CheckPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (
	decision tool.PermissionDecision,
	err error,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			decision = tool.PermissionDecision{}
			err = fmt.Errorf(
				"wrapped tool permission check panicked (type %T)",
				recovered,
			)
		}
	}()
	if req == nil {
		return tool.DenyPermission("permission request is nil"), nil
	}
	innerReq := *req
	innerReq.Tool = w.tool
	innerReq.Declaration = w.Declaration()
	innerReq.Metadata = w.metadata
	innerChecked := false
	if checker, ok := w.tool.(tool.PermissionChecker); ok {
		decision, err = checker.CheckPermission(ctx, &innerReq)
		if err != nil {
			return tool.PermissionDecision{}, w.sanitizeError(
				"wrapped tool permission check failed", err,
			)
		}
		decision, err = tool.NormalizePermissionDecision(decision)
		if err != nil {
			return tool.PermissionDecision{}, w.sanitizeError(
				"invalid permission decision", err,
			)
		}
		if decision.Action != tool.PermissionActionAllow {
			decision.Reason = redactedSnippet(
				decision.Reason,
				permissionReasonMaxLen,
			)
			return decision, nil
		}
		innerChecked = true
	}
	decision, err = w.guard.previewToolCall(ctx, req)
	if err == nil && decision.Action == tool.PermissionActionAllow &&
		innerChecked {
		w.rememberPrecheck(req.ToolCallID, req.Arguments)
	}
	return decision, err
}

func (w *wrappedCallableTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (result any, err error) {
	var toolCallID string
	lifecycleOwned := false
	wrappedCallStarted := false
	finishing := false
	defer func() {
		if wrappedCallStarted {
			w.guard.endWrappedCall()
		}
	}()
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		panicErr := fmt.Errorf(
			"wrapped tool panicked (type %T)", recovered,
		)
		if !lifecycleOwned {
			result = nil
			err = panicErr
			return
		}
		if finishing {
			w.guard.finishCall(toolCallID)
			result = nil
			err = panicErr
			return
		}
		finishing = true
		result, err = w.completeCallSafely(
			ctx, toolCallID, jsonArgs, result, panicErr,
		)
	}()

	var ok bool
	toolCallID, ok = tool.ToolCallIDFromContext(ctx)
	if !ok || toolCallID == "" {
		toolCallID = "tool-safety-" + newScanID()
	}
	req := &tool.PermissionRequest{
		Tool:        w.tool,
		ToolName:    w.Declaration().Name,
		ToolCallID:  toolCallID,
		Declaration: w.Declaration(),
		Arguments:   jsonArgs,
		Metadata:    w.metadata,
	}
	if checker, ok := w.tool.(tool.PermissionChecker); ok &&
		!w.consumePrecheck(toolCallID, jsonArgs) {
		decision, err := checker.CheckPermission(ctx, req)
		if err != nil {
			return nil, w.sanitizeError(
				"wrapped tool permission check failed", err,
			)
		}

		decision, err = tool.NormalizePermissionDecision(decision)
		if err != nil {
			return nil, w.sanitizeError(
				"invalid permission decision", err,
			)
		}
		if decision.Action != tool.PermissionActionAllow {
			return w.permissionResult(decision), nil
		}
	}
	if err := w.guard.beginWrappedCall(); err != nil {
		return nil, err
	}
	wrappedCallStarted = true
	decision, err := w.guard.checkToolCall(ctx, req)
	if err != nil {
		return nil, w.sanitizeError(
			"safety preflight failed", err,
		)
	}
	if decision.Action != tool.PermissionActionAllow {
		return w.permissionResult(decision), nil
	}
	lifecycleOwned = true

	originalResult, callErr := w.callable.Call(ctx, jsonArgs)
	result = originalResult
	finishing = true
	return w.completeCallSafely(
		ctx, toolCallID, jsonArgs, originalResult, callErr,
	)
}

func (w *wrappedCallableTool) rememberPrecheck(
	toolCallID string,
	arguments []byte,
) {
	if toolCallID == "" {
		return
	}
	w.precheckMu.Lock()
	defer w.precheckMu.Unlock()
	if w.prechecks == nil {
		w.prechecks = make(map[string][sha256.Size]byte)
	}
	if _, exists := w.prechecks[toolCallID]; !exists {
		w.precheckOrder = append(w.precheckOrder, toolCallID)
	}
	w.prechecks[toolCallID] = sha256.Sum256(arguments)
	for len(w.precheckOrder) > maxWrapperPrechecks {
		oldest := w.precheckOrder[0]
		w.precheckOrder = w.precheckOrder[1:]
		delete(w.prechecks, oldest)
	}
}

func (w *wrappedCallableTool) consumePrecheck(
	toolCallID string,
	arguments []byte,
) bool {
	if toolCallID == "" {
		return false
	}
	w.precheckMu.Lock()
	defer w.precheckMu.Unlock()
	expected, ok := w.prechecks[toolCallID]
	if !ok {
		return false
	}
	delete(w.prechecks, toolCallID)
	for i, id := range w.precheckOrder {
		if id == toolCallID {
			w.precheckOrder = append(
				w.precheckOrder[:i],
				w.precheckOrder[i+1:]...,
			)
			break
		}
	}
	return expected == sha256.Sum256(arguments)
}

func (w *wrappedCallableTool) completeCallSafely(
	ctx context.Context,
	toolCallID string,
	jsonArgs []byte,
	result any,
	callErr error,
) (safeResult any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			w.guard.finishCall(toolCallID)
			safeResult = nil
			err = fmt.Errorf(
				"safety completion panicked (type %T)",
				recovered,
			)
		}
	}()
	return w.completeCall(
		ctx, toolCallID, jsonArgs, result, callErr,
	)
}

func (w *wrappedCallableTool) completeCall(
	ctx context.Context,
	toolCallID string,
	jsonArgs []byte,
	result any,
	callErr error,
) (any, error) {
	originalResult := result
	meta := resultMetadata(result)
	finalResult, finalErr := w.guard.finalizeCall(
		ctx,
		&tool.AfterToolArgs{
			ToolCallID:  toolCallID,
			ToolName:    w.Declaration().Name,
			Declaration: w.Declaration(),
			Arguments:   jsonArgs,
			Result:      result,
			Error:       callErr,
			Meta:        meta,
		},
	)
	resultChanged := false
	if finalResult != nil && finalResult.CustomResult != nil {
		result = finalResult.CustomResult
		resultChanged = true
	}
	result = preserveResultCapabilities(
		w.guard, originalResult, result, resultChanged, meta,
	)
	if finalErr != nil {
		return result, errors.Join(
			w.sanitizeError("wrapped tool call failed", callErr),
			w.sanitizeError("safety completion failed", finalErr),
		)
	}
	if callErr != nil && finalResult != nil &&
		finalResult.CustomResult != nil &&
		hasSecret(callErr.Error()) {
		return result, w.sanitizeError(
			"wrapped tool call failed", callErr,
		)
	}
	if callErr != nil {
		return result, w.sanitizeError(
			"wrapped tool call failed", callErr,
		)
	}
	return result, nil
}

func (w *wrappedCallableTool) permissionResult(
	decision tool.PermissionDecision,
) tool.PermissionResult {
	decision.Reason = redactedSnippet(
		decision.Reason, permissionReasonMaxLen,
	)
	return tool.PermissionResultFor(
		w.Declaration().Name, decision,
	)
}

func (w *wrappedCallableTool) sanitizeError(
	prefix string,
	err error,
) error {
	if err == nil {
		return nil
	}
	message := redactedSnippet(
		err.Error(), permissionReasonMaxLen,
	)
	cause := err
	if hasSecret(err.Error()) {
		cause = nil
	}
	return &sanitizedError{
		message: prefix + ": " + message,
		cause:   cause,
	}
}

const permissionReasonMaxLen = 1024

type sanitizedError struct {
	message string
	cause   error
}

type safeResult struct {
	value      any
	callback   any
	retryError bool
	meta       map[string]any
}

func preserveResultCapabilities(
	guard *Guard,
	original any,
	safe any,
	resultChanged bool,
	safeMeta map[string]any,
) any {
	retry, hasRetry := original.(interface {
		RetryResultError() bool
	})
	_, hasMeta := original.(interface {
		GetMeta() map[string]any
	})
	callback, hasCallback := original.(interface {
		GetCallbackResult() any
	})
	var safeCallback any
	callbackChanged := false
	if hasCallback {
		safeCallback, callbackChanged = safeCallbackProjection(
			guard, callback.GetCallbackResult(),
		)
	}
	if !resultChanged && !callbackChanged {
		return original
	}
	if !hasRetry && !hasMeta && !hasCallback {
		return safe
	}
	result := &safeResult{value: safe}
	if hasRetry {
		result.retryError = retry.RetryResultError()
	}
	if hasMeta {
		result.meta = safeMeta
	}
	if hasCallback {
		result.callback = safeCallback
	} else {
		result.callback = safe
	}
	return result
}

func safeCallbackProjection(
	guard *Guard,
	original any,
) (any, bool) {
	if original == nil {
		return nil, false
	}
	safe := original
	changed := false
	var err error
	if guard == nil || guard.redaction {
		safe, changed, err = redactValue(original)
		if err != nil {
			return zeroValueOf(original), true
		}
	}
	preserved := original
	if changed {
		preserved, err = restoreJSONType(original, safe)
		if err != nil {
			return zeroValueOf(original), true
		}
	}
	if guard == nil || guard.scanner == nil {
		return preserved, changed
	}
	_, truncated, _ := limitResultBytes(
		preserved, guard.scanner.policy.MaxOutputSize,
	)
	if truncated {
		return zeroValueOf(original), true
	}
	return preserved, changed
}

func restoreJSONType(original any, safe any) (any, error) {
	typ := reflect.TypeOf(original)
	if typ == nil {
		return nil, nil
	}
	raw, err := json.Marshal(safe)
	if err != nil {
		return nil, err
	}
	if typ.Kind() == reflect.Pointer {
		value := reflect.New(typ.Elem())
		if err := json.Unmarshal(raw, value.Interface()); err != nil {
			return nil, err
		}
		return value.Interface(), nil
	}
	value := reflect.New(typ)
	if err := json.Unmarshal(raw, value.Interface()); err != nil {
		return nil, err
	}
	return value.Elem().Interface(), nil
}

func zeroValueOf(value any) any {
	typ := reflect.TypeOf(value)
	if typ == nil {
		return nil
	}
	return reflect.Zero(typ).Interface()
}

func (r *safeResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.value)
}

func (r *safeResult) RetryResultError() bool {
	return r.retryError
}

func (r *safeResult) GetCallbackResult() any {
	return r.callback
}

func (r *safeResult) GetMeta() map[string]any {
	return r.meta
}

func (e *sanitizedError) Error() string {
	return e.message
}

func (e *sanitizedError) Unwrap() error {
	return e.cause
}

func resultMetadata(result any) map[string]any {
	if result == nil {
		return nil
	}
	if value, ok := result.(interface {
		GetMeta() map[string]any
	}); ok {
		return value.GetMeta()
	}
	return nil
}
