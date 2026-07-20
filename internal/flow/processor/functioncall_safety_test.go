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
				safetyTestToolName, safety.BackendCustom, test.adapter,
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
