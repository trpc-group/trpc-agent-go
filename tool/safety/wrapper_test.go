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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const safeWorkspaceArguments = `{"command":"go test ./...","timeout_sec":30}`

type fakeCallableTool struct {
	declaration      tool.Declaration
	result           any
	err              error
	calls            int
	metadata         tool.ToolMetadata
	permission       tool.PermissionDecision
	permissionChecks int
	streamInner      bool
	innerTextMode    tool.InnerTextMode
}

func newFakeCallable(result any) *fakeCallableTool {
	return &fakeCallableTool{
		declaration: tool.Declaration{Name: "workspace_exec"},
		result:      result,
		streamInner: true,
	}
}

func (fake *fakeCallableTool) Declaration() *tool.Declaration {
	return &fake.declaration
}

func (fake *fakeCallableTool) Call(_ context.Context, _ []byte) (any, error) {
	fake.calls++
	return fake.result, fake.err
}

func (fake *fakeCallableTool) ToolMetadata() tool.ToolMetadata {
	return fake.metadata
}

func (fake *fakeCallableTool) CheckPermission(
	_ context.Context,
	_ *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	fake.permissionChecks++
	return fake.permission, nil
}

func (fake *fakeCallableTool) StreamInner() bool { return fake.streamInner }

func (fake *fakeCallableTool) InnerTextMode() tool.InnerTextMode {
	return fake.innerTextMode
}

type fakeInvocationStateTool struct {
	*fakeCallableTool
	delta map[string][]byte
}

func (fake *fakeInvocationStateTool) StateDeltaForInvocation(
	_ *agent.Invocation,
	_ string,
	_ []byte,
	_ []byte,
) map[string][]byte {
	if fake.delta != nil {
		return fake.delta
	}
	return map[string][]byte{"artifact": []byte("preserved")}
}

type fakeLegacyStateTool struct {
	*fakeCallableTool
	delta map[string][]byte
}

func (fake *fakeLegacyStateTool) StateDelta(
	_ string,
	_ []byte,
	_ []byte,
) map[string][]byte {
	return fake.delta
}

func newWrapperGuard(
	t *testing.T,
	configure func(*Policy),
) (*Guard, *memoryAuditor) {
	t.Helper()
	policy := DefaultPolicy()
	if configure != nil {
		configure(&policy)
	}
	auditor := &memoryAuditor{}
	guard, err := NewGuard(policy, WithAuditor(auditor))
	require.NoError(t, err)
	return guard, auditor
}

func wrapCallable(t *testing.T, guard *Guard, inner tool.Tool) tool.CallableTool {
	t.Helper()
	wrapped, err := WrapExecution(
		guard,
		inner,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)
	callable, ok := wrapped.(tool.CallableTool)
	require.True(t, ok)
	return callable
}

func TestWrapExecutionBlocksBeforeCallingInner(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := newFakeCallable("not called")
	wrapper := wrapCallable(t, guard, inner)

	result, err := wrapper.Call(
		context.Background(),
		[]byte(`{"command":"rm -rf /","timeout_sec":30}`),
	)
	require.Nil(t, result)
	require.Error(t, err)
	require.Zero(t, inner.calls)
	require.Len(t, auditor.events, 1)
	require.True(t, auditor.events[0].Blocked)
}

func TestWrapExecutionAllowsSafeResultOnce(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := newFakeCallable(map[string]any{"ok": true})
	wrapper := wrapCallable(t, guard, inner)

	result, err := wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"ok": true}, result)
	require.Equal(t, 1, inner.calls)
	require.Len(t, auditor.events, 1)
	require.False(t, auditor.events[0].Blocked)
}

func TestWrapExecutionWithholdsSensitiveOutput(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := newFakeCallable("api_key=top-secret-value")
	wrapper := wrapCallable(t, guard, inner)

	result, err := wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.Nil(t, result)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "top-secret-value")
	var safetyErr *ExecutionError
	require.True(t, errors.As(err, &safetyErr))
	require.Equal(t, "SECRET_IN_TOOL_OUTPUT", safetyErr.RuleID)
	require.Len(t, auditor.events, 2)
	require.Equal(t, auditPhasePostcheck, auditor.events[1].Phase)
	require.False(t, auditor.events[1].Blocked)
}

func TestWrapExecutionWithholdsOversizedOutput(t *testing.T) {
	guard, auditor := newWrapperGuard(t, func(policy *Policy) {
		policy.maxOutputBytes = 16
	})
	inner := newFakeCallable(strings.Repeat("x", 64))
	wrapper := wrapCallable(t, guard, inner)

	result, err := wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.Nil(t, result)
	require.ErrorContains(t, err, "RESOURCE_OUTPUT_LIMIT_EXCEEDED")
	require.Len(t, auditor.events, 2)
	require.Equal(t, "RESOURCE_OUTPUT_LIMIT_EXCEEDED", auditor.events[1].RuleID)
}

func TestWrapExecutionForwardsSemanticCapabilities(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := newFakeCallable("ok")
	inner.metadata = tool.ToolMetadata{OpenWorld: true, ConcurrencySafe: true}
	inner.permission = tool.AskPermission("native approval")
	inner.streamInner = false
	inner.innerTextMode = tool.InnerTextModeExclude
	wrapper := wrapCallable(t, guard, inner)

	require.Equal(t, inner.metadata, tool.MetadataOf(wrapper))
	checker := wrapper.(tool.PermissionChecker)
	decision, err := checker.CheckPermission(context.Background(), &tool.PermissionRequest{})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.False(t, wrapper.(interface{ StreamInner() bool }).StreamInner())
	require.Equal(t, tool.InnerTextModeExclude,
		wrapper.(interface{ InnerTextMode() tool.InnerTextMode }).InnerTextMode())
}

func TestWrapExecutionPreservesMetadataThroughDeclarationOverlay(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := newFakeCallable("ok")
	inner.metadata = tool.ToolMetadata{Destructive: true, OpenWorld: true}
	overlaid := itool.ApplyDeclarations(
		[]tool.Tool{inner},
		[]tool.Declaration{{Name: "workspace_exec", Description: "overlaid"}},
	)
	require.Len(t, overlaid, 1)

	wrapper := wrapCallable(t, guard, overlaid[0])
	require.Equal(t, inner.metadata, tool.MetadataOf(wrapper))
}

func TestWrapExecutionForwardsInvocationStateDelta(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := &fakeInvocationStateTool{fakeCallableTool: newFakeCallable("ok")}
	wrapper := wrapCallable(t, guard, inner)
	provider, ok := wrapper.(invocationStateDeltaProvider)
	require.True(t, ok)

	delta := provider.StateDeltaForInvocation(nil, "call-1", nil, nil)
	require.Equal(t, []byte("preserved"), delta["artifact"])
}

func TestWrapExecutionWithholdsSensitiveInvocationStateDelta(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := &fakeInvocationStateTool{
		fakeCallableTool: newFakeCallable("ok"),
		delta: map[string][]byte{
			"artifact": []byte(`{"content":"api_key=top-secret-value"}`),
		},
	}
	wrapper := wrapCallable(t, guard, inner)
	_, err := wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.NoError(t, err)
	provider := wrapper.(invocationStateDeltaProvider)

	delta := provider.StateDeltaForInvocation(nil, "call-1", nil, nil)
	require.Nil(t, delta)
	require.Len(t, auditor.events, 2)
	require.Equal(t, "SECRET_IN_STATE_DELTA", auditor.events[1].RuleID)
	require.Equal(t, auditPhasePostcheck, auditor.events[1].Phase)
	require.True(t, auditor.events[1].Redacted)
	require.False(t, auditor.events[1].Blocked)
}

func TestWrapExecutionWithholdsSplitSecretStateDelta(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := &fakeInvocationStateTool{
		fakeCallableTool: newFakeCallable("ok"),
		delta: map[string][]byte{
			"api_key": []byte("top-secret-value"),
		},
	}
	wrapper := wrapCallable(t, guard, inner)
	provider := wrapper.(invocationStateDeltaProvider)

	delta := provider.StateDeltaForInvocation(nil, "call-1", nil, nil)
	require.Nil(t, delta)
	require.Len(t, auditor.events, 1)
	require.Equal(t, "SECRET_IN_STATE_DELTA", auditor.events[0].RuleID)
}

func TestWrapExecutionWithholdsOversizedLegacyStateDelta(t *testing.T) {
	guard, auditor := newWrapperGuard(t, func(policy *Policy) {
		policy.maxOutputBytes = 16
	})
	inner := &fakeLegacyStateTool{
		fakeCallableTool: newFakeCallable("ok"),
		delta:            map[string][]byte{"artifact": []byte(strings.Repeat("x", 32))},
	}
	wrapper := wrapCallable(t, guard, inner)
	_, err := wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.NoError(t, err)
	provider := wrapper.(stateDeltaProvider)

	delta := provider.StateDelta("call-1", nil, nil)
	require.Nil(t, delta)
	require.Len(t, auditor.events, 2)
	require.Equal(t, "STATE_DELTA_LIMIT_EXCEEDED", auditor.events[1].RuleID)
}

func TestWrapExecutionUsesSemanticStreamCapability(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := &fakeStreamTool{
		declaration: tool.Declaration{Name: "workspace_exec"},
		chunks:      []tool.StreamChunk{{Content: "ok"}},
	}
	named := itool.NewUnprefixedNamedTool(inner)
	wrapper, err := WrapExecution(
		guard,
		named,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)
	_, callable := wrapper.(tool.CallableTool)
	_, streamable := wrapper.(tool.StreamableTool)
	require.False(t, callable)
	require.True(t, streamable)
}
