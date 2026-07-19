//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type stateDeltaProvider interface {
	StateDelta(string, []byte, []byte) map[string][]byte
}

type stateDeltaErrorProvider interface {
	StateDeltaWithError(
		context.Context,
		string,
		[]byte,
		[]byte,
	) (map[string][]byte, error)
}

type invocationStateDeltaProvider interface {
	StateDeltaForInvocation(
		*agent.Invocation,
		string,
		[]byte,
		[]byte,
	) map[string][]byte
}

type invocationStateDeltaErrorProvider interface {
	StateDeltaForInvocationWithError(
		context.Context,
		*agent.Invocation,
		string,
		[]byte,
		[]byte,
	) (map[string][]byte, error)
}

// ExecutionError reports a blocked precheck or withheld tool output.
type ExecutionError struct {
	Phase     string
	Decision  Decision
	RiskLevel RiskLevel
	RuleID    string
	message   string
}

// Error implements error without including raw tool input or output.
func (err *ExecutionError) Error() string {
	if err == nil {
		return "tool safety: execution blocked"
	}
	return err.message
}

type executionWrapper struct {
	guard    *Guard
	inner    tool.Tool
	semantic tool.Tool
	binding  Binding
	callable tool.CallableTool
}

// WrapExecution wraps one explicitly bound execution tool.
func WrapExecution(
	guard *Guard,
	inner tool.Tool,
	binding Binding,
) (tool.Tool, error) {
	if err := validateExecutionWrapper(guard, inner, binding); err != nil {
		return nil, err
	}
	base := &executionWrapper{
		guard:    guard,
		inner:    inner,
		semantic: itool.ResolveSemantic(inner),
		binding:  binding,
	}
	wrapped, err := base.wrapCallCapabilities()
	if err != nil {
		return nil, err
	}
	return wrapStateCapability(wrapped, base.semantic), nil
}

func validateExecutionWrapper(
	guard *Guard,
	inner tool.Tool,
	binding Binding,
) error {
	if err := validateExecutionGuard(guard); err != nil {
		return err
	}
	if isNilTool(inner) {
		return errors.New("tool safety: wrapped tool requires a declaration")
	}
	declaration := inner.Declaration()
	if declaration == nil {
		return errors.New("tool safety: wrapped tool requires a declaration")
	}
	if err := validateBinding(binding); err != nil {
		return err
	}
	if declaration.Name != binding.ToolName {
		return errors.New("tool safety: binding name must match wrapped tool")
	}
	return nil
}

func isNilTool(tl tool.Tool) bool {
	if tl == nil {
		return true
	}
	value := reflect.ValueOf(tl)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (wrapper *executionWrapper) wrapCallCapabilities() (tool.Tool, error) {
	_, hasCallable := wrapper.semantic.(tool.CallableTool)
	if !hasCallable {
		return nil, errors.New(
			"tool safety: wrapped tool must support non-streaming calls",
		)
	}
	callable, ok := wrapper.inner.(tool.CallableTool)
	if !ok {
		return nil, errors.New("tool safety: wrapped call capability is unavailable")
	}
	wrapper.callable = callable
	return &callableExecutionWrapper{executionWrapper: wrapper}, nil
}

func (wrapper *executionWrapper) Declaration() *tool.Declaration {
	return wrapper.inner.Declaration()
}

func (wrapper *executionWrapper) ToolMetadata() tool.ToolMetadata {
	return tool.MetadataOf(wrapper.semantic)
}

func (wrapper *executionWrapper) CheckPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	checker, ok := wrapper.semantic.(tool.PermissionChecker)
	if !ok {
		return tool.AllowPermission(), nil
	}
	return checker.CheckPermission(ctx, req)
}

func (wrapper *executionWrapper) StreamInner() bool {
	preference, ok := wrapper.semantic.(interface{ StreamInner() bool })
	return !ok || preference.StreamInner()
}

func (wrapper *executionWrapper) InnerTextMode() tool.InnerTextMode {
	preference, ok := wrapper.semantic.(interface {
		InnerTextMode() tool.InnerTextMode
	})
	if !ok {
		return tool.InnerTextModeInclude
	}
	return tool.NormalizeInnerTextMode(preference.InnerTextMode())
}

// LongRunning delegates the semantic tool's long-running preference.
func (wrapper *executionWrapper) LongRunning() bool {
	runner, ok := wrapper.semantic.(interface{ LongRunning() bool })
	return ok && runner.LongRunning()
}

type callableExecutionWrapper struct{ *executionWrapper }

func (wrapper *callableExecutionWrapper) Call(
	ctx context.Context,
	arguments []byte,
) (any, error) {
	return wrapper.call(ctx, arguments)
}

type invocationCallableWrapper struct {
	*callableExecutionWrapper
	provider any
}

func (wrapper *invocationCallableWrapper) StateDeltaForInvocation(
	invocation *agent.Invocation,
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	delta, err := wrapper.StateDeltaForInvocationWithError(
		context.Background(), invocation, toolCallID, arguments, result,
	)
	if err != nil {
		return nil
	}
	return delta
}

func (wrapper *invocationCallableWrapper) StateDeltaForInvocationWithError(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCallID string,
	arguments []byte,
	result []byte,
) (map[string][]byte, error) {
	var delta map[string][]byte
	if provider, ok := wrapper.provider.(invocationStateDeltaErrorProvider); ok {
		var err error
		delta, err = provider.StateDeltaForInvocationWithError(
			ctx, invocation, toolCallID, arguments, result,
		)
		if err != nil {
			return nil, err
		}
	} else if provider, ok := wrapper.provider.(invocationStateDeltaProvider); ok {
		delta = provider.StateDeltaForInvocation(
			invocation, toolCallID, arguments, result,
		)
	} else {
		return nil, nil
	}
	return wrapper.inspectStateDelta(ctx, delta)
}

type stateCallableWrapper struct {
	*callableExecutionWrapper
	provider any
}

func (wrapper *stateCallableWrapper) StateDelta(
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	delta, err := wrapper.StateDeltaWithError(
		context.Background(), toolCallID, arguments, result,
	)
	if err != nil {
		return nil
	}
	return delta
}

func (wrapper *stateCallableWrapper) StateDeltaWithError(
	ctx context.Context,
	toolCallID string,
	arguments []byte,
	result []byte,
) (map[string][]byte, error) {
	var delta map[string][]byte
	if provider, ok := wrapper.provider.(stateDeltaErrorProvider); ok {
		var err error
		delta, err = provider.StateDeltaWithError(
			ctx, toolCallID, arguments, result,
		)
		if err != nil {
			return nil, err
		}
	} else if provider, ok := wrapper.provider.(stateDeltaProvider); ok {
		delta = provider.StateDelta(toolCallID, arguments, result)
	} else {
		return nil, nil
	}
	return wrapper.inspectStateDelta(ctx, delta)
}

func wrapStateCapability(wrapped tool.Tool, semantic tool.Tool) tool.Tool {
	if _, ok := semantic.(invocationStateDeltaErrorProvider); ok {
		return wrapInvocationState(wrapped, semantic)
	}
	if _, ok := semantic.(stateDeltaErrorProvider); ok {
		return wrapLegacyState(wrapped, semantic)
	}
	if _, ok := semantic.(invocationStateDeltaProvider); ok {
		return wrapInvocationState(wrapped, semantic)
	}
	if _, ok := semantic.(stateDeltaProvider); ok {
		return wrapLegacyState(wrapped, semantic)
	}
	return wrapped
}

func wrapInvocationState(
	wrapped tool.Tool,
	provider any,
) tool.Tool {
	concrete, ok := wrapped.(*callableExecutionWrapper)
	if !ok {
		return wrapped
	}
	return &invocationCallableWrapper{concrete, provider}
}

func wrapLegacyState(wrapped tool.Tool, provider any) tool.Tool {
	concrete, ok := wrapped.(*callableExecutionWrapper)
	if !ok {
		return wrapped
	}
	return &stateCallableWrapper{concrete, provider}
}

func newExecutionError(report Report, phase string) *ExecutionError {
	return &ExecutionError{
		Phase:     phase,
		Decision:  report.Decision,
		RiskLevel: report.RiskLevel,
		RuleID:    report.RuleID,
		message: fmt.Sprintf(
			"tool safety %s: %s",
			phase,
			reportReason(report),
		),
	}
}
