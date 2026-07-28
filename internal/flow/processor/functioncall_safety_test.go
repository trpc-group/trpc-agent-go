//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

const safetyTestToolName = "custom.exec"

type invalidSafetyAdapter struct {
	input  safety.ScanInput
	panics bool
}

func (adapter invalidSafetyAdapter) Adapt(
	context.Context,
	safety.AdaptRequest,
	safety.Binding,
) (safety.ScanInput, error) {
	if adapter.panics {
		panic("adapter panic")
	}
	return adapter.input, nil
}

type safetyCanaryTool struct {
	calls int
}

type guardedSafetyTool struct {
	calls             int
	result            any
	err               error
	skipSummarization bool
}

func (*guardedSafetyTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: safetyTestToolName}
}

func (guarded *guardedSafetyTool) Call(context.Context, []byte) (any, error) {
	guarded.calls++
	return guarded.result, guarded.err
}

func (guarded *guardedSafetyTool) SkipSummarization() bool {
	return guarded.skipSummarization
}

func (*safetyCanaryTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: safetyTestToolName}
}

func (canary *safetyCanaryTool) Call(context.Context, []byte) (any, error) {
	canary.calls++
	return map[string]bool{"executed": true}, nil
}

type safetyAuditRecorder struct {
	events []safety.AuditEvent
}

func (recorder *safetyAuditRecorder) Record(
	_ context.Context,
	event safety.AuditEvent,
) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func TestInvalidSafetyAdapterStopsFrameworkToolExecution(t *testing.T) {
	tests := []struct {
		name    string
		adapter safety.InputAdapter
	}{
		{name: "zero output", adapter: invalidSafetyAdapter{}},
		{name: "partial output", adapter: invalidSafetyAdapter{input: safety.ScanInput{
			Operation: safety.OperationExecute,
		}}},
		{name: "panic", adapter: invalidSafetyAdapter{panics: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			canary := &safetyCanaryTool{}
			auditor := &safetyAuditRecorder{}
			guard, err := safety.NewGuard(
				safety.DefaultPolicy(), safety.WithAuditor(auditor),
			)
			require.NoError(t, err)
			policy, err := safety.NewPermissionPolicy(guard, safety.BindCustom(
				safetyTestToolName, test.adapter,
			))
			require.NoError(t, err)
			invocation := &agent.Invocation{RunOptions: agent.NewRunOptions(
				agent.WithToolPermissionPolicy(policy),
			)}

			_, result, _, _, _, err := NewFunctionCallResponseProcessor(false, nil).
				executeToolWithCallbacks(
					context.Background(), invocation, model.ToolCall{
						Function: model.FunctionDefinitionParam{
							Name: safetyTestToolName, Arguments: []byte(`{}`),
						},
					}, canary, nil,
				)
			require.NoError(t, err)
			require.Zero(t, canary.calls)
			permissionResult, ok := result.(tool.PermissionResult)
			require.True(t, ok)
			require.Equal(t, tool.PermissionResultStatusApprovalRequired, permissionResult.Status)
			require.Contains(t, permissionResult.Reason, "TOOL_INPUT_UNPARSABLE")
			require.Len(t, auditor.events, 1)
			require.True(t, auditor.events[0].Blocked)
			require.Equal(t, "TOOL_INPUT_UNPARSABLE", auditor.events[0].RuleID)
		})
	}
}

func TestSafetyWrapperPreservesFrameworkSkipSummarization(t *testing.T) {
	inner := &guardedSafetyTool{
		result:            map[string]bool{"ok": true},
		skipSummarization: true,
	}
	guard, err := safety.NewGuard(
		safety.DefaultPolicy(),
		safety.WithAuditor(&safetyAuditRecorder{}),
	)
	require.NoError(t, err)
	wrapped, err := safety.WrapOutputGuard(
		guard,
		inner,
		safety.BindCustom(safetyTestToolName, invalidSafetyAdapter{}),
	)
	require.NoError(t, err)

	require.True(t, toolPrefersSkipSummarization(wrapped))
}

func TestSafetyWrapperPreservesStopErrorWithoutRetryOrOutput(t *testing.T) {
	inner := &guardedSafetyTool{
		result: map[string]string{"password": "secret-value"},
		err:    agent.NewStopError("stop with secret-value"),
	}
	guard, err := safety.NewGuard(
		safety.DefaultPolicy(),
		safety.WithAuditor(&safetyAuditRecorder{}),
	)
	require.NoError(t, err)
	wrapped, err := safety.WrapOutputGuard(
		guard,
		inner,
		safety.BindCustom(safetyTestToolName, invalidSafetyAdapter{}),
	)
	require.NoError(t, err)
	callable, ok := wrapped.(tool.CallableTool)
	require.True(t, ok)
	processor := NewFunctionCallResponseProcessor(
		false,
		nil,
		WithToolCallRetryPolicy(&tool.RetryPolicy{
			MaxAttempts: 3,
			RetryOn: func(context.Context, *tool.RetryInfo) (bool, error) {
				return true, nil
			},
		}),
	)

	_, result, callErr := processor.executeCallableTool(
		context.Background(),
		model.ToolCall{Function: model.FunctionDefinitionParam{
			Name: safetyTestToolName, Arguments: []byte(`{}`),
		}},
		callable,
	)

	require.Nil(t, result)
	_, ok = agent.AsStopError(callErr)
	require.True(t, ok)
	require.NotContains(t, callErr.Error(), "secret-value")
	require.Equal(t, 1, inner.calls)
}
