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
	"time"

	"github.com/stretchr/testify/require"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	workspaceToolName = "workspace_exec"
	testOutputLimit   = int64(16)
	testTimeout       = 10 * time.Millisecond
	testStreamBuffer  = 0
)

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
	longRunning      bool
	call             func(context.Context, []byte) (any, error)
}

type safePayload struct {
	Status string `json:"status"`
}

type uninspectablePayload struct{ Callback func() }

type panicPayload struct{}

func (panicPayload) MarshalJSON() ([]byte, error) {
	panic("marshal panic")
}

func newFakeCallable(result any) *fakeCallableTool {
	return &fakeCallableTool{
		declaration: tool.Declaration{Name: workspaceToolName},
		result:      result, streamInner: true,
	}
}

func (fake *fakeCallableTool) Declaration() *tool.Declaration {
	return &fake.declaration
}

func (fake *fakeCallableTool) Call(
	ctx context.Context,
	arguments []byte,
) (any, error) {
	fake.calls++
	if fake.call != nil {
		return fake.call(ctx, arguments)
	}
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

func (fake *fakeCallableTool) LongRunning() bool { return fake.longRunning }

type minimalCallableTool struct {
	declaration *tool.Declaration
	result      any
}

func (minimal *minimalCallableTool) Declaration() *tool.Declaration {
	return minimal.declaration
}

func (minimal *minimalCallableTool) Call(
	_ context.Context,
	_ []byte,
) (any, error) {
	return minimal.result, nil
}

type fakeDualTool struct{ *fakeCallableTool }

func (fake *fakeDualTool) StreamableCall(
	_ context.Context,
	_ []byte,
) (*tool.StreamReader, error) {
	return tool.NewStream(testStreamBuffer).Reader, nil
}

type fakeStateTool struct{ *fakeCallableTool }

func (fake *fakeStateTool) StateDelta(
	_ string,
	_ []byte,
	_ []byte,
) map[string][]byte {
	return nil
}

type panicAuditor struct{}

func (panicAuditor) Record(context.Context, AuditEvent) error {
	panic("audit panic")
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

func wrapOutput(
	t *testing.T,
	guard *Guard,
	inner tool.Tool,
) tool.CallableTool {
	t.Helper()
	wrapper, err := WrapOutputGuard(
		guard, inner, BindWorkspaceExec(workspaceToolName),
	)
	require.NoError(t, err)
	callable, ok := wrapper.(tool.CallableTool)
	require.True(t, ok)
	return callable
}

func TestWrapOutputGuardPreservesSafeResult(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	want := &safePayload{Status: "ok"}
	inner := newFakeCallable(want)
	wrapper := wrapOutput(t, guard, inner)

	result, err := wrapper.Call(context.Background(), nil)
	require.NoError(t, err)
	require.Same(t, want, result)
	require.Equal(t, 1, inner.calls)
	require.Empty(t, auditor.events)
}

func TestWrapOutputGuardDoesNotRepeatPermissionPrecheck(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := newFakeCallable("executed")
	wrapper := wrapOutput(t, guard, inner)

	result, err := wrapper.Call(
		context.Background(), []byte(`{"command":"rm -rf /"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "executed", result)
	require.Equal(t, 1, inner.calls)
	require.Empty(t, auditor.events)
}

func TestWrapOutputGuardReturnsBlockedResult(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := newFakeCallable(map[string]string{"token": "ghp_abcdefghijklmnopqrstuvwxyz123456"})
	wrapper := wrapOutput(t, guard, inner)

	result, err := wrapper.Call(context.Background(), nil)
	require.NoError(t, err)
	blocked, ok := result.(BlockedResult)
	require.True(t, ok)
	require.Equal(t, ruleOutputSecret, blocked.RuleID)
	require.True(t, blocked.Blocked)
	require.True(t, blocked.Redacted)
	require.Len(t, auditor.events, 1)
	require.True(t, auditor.events[0].Blocked)
}

func TestWrapOutputGuardBlocksOversizedAndUninspectableResults(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Policy)
		result    any
		ruleID    string
	}{
		{
			name: "oversized", configure: func(policy *Policy) {
				policy.maxOutputBytes = testOutputLimit
			},
			result: strings.Repeat("x", int(testOutputLimit)), ruleID: ruleOutputLimit,
		},
		{name: "uninspectable", result: uninspectablePayload{}, ruleID: ruleOutputUninspectable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard, _ := newWrapperGuard(t, test.configure)
			result, err := wrapOutput(t, guard, newFakeCallable(test.result)).Call(
				context.Background(), nil,
			)
			require.NoError(t, err)
			require.Equal(t, test.ruleID, result.(BlockedResult).RuleID)
		})
	}
}

func TestWrapOutputGuardHandlesToolErrors(t *testing.T) {
	plainErr := errors.New("ordinary failure")
	guard, _ := newWrapperGuard(t, nil)
	plain := newFakeCallable(nil)
	plain.err = plainErr
	result, err := wrapOutput(t, guard, plain).Call(context.Background(), nil)
	require.Nil(t, result)
	require.ErrorIs(t, err, plainErr)

	secret := newFakeCallable(nil)
	secret.err = errors.New("password=super-secret-value")
	result, err = wrapOutput(t, guard, secret).Call(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, ruleOutputSecret, result.(BlockedResult).RuleID)
}

func TestWrapOutputGuardBlocksOversizedToolError(t *testing.T) {
	guard, _ := newWrapperGuard(t, func(policy *Policy) {
		policy.maxOutputBytes = testOutputLimit
	})
	inner := newFakeCallable(nil)
	inner.err = errors.New(strings.Repeat("x", int(testOutputLimit)+1))

	result, err := wrapOutput(t, guard, inner).Call(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, ruleOutputLimit, result.(BlockedResult).RuleID)
}

func TestWrapOutputGuardEnforcesRuntimeTimeout(t *testing.T) {
	guard, _ := newWrapperGuard(t, func(policy *Policy) {
		policy.maxTimeout = testTimeout
	})
	inner := newFakeCallable(nil)
	inner.call = func(ctx context.Context, _ []byte) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	result, err := wrapOutput(t, guard, inner).Call(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, ruleExecutionTimeout, result.(BlockedResult).RuleID)
	require.Equal(t, 1, inner.calls)
}

func TestWrapOutputGuardRecoversPostcheckPanic(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	result, err := wrapOutput(t, guard, newFakeCallable(panicPayload{})).Call(
		context.Background(), nil,
	)
	require.NoError(t, err)
	require.Equal(t, rulePostcheckFailed, result.(BlockedResult).RuleID)
	require.Len(t, auditor.events, 1)
}

func TestWrapOutputGuardForwardsSemanticCapabilities(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := newFakeCallable("ok")
	inner.metadata = tool.ToolMetadata{OpenWorld: true, ConcurrencySafe: true}
	inner.permission = tool.AskPermission("native approval")
	inner.longRunning = true
	wrapper := wrapOutput(t, guard, inner)

	require.Equal(t, inner.metadata, tool.MetadataOf(wrapper))
	decision, err := wrapper.(tool.PermissionChecker).CheckPermission(
		context.Background(), &tool.PermissionRequest{},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.True(t, wrapper.(interface{ LongRunning() bool }).LongRunning())
}

func TestWrapOutputGuardPreservesDeclarationOverlay(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := newFakeCallable("ok")
	inner.metadata = tool.ToolMetadata{Destructive: true}
	overlaid := itool.ApplyDeclarations(
		[]tool.Tool{inner},
		[]tool.Declaration{{Name: workspaceToolName, Description: "overlaid"}},
	)
	require.Len(t, overlaid, 1)

	wrapper, err := WrapOutputGuard(
		guard, overlaid[0], BindWorkspaceExec(workspaceToolName),
	)
	require.NoError(t, err)
	require.Equal(t, "overlaid", wrapper.Declaration().Description)
	require.Equal(t, inner.metadata, tool.MetadataOf(wrapper))
}

func TestWrapOutputGuardRejectsUnsupportedCapabilities(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	tests := []struct {
		name  string
		inner tool.Tool
	}{
		{name: "streaming", inner: &fakeDualTool{newFakeCallable("ok")}},
		{name: "state delta", inner: &fakeStateTool{newFakeCallable("ok")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapper, err := WrapOutputGuard(
				guard, test.inner, BindWorkspaceExec(workspaceToolName),
			)
			require.Nil(t, wrapper)
			require.Error(t, err)
		})
	}
}

func TestWrapOutputGuardRejectsInvalidConstruction(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	guardWithoutAudit, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	var typedNil *minimalCallableTool
	tests := []struct {
		name    string
		guard   *Guard
		inner   tool.Tool
		binding Binding
	}{
		{name: "nil guard", inner: newFakeCallable("ok"), binding: BindWorkspaceExec(workspaceToolName)},
		{name: "nil auditor", guard: guardWithoutAudit, inner: newFakeCallable("ok"), binding: BindWorkspaceExec(workspaceToolName)},
		{name: "typed nil", guard: guard, inner: typedNil, binding: BindWorkspaceExec(workspaceToolName)},
		{name: "nil declaration", guard: guard, inner: &minimalCallableTool{}, binding: BindWorkspaceExec(workspaceToolName)},
		{name: "name mismatch", guard: guard, inner: newFakeCallable("ok"), binding: BindWorkspaceExec("other")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapper, wrapErr := WrapOutputGuard(test.guard, test.inner, test.binding)
			require.Nil(t, wrapper)
			require.Error(t, wrapErr)
		})
	}
}

func TestWrapOutputGuardConvertsAuditFailuresToBlockedResult(t *testing.T) {
	auditor := &memoryAuditor{err: errors.New("audit unavailable")}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	result, callErr := wrapOutput(
		t, guard, newFakeCallable(map[string]string{"password": "secret-value"}),
	).Call(context.Background(), nil)
	require.NoError(t, callErr)
	require.Equal(t, "AUDIT_WRITE_FAILED", result.(BlockedResult).RuleID)
}

func TestWrapOutputGuardRecoversAuditorPanic(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(panicAuditor{}))
	require.NoError(t, err)
	result, callErr := wrapOutput(
		t, guard, newFakeCallable(map[string]string{"password": "secret-value"}),
	).Call(context.Background(), nil)
	require.NoError(t, callErr)
	require.Equal(t, "AUDIT_WRITE_FAILED", result.(BlockedResult).RuleID)
}
