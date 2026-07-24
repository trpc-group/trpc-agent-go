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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type stateDeltaProvider interface {
	StateDelta(string, []byte, []byte) map[string][]byte
}

type invocationStateDeltaProvider interface {
	StateDeltaForInvocation(
		*agent.Invocation,
		string,
		[]byte,
		[]byte,
	) map[string][]byte
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
	guard      *Guard
	inner      tool.Tool
	semantic   tool.Tool
	binding    Binding
	callable   tool.CallableTool
	streamable tool.StreamableTool
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
	if inner == nil || inner.Declaration() == nil {
		return errors.New("tool safety: wrapped tool requires a declaration")
	}
	if err := validateBinding(binding); err != nil {
		return err
	}
	if inner.Declaration().Name != binding.ToolName {
		return errors.New("tool safety: binding name must match wrapped tool")
	}
	return nil
}

func (wrapper *executionWrapper) wrapCallCapabilities() (tool.Tool, error) {
	_, hasCallable := wrapper.semantic.(tool.CallableTool)
	_, hasStreamable := wrapper.semantic.(tool.StreamableTool)
	if hasCallable {
		wrapper.callable, hasCallable = wrapper.inner.(tool.CallableTool)
	}
	if hasStreamable {
		wrapper.streamable, hasStreamable = wrapper.inner.(tool.StreamableTool)
	}
	switch {
	case hasCallable && hasStreamable:
		return &dualExecutionWrapper{executionWrapper: wrapper}, nil
	case hasCallable:
		return &callableExecutionWrapper{executionWrapper: wrapper}, nil
	case hasStreamable:
		return &streamableExecutionWrapper{executionWrapper: wrapper}, nil
	default:
		return nil, errors.New("tool safety: wrapped tool is not executable")
	}
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

type callableExecutionWrapper struct{ *executionWrapper }

func (wrapper *callableExecutionWrapper) Call(
	ctx context.Context,
	arguments []byte,
) (any, error) {
	return wrapper.call(ctx, arguments)
}

type streamableExecutionWrapper struct{ *executionWrapper }

func (wrapper *streamableExecutionWrapper) StreamableCall(
	ctx context.Context,
	arguments []byte,
) (*tool.StreamReader, error) {
	return wrapper.stream(ctx, arguments)
}

type dualExecutionWrapper struct{ *executionWrapper }

func (wrapper *dualExecutionWrapper) Call(
	ctx context.Context,
	arguments []byte,
) (any, error) {
	return wrapper.call(ctx, arguments)
}

func (wrapper *dualExecutionWrapper) StreamableCall(
	ctx context.Context,
	arguments []byte,
) (*tool.StreamReader, error) {
	return wrapper.stream(ctx, arguments)
}

type invocationCallableWrapper struct {
	*callableExecutionWrapper
	provider invocationStateDeltaProvider
}

func (wrapper *invocationCallableWrapper) StateDeltaForInvocation(
	invocation *agent.Invocation,
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	delta := wrapper.provider.StateDeltaForInvocation(
		invocation, toolCallID, arguments, result,
	)
	return wrapper.inspectStateDelta(delta)
}

type invocationStreamableWrapper struct {
	*streamableExecutionWrapper
	provider invocationStateDeltaProvider
}

func (wrapper *invocationStreamableWrapper) StateDeltaForInvocation(
	invocation *agent.Invocation,
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	delta := wrapper.provider.StateDeltaForInvocation(
		invocation, toolCallID, arguments, result,
	)
	return wrapper.inspectStateDelta(delta)
}

type invocationDualWrapper struct {
	*dualExecutionWrapper
	provider invocationStateDeltaProvider
}

func (wrapper *invocationDualWrapper) StateDeltaForInvocation(
	invocation *agent.Invocation,
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	delta := wrapper.provider.StateDeltaForInvocation(
		invocation, toolCallID, arguments, result,
	)
	return wrapper.inspectStateDelta(delta)
}

type stateCallableWrapper struct {
	*callableExecutionWrapper
	provider stateDeltaProvider
}

func (wrapper *stateCallableWrapper) StateDelta(
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	delta := wrapper.provider.StateDelta(toolCallID, arguments, result)
	return wrapper.inspectStateDelta(delta)
}

type stateStreamableWrapper struct {
	*streamableExecutionWrapper
	provider stateDeltaProvider
}

func (wrapper *stateStreamableWrapper) StateDelta(
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	delta := wrapper.provider.StateDelta(toolCallID, arguments, result)
	return wrapper.inspectStateDelta(delta)
}

type stateDualWrapper struct {
	*dualExecutionWrapper
	provider stateDeltaProvider
}

func (wrapper *stateDualWrapper) StateDelta(
	toolCallID string,
	arguments []byte,
	result []byte,
) map[string][]byte {
	delta := wrapper.provider.StateDelta(toolCallID, arguments, result)
	return wrapper.inspectStateDelta(delta)
}

func wrapStateCapability(wrapped tool.Tool, semantic tool.Tool) tool.Tool {
	if provider, ok := semantic.(invocationStateDeltaProvider); ok {
		return wrapInvocationState(wrapped, provider)
	}
	if provider, ok := semantic.(stateDeltaProvider); ok {
		return wrapLegacyState(wrapped, provider)
	}
	return wrapped
}

func wrapInvocationState(
	wrapped tool.Tool,
	provider invocationStateDeltaProvider,
) tool.Tool {
	switch concrete := wrapped.(type) {
	case *callableExecutionWrapper:
		return &invocationCallableWrapper{concrete, provider}
	case *streamableExecutionWrapper:
		return &invocationStreamableWrapper{concrete, provider}
	case *dualExecutionWrapper:
		return &invocationDualWrapper{concrete, provider}
	default:
		return wrapped
	}
}

func wrapLegacyState(wrapped tool.Tool, provider stateDeltaProvider) tool.Tool {
	switch concrete := wrapped.(type) {
	case *callableExecutionWrapper:
		return &stateCallableWrapper{concrete, provider}
	case *streamableExecutionWrapper:
		return &stateStreamableWrapper{concrete, provider}
	case *dualExecutionWrapper:
		return &stateDualWrapper{concrete, provider}
	default:
		return wrapped
	}
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
