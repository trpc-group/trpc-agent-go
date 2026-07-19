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
	"encoding/json"
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
	longRunning      bool
}

type maskedResult struct {
	secret string
}

func (maskedResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"status":"safe"}`), nil
}

type failSecondAudit struct {
	calls int
}

func (auditor *failSecondAudit) Record(
	_ context.Context,
	_ AuditEvent,
) error {
	auditor.calls++
	if auditor.calls == 2 {
		return errors.New("audit unavailable")
	}
	return nil
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

func (fake *fakeCallableTool) LongRunning() bool { return fake.longRunning }

type fakeStreamOnlyTool struct {
	declaration tool.Declaration
}

func (fake *fakeStreamOnlyTool) Declaration() *tool.Declaration {
	return &fake.declaration
}

func (fake *fakeStreamOnlyTool) StreamableCall(
	_ context.Context,
	_ []byte,
) (*tool.StreamReader, error) {
	return tool.NewStream(0).Reader, nil
}

type fakeDualTool struct{ *fakeCallableTool }

type nilDeclarationCallable struct{}

func (*nilDeclarationCallable) Declaration() *tool.Declaration { return nil }

func (*nilDeclarationCallable) Call(context.Context, []byte) (any, error) {
	return "ok", nil
}

type minimalCallableTool struct {
	declaration tool.Declaration
	result      any
	err         error
}

func (minimal *minimalCallableTool) Declaration() *tool.Declaration {
	return &minimal.declaration
}

func (minimal *minimalCallableTool) Call(context.Context, []byte) (any, error) {
	return minimal.result, minimal.err
}

func (fake *fakeDualTool) StreamableCall(
	_ context.Context,
	_ []byte,
) (*tool.StreamReader, error) {
	return tool.NewStream(0).Reader, nil
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

type fakeErrorInvocationStateTool struct {
	*fakeInvocationStateTool
	err error
}

func (fake *fakeErrorInvocationStateTool) StateDeltaForInvocationWithError(
	_ context.Context,
	_ *agent.Invocation,
	_ string,
	_ []byte,
	_ []byte,
) (map[string][]byte, error) {
	return fake.delta, fake.err
}

type fakeErrorLegacyStateTool struct {
	*fakeLegacyStateTool
	err error
}

type fakeInvocationLegacyStateErrorTool struct {
	*fakeInvocationStateTool
	err error
}

func (fake *fakeInvocationLegacyStateErrorTool) StateDeltaWithError(
	_ context.Context,
	_ string,
	_ []byte,
	_ []byte,
) (map[string][]byte, error) {
	return nil, fake.err
}

type fakeErrorOnlyInvocationStateTool struct {
	*fakeCallableTool
	delta map[string][]byte
	err   error
}

func (fake *fakeErrorOnlyInvocationStateTool) StateDeltaForInvocationWithError(
	_ context.Context,
	_ *agent.Invocation,
	_ string,
	_ []byte,
	_ []byte,
) (map[string][]byte, error) {
	return fake.delta, fake.err
}

type fakeErrorOnlyLegacyStateTool struct {
	*fakeCallableTool
	delta map[string][]byte
	err   error
}

func (fake *fakeErrorOnlyLegacyStateTool) StateDeltaWithError(
	_ context.Context,
	_ string,
	_ []byte,
	_ []byte,
) (map[string][]byte, error) {
	return fake.delta, fake.err
}

func (fake *fakeErrorLegacyStateTool) StateDeltaWithError(
	_ context.Context,
	_ string,
	_ []byte,
	_ []byte,
) (map[string][]byte, error) {
	return fake.delta, fake.err
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
	raw, ok := result.(json.RawMessage)
	require.True(t, ok)
	require.Equal(t, json.RawMessage(`{"ok":true}`), raw)
	require.Equal(t, 1, inner.calls)
	require.Len(t, auditor.events, 1)
	require.False(t, auditor.events[0].Blocked)
}

func TestWrapExecutionReturnsInspectedRepresentation(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := newFakeCallable(maskedResult{secret: "api_key=top-secret-value"})
	wrapper := wrapCallable(t, guard, inner)

	result, err := wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.NoError(t, err)
	raw, ok := result.(json.RawMessage)
	require.True(t, ok)
	require.Equal(t, json.RawMessage(`{"status":"safe"}`), raw)
	require.NotContains(t, string(raw), "top-secret-value")
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

func TestWrapExecutionSuppliesDefaultsForOptionalCapabilities(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	wrapper := wrapCallable(t, guard, &minimalCallableTool{
		declaration: tool.Declaration{Name: "workspace_exec"},
		result:      "ok",
	})

	decision, err := wrapper.(tool.PermissionChecker).CheckPermission(
		context.Background(), &tool.PermissionRequest{},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
	require.True(t, wrapper.(interface{ StreamInner() bool }).StreamInner())
	require.Equal(t, tool.InnerTextModeInclude,
		wrapper.(interface{ InnerTextMode() tool.InnerTextMode }).InnerTextMode())
	require.False(t, wrapper.(interface{ LongRunning() bool }).LongRunning())
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

func TestWrapExecutionPropagatesStateDeltaAuditFailure(t *testing.T) {
	for _, test := range []struct {
		name  string
		inner tool.Tool
		check func(t *testing.T, wrapped tool.CallableTool)
	}{
		{
			name: "invocation provider",
			inner: &fakeInvocationStateTool{
				fakeCallableTool: newFakeCallable("ok"),
				delta: map[string][]byte{
					"api_key": []byte("top-secret-value"),
				},
			},
			check: func(t *testing.T, wrapped tool.CallableTool) {
				provider := wrapped.(invocationStateDeltaErrorProvider)
				delta, err := provider.StateDeltaForInvocationWithError(
					context.Background(), nil, "call-1", nil, nil,
				)
				require.Nil(t, delta)
				require.ErrorContains(t, err, "AUDIT_WRITE_FAILED")
			},
		},
		{
			name: "legacy provider",
			inner: &fakeLegacyStateTool{
				fakeCallableTool: newFakeCallable("ok"),
				delta: map[string][]byte{
					"api_key": []byte("top-secret-value"),
				},
			},
			check: func(t *testing.T, wrapped tool.CallableTool) {
				provider := wrapped.(stateDeltaErrorProvider)
				delta, err := provider.StateDeltaWithError(
					context.Background(), "call-1", nil, nil,
				)
				require.Nil(t, delta)
				require.ErrorContains(t, err, "AUDIT_WRITE_FAILED")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			auditor := &failSecondAudit{}
			guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
			require.NoError(t, err)
			wrapped := wrapCallable(t, guard, test.inner)
			_, err = wrapped.Call(
				context.Background(), []byte(safeWorkspaceArguments),
			)
			require.NoError(t, err)

			test.check(t, wrapped)
			require.Equal(t, 2, auditor.calls)
		})
	}
}

func TestWrapExecutionPreservesInnerStateDeltaErrors(t *testing.T) {
	wantErr := errors.New("inner state delta failed")
	guard, _ := newWrapperGuard(t, nil)

	invocationInner := &fakeErrorInvocationStateTool{
		fakeInvocationStateTool: &fakeInvocationStateTool{
			fakeCallableTool: newFakeCallable("ok"),
		},
		err: wantErr,
	}
	invocationWrapped := wrapCallable(t, guard, invocationInner)
	_, err := invocationWrapped.(invocationStateDeltaErrorProvider).
		StateDeltaForInvocationWithError(
			context.Background(), nil, "call-1", nil, nil,
		)
	require.ErrorIs(t, err, wantErr)

	legacyInner := &fakeErrorLegacyStateTool{
		fakeLegacyStateTool: &fakeLegacyStateTool{
			fakeCallableTool: newFakeCallable("ok"),
		},
		err: wantErr,
	}
	legacyWrapped := wrapCallable(t, guard, legacyInner)
	_, err = legacyWrapped.(stateDeltaErrorProvider).StateDeltaWithError(
		context.Background(), "call-1", nil, nil,
	)
	require.ErrorIs(t, err, wantErr)
}

func TestWrapExecutionPrefersErrorAwareStateOverLegacyInvocationState(t *testing.T) {
	wantErr := errors.New("state delta failed")
	guard, _ := newWrapperGuard(t, nil)
	inner := &fakeInvocationLegacyStateErrorTool{
		fakeInvocationStateTool: &fakeInvocationStateTool{
			fakeCallableTool: newFakeCallable("ok"),
		},
		err: wantErr,
	}
	wrapper := wrapCallable(t, guard, inner)
	provider, ok := wrapper.(stateDeltaErrorProvider)
	require.True(t, ok)
	_, err := provider.StateDeltaWithError(
		context.Background(), "call-1", nil, nil,
	)
	require.ErrorIs(t, err, wantErr)
}

func TestWrapExecutionPreservesErrorOnlyStateCapabilities(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	wantDelta := map[string][]byte{"artifact": []byte("preserved")}

	invocationWrapped := wrapCallable(t, guard, &fakeErrorOnlyInvocationStateTool{
		fakeCallableTool: newFakeCallable("ok"),
		delta:            wantDelta,
	})
	invocationDelta, err := invocationWrapped.(invocationStateDeltaErrorProvider).
		StateDeltaForInvocationWithError(
			context.Background(), nil, "call-1", nil, nil,
		)
	require.NoError(t, err)
	require.Equal(t, wantDelta, invocationDelta)
	require.Equal(
		t,
		wantDelta,
		invocationWrapped.(invocationStateDeltaProvider).StateDeltaForInvocation(
			nil, "call-1", nil, nil,
		),
	)

	legacyWrapped := wrapCallable(t, guard, &fakeErrorOnlyLegacyStateTool{
		fakeCallableTool: newFakeCallable("ok"),
		delta:            wantDelta,
	})
	legacyDelta, err := legacyWrapped.(stateDeltaErrorProvider).StateDeltaWithError(
		context.Background(), "call-1", nil, nil,
	)
	require.NoError(t, err)
	require.Equal(t, wantDelta, legacyDelta)
	require.Equal(
		t,
		wantDelta,
		legacyWrapped.(stateDeltaProvider).StateDelta("call-1", nil, nil),
	)

	failingWrapped := wrapCallable(t, guard, &fakeErrorOnlyLegacyStateTool{
		fakeCallableTool: newFakeCallable("ok"),
		err:              errors.New("state delta failed"),
	})
	require.Nil(
		t,
		failingWrapped.(stateDeltaProvider).StateDelta("call-1", nil, nil),
	)

	failingInvocation := wrapCallable(t, guard, &fakeErrorOnlyInvocationStateTool{
		fakeCallableTool: newFakeCallable("ok"),
		err:              errors.New("invocation state delta failed"),
	})
	require.Nil(
		t,
		failingInvocation.(invocationStateDeltaProvider).StateDeltaForInvocation(
			nil, "call-1", nil, nil,
		),
	)
}

func TestWrapExecutionForwardsSemanticLongRunning(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	base := newFakeCallable(nil)
	base.longRunning = true
	tests := []struct {
		name  string
		inner tool.Tool
	}{
		{name: "callable", inner: base},
		{name: "invocation state", inner: &fakeInvocationStateTool{
			fakeCallableTool: base,
		}},
		{name: "legacy state", inner: &fakeLegacyStateTool{
			fakeCallableTool: base,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertWrappedLongRunning(t, guard, test.inner)
		})
	}
}

func assertWrappedLongRunning(t *testing.T, guard *Guard, inner tool.Tool) {
	t.Helper()
	wrapper, err := WrapExecution(
		guard,
		inner,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)
	overlaid := itool.ApplyDeclarations(
		[]tool.Tool{wrapper},
		[]tool.Declaration{{Name: "workspace_exec", Description: "overlaid"}},
	)
	require.Len(t, overlaid, 1)
	runner, ok := itool.ResolveDeclaration(overlaid[0]).(interface {
		LongRunning() bool
	})
	require.True(t, ok)
	require.True(t, runner.LongRunning())
}

func TestWrapExecutionRejectsStreamOnlyTool(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := &fakeStreamOnlyTool{
		declaration: tool.Declaration{Name: "workspace_exec"},
	}

	wrapper, err := WrapExecution(
		guard,
		inner,
		BindWorkspaceExec("workspace_exec"),
	)
	require.Nil(t, wrapper)
	require.ErrorContains(t, err, "must support non-streaming calls")
}

func TestWrapExecutionNarrowsDualToolToCallable(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	inner := &fakeDualTool{fakeCallableTool: newFakeCallable("ok")}

	wrapper, err := WrapExecution(
		guard,
		inner,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)
	_, callable := wrapper.(tool.CallableTool)
	_, streamable := wrapper.(tool.StreamableTool)
	require.True(t, callable)
	require.False(t, streamable)
}

func TestWrapExecutionRejectsTypedNil(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	var inner *fakeCallableTool

	require.NotPanics(t, func() {
		wrapped, err := WrapExecution(
			guard,
			inner,
			BindWorkspaceExec("workspace_exec"),
		)
		require.Nil(t, wrapped)
		require.ErrorContains(t, err, "requires a declaration")
	})
}

func TestWrapExecutionRejectsInvalidConstruction(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	tests := []struct {
		name    string
		guard   *Guard
		inner   tool.Tool
		binding Binding
		want    string
	}{
		{
			name: "nil guard", inner: newFakeCallable("ok"),
			binding: BindWorkspaceExec("workspace_exec"), want: "guard",
		},
		{
			name: "nil tool", guard: guard,
			binding: BindWorkspaceExec("workspace_exec"), want: "declaration",
		},
		{
			name: "nil declaration", guard: guard,
			inner:   &nilDeclarationCallable{},
			binding: BindWorkspaceExec("workspace_exec"), want: "declaration",
		},
		{
			name: "binding mismatch", guard: guard,
			inner: newFakeCallable("ok"), binding: BindWorkspaceExec("other"),
			want: "binding name must match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped, err := WrapExecution(test.guard, test.inner, test.binding)
			require.Nil(t, wrapped)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestWrapExecutionAuditFailureBlocksCall(t *testing.T) {
	auditor := &memoryAuditor{err: errors.New("audit unavailable")}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	inner := newFakeCallable("ok")
	wrapper := wrapCallable(t, guard, inner)

	result, err := wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.Nil(t, result)
	require.ErrorContains(t, err, "record audit event")
	require.Zero(t, inner.calls)
}

func TestWrapExecutionHandlesToolErrorsAndUninspectableOutput(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)

	plainErr := errors.New("tool failed")
	plain := newFakeCallable(nil)
	plain.err = plainErr
	wrapper := wrapCallable(t, guard, plain)
	result, err := wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.Nil(t, result)
	require.ErrorIs(t, err, plainErr)

	secret := newFakeCallable(nil)
	secret.err = errors.New("api_key=top-secret-value")
	wrapper = wrapCallable(t, guard, secret)
	result, err = wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.Nil(t, result)
	require.NotContains(t, err.Error(), "top-secret-value")
	var executionErr *ExecutionError
	require.ErrorAs(t, err, &executionErr)
	require.Equal(t, "SECRET_IN_TOOL_OUTPUT", executionErr.RuleID)

	uninspectable := newFakeCallable(make(chan struct{}))
	wrapper = wrapCallable(t, guard, uninspectable)
	result, err = wrapper.Call(context.Background(), []byte(safeWorkspaceArguments))
	require.Nil(t, result)
	require.ErrorAs(t, err, &executionErr)
	require.Equal(t, "TOOL_OUTPUT_UNINSPECTABLE", executionErr.RuleID)
}
