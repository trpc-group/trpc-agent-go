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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// WrapTool wraps a callable tool with safety preflight and completion
// handling. The wrapper owns the complete call lifecycle: it scans before
// execution, returns structured deny/ask results without calling the
// underlying tool, and redacts, limits, audits, and releases resources
// after execution.
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
}

func (w *wrappedCallableTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (result any, err error) {
	var toolCallID string
	lifecycleOwned := false
	finishing := false
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
	if checker, ok := w.tool.(tool.PermissionChecker); ok {
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
	defer w.guard.endWrappedCall()
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
