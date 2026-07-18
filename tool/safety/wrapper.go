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
	concrete, ok := wrapped.(*callableExecutionWrapper)
	if !ok {
		return wrapped
	}
	return &invocationCallableWrapper{concrete, provider}
}

func wrapLegacyState(wrapped tool.Tool, provider stateDeltaProvider) tool.Tool {
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
