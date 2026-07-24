//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type wrapperTestTool struct {
	name            string
	called          bool
	result          any
	err             error
	permissionErr   error
	permissionPanic any
	panicValue      any
	gotToolCallID   string
	stateDelta      map[string][]byte
	started         chan<- struct{}
	block           <-chan struct{}
	decision        tool.PermissionDecision
	metadata        tool.ToolMetadata
	long            bool
	skip            bool
	stream          bool
	pollutes        bool
	structured      bool
}

func (t *wrapperTestTool) Declaration() *tool.Declaration {
	name := t.name
	if name == "" {
		name = "workspace_exec"
	}
	return &tool.Declaration{Name: name}
}

func (t *wrapperTestTool) Call(
	context.Context,
	[]byte,
) (any, error) {
	t.called = true
	if t.started != nil {
		t.started <- struct{}{}
	}
	if t.block != nil {
		<-t.block
	}
	if t.panicValue != nil {
		panic(t.panicValue)
	}
	return t.result, t.err
}

func (t *wrapperTestTool) CheckPermission(
	_ context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	t.gotToolCallID = req.ToolCallID
	if t.permissionPanic != nil {
		panic(t.permissionPanic)
	}
	if t.permissionErr != nil {
		return tool.PermissionDecision{}, t.permissionErr
	}
	return t.decision, nil
}

func (t *wrapperTestTool) ToolMetadata() tool.ToolMetadata {
	return t.metadata
}

func (t *wrapperTestTool) LongRunning() bool {
	return t.long
}

func (t *wrapperTestTool) SkipSummarization() bool {
	return t.skip
}

func (t *wrapperTestTool) StreamInner() bool {
	return t.stream
}

func (*wrapperTestTool) InnerTextMode() tool.InnerTextMode {
	return tool.InnerTextModeExclude
}

func (t *wrapperTestTool) PollutesAutoMemory() bool {
	return t.pollutes
}

func (t *wrapperTestTool) TRPCAgentGoStructuredStreamErrorsOptIn() bool {
	return t.structured
}

func (t *wrapperTestTool) StateDelta(
	string,
	[]byte,
	[]byte,
) map[string][]byte {
	return t.stateDelta
}

func newWrapperTestGuard(
	t *testing.T,
	opts ...Option,
) (*Guard, *bytes.Buffer) {
	t.Helper()
	policy := testPolicy(t)
	policy.Audit.Path = ""
	policy.Audit.Required = false
	audit := new(bytes.Buffer)
	allOpts := []Option{
		WithPolicy(policy),
		WithAuditWriter(audit),
	}
	guard, err := NewGuard(append(allOpts, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, guard.Close()) })
	return guard, audit
}

func TestWrapTool_DeniesBeforeExecution(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	inner := &wrapperTestTool{}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)

	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"rm -rf /","timeout":10}`),
	)
	require.NoError(t, err)
	require.False(t, inner.called)
	permission, ok := result.(tool.PermissionResult)
	require.True(t, ok)
	require.Equal(t, tool.PermissionResultStatusDenied,
		permission.Status)
}

func TestWrapTool_ReturnsAskWithoutExecution(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	inner := &wrapperTestTool{}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)

	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"npm install package","timeout":10}`),
	)
	require.NoError(t, err)
	require.False(t, inner.called)
	permission, ok := result.(tool.PermissionResult)
	require.True(t, ok)
	require.Equal(t, tool.PermissionResultStatusApprovalRequired,
		permission.Status)
}

func TestWrapTool_CodeExecutorRequiresTimeoutProfile(t *testing.T) {
	policy := testPolicy(t)
	policy.Audit.Path = ""
	policy.Audit.Required = false
	defaultGuard, err := NewGuard(WithPolicy(policy))
	require.NoError(t, err)
	defer defaultGuard.Close()
	inner := &wrapperTestTool{
		name:   "execute_code",
		result: "ok",
	}
	wrapped, err := WrapTool(inner, defaultGuard)
	require.NoError(t, err)
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"code_blocks":[{"language":"python","code":"print(1)"}]}`),
	)
	require.NoError(t, err)
	permission := result.(tool.PermissionResult)
	require.Equal(t, tool.PermissionResultStatusDenied,
		permission.Status)
	require.Contains(t, permission.Reason,
		"resource.timeout_unknown")

	customGuard, err := NewGuard(
		WithPolicy(policy),
		WithToolProfile(ToolProfile{
			Name:                "execute_code",
			Backend:             BackendCodeExec,
			DefaultTimeout:      time.Second,
			Isolated:            true,
			EnvironmentIsolated: true,
			CodeBlocksField:     "code_blocks",
		}),
	)
	require.NoError(t, err)
	defer customGuard.Close()
	wrapped, err = WrapTool(inner, customGuard)
	require.NoError(t, err)
	result, err = wrapped.Call(
		context.Background(),
		[]byte(`{"code_blocks":[{"language":"python","code":"print(1)"}]}`),
	)
	require.NoError(t, err)
	require.Equal(t, "ok", result)
}

func TestWrapTool_RedactsAndCorrelatesAudit(t *testing.T) {
	guard, audit := newWrapperTestGuard(t)
	inner := &wrapperTestTool{
		result: map[string]any{
			"output": "API_KEY=sk_live_1234567890abcdef1234",
		},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)

	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	require.True(t, inner.called)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw),
		"sk_live_1234567890abcdef1234")

	lines := strings.Split(strings.TrimSpace(audit.String()), "\n")
	require.Len(t, lines, 2)
	var preflight, post AuditEvent
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &preflight))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &post))
	require.Equal(t, AuditPhasePreflight, preflight.Phase)
	require.Equal(t, AuditPhasePostExecute, post.Phase)
	require.Equal(t, preflight.ScanID, post.ScanID)
}

func TestWrapTool_RedactsSecretError(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	inner := &wrapperTestTool{
		err: errors.New(
			"API_KEY=sk_live_1234567890abcdef1234",
		),
	}

	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)

	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.Error(t, err)
	require.Nil(t, errors.Unwrap(err))
	require.NotContains(t, err.Error(),
		"sk_live_1234567890abcdef1234")
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw),
		"sk_live_1234567890abcdef1234")
	require.Contains(t, string(raw), "error_redacted")
}

type wrapperCallbackContent struct {
	Text string `json:"text"`
}

type wrapperCallbackResult struct {
	Output   string                   `json:"output"`
	Password string                   `json:"password,omitempty"`
	Callback []wrapperCallbackContent `json:"-"`
}

func (r *wrapperCallbackResult) GetCallbackResult() any {
	return r.Callback
}

func TestWrapTool_PreservesCallbackProjectionType(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	secret := "sk_live_1234567890abcdef1234"
	inner := &wrapperTestTool{
		result: &wrapperCallbackResult{
			Output: "ok",
			Callback: []wrapperCallbackContent{{
				Text: secret,
			}},
		},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	callbackResult := result.(interface {
		GetCallbackResult() any
	}).GetCallbackResult()
	content, ok := callbackResult.([]wrapperCallbackContent)
	require.True(t, ok, "callback result type: %T", callbackResult)
	require.Len(t, content, 1)
	require.NotContains(t, content[0].Text, secret)
}

func TestWrapTool_TruncatedCallbackProjectionKeepsType(t *testing.T) {
	policy := testPolicy(t)
	policy.Audit.Path = ""
	policy.Audit.Required = false
	policy.MaxOutputSize = 16
	guard, err := NewGuard(WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()
	inner := &wrapperTestTool{
		result: &wrapperCallbackResult{
			Output: "ok",
			Callback: []wrapperCallbackContent{{
				Text: strings.Repeat("y", 100),
			}},
		},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	callbackResult := result.(interface {
		GetCallbackResult() any
	}).GetCallbackResult()
	_, ok := callbackResult.([]wrapperCallbackContent)
	require.True(t, ok, "callback result type: %T", callbackResult)
}

func TestWrapTool_TruncationIsNotRedaction(t *testing.T) {
	policy := testPolicy(t)
	policy.Audit.Path = ""
	policy.Audit.Required = false
	policy.MaxOutputSize = 32
	audit := new(bytes.Buffer)
	guard, err := NewGuard(
		WithPolicy(policy),
		WithAuditWriter(audit),
		WithRedaction(false),
	)
	require.NoError(t, err)
	defer guard.Close()
	inner := &wrapperTestTool{
		result: strings.Repeat("x", 100),
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(audit.String()), "\n")
	require.Len(t, lines, 2)
	var post AuditEvent
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &post))
	require.True(t, post.Truncated)
	require.False(t, post.Redacted)
}

type wrapperRetryResult struct {
	Password string `json:"password"`
}

func (*wrapperRetryResult) RetryResultError() bool {
	return true
}

func TestWrapTool_PreservesRetryResultSemantics(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	inner := &wrapperTestTool{
		result: &wrapperRetryResult{Password: "hunter2xyz"},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	retry, ok := result.(interface{ RetryResultError() bool })
	require.True(t, ok)
	require.True(t, retry.RetryResultError())
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "hunter2xyz")
}

func TestWrapTool_BoundsOrdinaryErrorAndPreservesCause(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	cause := errors.New(strings.Repeat("ordinary failure ", 200))
	inner := &wrapperTestTool{err: cause}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.Error(t, err)
	require.ErrorIs(t, err, cause)
	require.LessOrEqual(t, len(err.Error()),
		len("wrapped tool call failed: ")+permissionReasonMaxLen)
}

func TestWrapTool_ReleasesConcurrency(t *testing.T) {
	guard, _ := newWrapperTestGuard(t,
		WithConcurrencyPolicy(
			ConcurrencyPolicy{MaxActiveCalls: 1},
		),
	)
	inner := &wrapperTestTool{result: "ok"}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		result, err := wrapped.Call(
			context.Background(),
			[]byte(`{"command":"ls","timeout":10}`),
		)
		require.NoError(t, err)
		require.Equal(t, "ok", result)
	}
	require.Equal(t, int64(0), guard.concurrency.activeCount())
}

func TestWrapTool_PreservesToolCapabilities(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	metadata := tool.ToolMetadata{
		ReadOnly:        true,
		ConcurrencySafe: true,
	}
	inner := &wrapperTestTool{
		metadata:   metadata,
		long:       true,
		skip:       true,
		stream:     false,
		pollutes:   true,
		structured: true,
		stateDelta: map[string][]byte{
			"key": []byte("value"),
		},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	require.Equal(t, metadata, tool.MetadataOf(wrapped))
	require.Equal(t, inner,
		wrapped.(interface{ Original() tool.Tool }).Original())
	require.True(t,
		wrapped.(interface{ LongRunning() bool }).LongRunning())
	require.True(t,
		wrapped.(interface{ SkipSummarization() bool }).
			SkipSummarization())
	require.False(t,
		wrapped.(interface{ StreamInner() bool }).StreamInner())
	require.Equal(t, tool.InnerTextModeExclude,
		wrapped.(interface {
			InnerTextMode() tool.InnerTextMode
		}).InnerTextMode())
	require.True(t,
		wrapped.(interface{ PollutesAutoMemory() bool }).
			PollutesAutoMemory())
	require.True(t,
		wrapped.(interface {
			TRPCAgentGoStructuredStreamErrorsOptIn() bool
		}).TRPCAgentGoStructuredStreamErrorsOptIn())
	require.Equal(t, inner.stateDelta,
		wrapped.(interface {
			StateDelta(string, []byte, []byte) map[string][]byte
		}).StateDelta("id", nil, nil))
	require.Equal(t, inner.stateDelta,
		wrapped.(interface {
			StateDeltaForInvocation(
				*agent.Invocation,
				string,
				[]byte,
				[]byte,
			) map[string][]byte
		}).StateDeltaForInvocation(nil, "id", nil, nil))
}

func TestWrapTool_InnerPermissionCheckerRuns(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	inner := &wrapperTestTool{
		decision: tool.DenyPermission("inner deny"),
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	require.False(t, inner.called)
	permission := result.(tool.PermissionResult)
	require.Equal(t, "inner deny", permission.Reason)
}

func TestWrapTool_UsesFrameworkToolCallID(t *testing.T) {
	guard, audit := newWrapperTestGuard(t)
	inner := &wrapperTestTool{result: "ok"}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	ctx := context.WithValue(
		context.Background(),
		tool.ContextKeyToolCallID{},
		"framework-call-id",
	)
	_, err = wrapped.Call(
		ctx,
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	require.Equal(t, "framework-call-id", inner.gotToolCallID)

	lines := strings.Split(strings.TrimSpace(audit.String()), "\n")
	require.Len(t, lines, 2)
	var preflight AuditEvent
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &preflight))
	require.NotEmpty(t, preflight.ScanID)
	require.Empty(t, guard.scanEvents)
	require.Empty(t, guard.activeCalls)
}

func TestWrapTool_SanitizesPermissionCheckerOutput(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	inner := &wrapperTestTool{
		permissionErr: errors.New(
			"API_KEY=sk_live_1234567890abcdef1234",
		),
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.Error(t, err)
	require.NotContains(t, err.Error(),
		"sk_live_1234567890abcdef1234")

	inner.permissionErr = nil
	inner.decision = tool.DenyPermission(
		"password: hunter2xyz",
	)
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	permission := result.(tool.PermissionResult)
	require.NotContains(t, permission.Reason, "hunter2xyz")
	require.LessOrEqual(t, len(permission.Reason),
		permissionReasonMaxLen)
}

func TestWrapTool_PanicReleasesLifecycleState(t *testing.T) {
	guard, _ := newWrapperTestGuard(t,
		WithConcurrencyPolicy(
			ConcurrencyPolicy{MaxActiveCalls: 1},
		),
	)
	inner := &wrapperTestTool{panicValue: "secret panic"}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.ErrorContains(t, err, "wrapped tool panicked")
	require.NotContains(t, err.Error(), "secret panic")
	require.Equal(t, int64(0), guard.concurrency.activeCount())
	require.Empty(t, guard.scanEvents)
	require.Empty(t, guard.activeCalls)

	inner.panicValue = nil
	inner.result = "ok"
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	require.Equal(t, "ok", result)
}

func TestWrapTool_UnownedPanicDoesNotReleaseDuplicateCall(t *testing.T) {
	guard, _ := newWrapperTestGuard(t,
		WithConcurrencyPolicy(
			ConcurrencyPolicy{MaxActiveCalls: 1},
		),
	)
	started := make(chan struct{}, 1)
	unblock := make(chan struct{})
	firstInner := &wrapperTestTool{
		result:  "ok",
		started: started,
		block:   unblock,
	}
	first, err := WrapTool(firstInner, guard)
	require.NoError(t, err)
	ctx := context.WithValue(
		context.Background(),
		tool.ContextKeyToolCallID{},
		"shared-id",
	)
	firstDone := make(chan error, 1)
	go func() {
		_, callErr := first.Call(
			ctx,
			[]byte(`{"command":"ls","timeout":10}`),
		)
		firstDone <- callErr
	}()
	<-started
	require.Equal(t, int64(1), guard.concurrency.activeCount())

	secondInner := &wrapperTestTool{
		permissionPanic: "checker panic",
	}
	second, err := WrapTool(secondInner, guard)
	require.NoError(t, err)
	_, err = second.Call(
		ctx,
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.ErrorContains(t, err, "wrapped tool panicked")
	require.Equal(t, int64(1), guard.concurrency.activeCount())
	require.Contains(t, guard.activeCalls, "shared-id")

	close(unblock)
	require.NoError(t, <-firstDone)
	require.Equal(t, int64(0), guard.concurrency.activeCount())
	require.Empty(t, guard.activeCalls)
}

type wrapperPanicWriter struct{}

func (wrapperPanicWriter) Write([]byte) (int, error) {
	panic("writer panic")
}

func TestWrapTool_PreflightPanicReleasesLifecycleState(t *testing.T) {
	policy := testPolicy(t)
	policy.Audit.Path = ""
	policy.Audit.Required = false
	guard, err := NewGuard(
		WithPolicy(policy),
		WithAuditWriter(wrapperPanicWriter{}),
		WithConcurrencyPolicy(
			ConcurrencyPolicy{MaxActiveCalls: 1},
		),
	)
	require.NoError(t, err)
	defer guard.Close()

	inner := &wrapperTestTool{result: "ok"}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.Error(t, err)
	require.False(t, inner.called)
	require.Equal(t, int64(0), guard.concurrency.activeCount())
	require.Empty(t, guard.scanEvents)
	require.Empty(t, guard.activeCalls)
}

func TestGuard_CloseWaitsForWrappedCallsAndRejectsReuse(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	started := make(chan struct{}, 1)
	unblock := make(chan struct{})
	inner := &wrapperTestTool{
		result:  "ok",
		started: started,
		block:   unblock,
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	callDone := make(chan error, 1)
	go func() {
		_, callErr := wrapped.Call(
			context.Background(),
			[]byte(`{"command":"ls","timeout":10}`),
		)
		callDone <- callErr
	}()
	<-started

	closeDone := make(chan error, 1)
	go func() { closeDone <- guard.Close() }()
	for {
		guard.mu.Lock()
		closing := guard.closing
		guard.mu.Unlock()
		if closing {
			break
		}
		runtime.Gosched()
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before the call finished: %v", err)
	default:
	}
	close(unblock)
	require.NoError(t, <-callDone)
	require.NoError(t, <-closeDone)

	inner.called = false
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.ErrorContains(t, err, "safety guard is closed")
	require.False(t, inner.called)
}

func TestWrapTool_TracksTypedSessionResult(t *testing.T) {
	guard, audit := newWrapperTestGuard(t)
	type execResult struct {
		SessionID string `json:"session_id"`
	}

	inner := &wrapperTestTool{
		result: execResult{SessionID: "session-1"},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	require.True(t, guard.sessions.isKnown("session-1"))

	lines := strings.Split(strings.TrimSpace(audit.String()), "\n")
	require.Len(t, lines, 2)
	var post AuditEvent
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &post))
	require.NotEmpty(t, post.SessionHash)

	writeTool := &wrapperTestTool{
		name: "workspace_write_stdin",
		result: struct {
			Status string `json:"status"`
		}{Status: "exited"},
	}
	wrappedWrite, err := WrapTool(writeTool, guard)
	require.NoError(t, err)
	_, err = wrappedWrite.Call(
		context.Background(),
		[]byte(`{"session_id":"session-1","chars":""}`),
	)
	require.NoError(t, err)
	require.False(t, guard.sessions.isKnown("session-1"))
	require.True(t, guard.sessions.isKilled("session-1"))
}

type wrapperMetaResult struct {
	Value string `json:"value"`
	meta  map[string]any
}

func (r *wrapperMetaResult) GetMeta() map[string]any {
	return r.meta
}

func TestWrapTool_RedactsMetadataInPlace(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	meta := map[string]any{
		"token": "xoxb-1234567890-abcdef",
	}
	inner := &wrapperTestTool{
		result: &wrapperMetaResult{Value: "ok", meta: meta},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	raw, err := json.Marshal(meta)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "xoxb-1234567890-abcdef")

	cyclic := map[string]any{
		"token": "xoxb-1234567890-abcdef",
	}
	cyclic["self"] = cyclic
	inner.result = &wrapperMetaResult{Value: "ok", meta: cyclic}
	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	raw, err = json.Marshal(cyclic)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "xoxb-1234567890-abcdef")
	require.Equal(t, "redacted", cyclic["status"])
}

type wrapperCopyMetaResult struct {
	meta map[string]any
}

func (r *wrapperCopyMetaResult) GetMeta() map[string]any {
	out := make(map[string]any, len(r.meta))
	for key, value := range r.meta {
		out[key] = value
	}
	return out
}

func TestWrapTool_UsesSanitizedDefensiveMetadataCopy(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	inner := &wrapperTestTool{
		result: &wrapperCopyMetaResult{
			meta: map[string]any{
				"token": "xoxb-1234567890-abcdef",
			},
		},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	meta := result.(interface {
		GetMeta() map[string]any
	}).GetMeta()
	raw, err := json.Marshal(meta)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "xoxb-1234567890-abcdef")
}

func TestWrapTool_DisabledRedactionPreservesCallbackPayload(t *testing.T) {
	policy := testPolicy(t)
	policy.Audit.Path = ""
	policy.Audit.Required = false
	guard, err := NewGuard(
		WithPolicy(policy),
		WithRedaction(false),
	)
	require.NoError(t, err)
	defer guard.Close()
	secret := "sk_live_1234567890abcdef1234"
	inner := &wrapperTestTool{
		result: &wrapperCallbackResult{
			Output: "ok",
			Callback: []wrapperCallbackContent{{
				Text: secret,
			}},
		},
	}
	wrapped, err := WrapTool(inner, guard)
	require.NoError(t, err)
	result, err := wrapped.Call(
		context.Background(),
		[]byte(`{"command":"ls","timeout":10}`),
	)
	require.NoError(t, err)
	callback := result.(interface {
		GetCallbackResult() any
	}).GetCallbackResult().([]wrapperCallbackContent)
	require.Equal(t, secret, callback[0].Text)
}

func TestWrapTool_RejectsInvalidInputs(t *testing.T) {
	guard, _ := newWrapperTestGuard(t)
	_, err := WrapTool(nil, guard)
	require.ErrorContains(t, err, "tool is nil")
	_, err = WrapTool(&wrapperTestTool{}, nil)
	require.ErrorContains(t, err, "safety guard is nil")
}
