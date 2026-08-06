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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type unsafeAuditor struct {
	events []AuditEvent
}

func (auditor *unsafeAuditor) Record(
	_ context.Context,
	event AuditEvent,
) error {
	auditor.events = append(auditor.events, event)
	return nil
}

func TestNewPermissionPolicyRequiresAuditedGuard(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	_, err = NewPermissionPolicy(guard, BindWorkspaceExec("workspace_exec"))
	require.ErrorContains(t, err, "requires an auditor")
}

func TestNewPermissionPolicyRejectsInvalidBindingSets(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(&memoryAuditor{}))
	require.NoError(t, err)

	_, err = NewPermissionPolicy(guard)
	require.ErrorContains(t, err, "requires bindings")

	binding := BindWorkspaceExec("workspace_exec")
	_, err = NewPermissionPolicy(guard, binding, binding)
	require.ErrorContains(t, err, "duplicate binding tool name")
}

func TestPermissionPolicyNilRequestFailsClosed(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(&memoryAuditor{}))
	require.NoError(t, err)
	policy, err := NewPermissionPolicy(guard, BindWorkspaceExec("workspace_exec"))
	require.NoError(t, err)

	decision, err := policy.CheckToolPermission(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestPermissionPolicySerializesAdapterCallsPerBinding(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	adapter := &concurrencyProbeAdapter{}
	binding := BindCustom("custom.exec", adapter)
	policy, err := NewPermissionPolicy(guard, binding)
	require.NoError(t, err)

	const calls = 16
	start := make(chan struct{})
	errs := make(chan error, calls)
	var wg sync.WaitGroup
	wg.Add(calls)
	for i := 0; i < calls; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, checkErr := policy.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{ToolName: binding.ToolName},
			)
			errs <- checkErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), adapter.maxActive.Load())
}

func TestPermissionPolicyAdapterGateHonorsCancellation(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	adapter := &blockingAdapter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	binding := BindCustom("custom.exec", adapter)
	policy, err := NewPermissionPolicy(guard, binding)
	require.NoError(t, err)

	firstDone := make(chan error, 1)
	go func() {
		_, checkErr := policy.CheckToolPermission(
			context.Background(),
			&tool.PermissionRequest{ToolName: binding.ToolName},
		)
		firstDone <- checkErr
	}()
	<-adapter.entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, checkErr := policy.CheckToolPermission(
		ctx,
		&tool.PermissionRequest{ToolName: binding.ToolName},
	)
	require.ErrorIs(t, checkErr, context.Canceled)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)

	close(adapter.release)
	require.NoError(t, <-firstDone)
}

func TestPermissionPolicyPreservesAdapterCancellation(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(&memoryAuditor{}))
	require.NoError(t, err)

	for _, wantErr := range []error{
		context.Canceled,
		context.DeadlineExceeded,
	} {
		t.Run(wantErr.Error(), func(t *testing.T) {
			binding := BindCustom(
				"custom.exec",
				&testAdapter{err: wantErr},
			)
			policy, policyErr := NewPermissionPolicy(guard, binding)
			require.NoError(t, policyErr)
			decision, scanErr := policy.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{ToolName: binding.ToolName},
			)
			require.ErrorIs(t, scanErr, wantErr)
			require.Equal(t, tool.PermissionActionDeny, decision.Action)
		})
	}
}

func TestAdaptSafelyOverwritesUntrustedAdapterIdentity(t *testing.T) {
	adapter := &testAdapter{input: ScanInput{
		ToolName: "spoofed",
		Kind:     ExecutionKindHostExec, Operation: OperationExecute,
		Command: "go test ./...", Backend: BackendHostExec,
		Args: nil,
		Env:  map[string]string{"SAFE": "value"},
	}}
	binding := BindCustom("custom.exec", adapter)
	req := AdaptRequest{
		ToolName: "custom.exec",
		Metadata: tool.ToolMetadata{OpenWorld: true},
	}

	input, err := adaptSafely(context.Background(), req, binding)
	require.NoError(t, err)
	require.Equal(t, req.ToolName, input.ToolName)
	require.Equal(t, binding.Kind, input.Kind)
	require.Equal(t, binding.Backend, input.Backend)
	require.Equal(t, req.Metadata, input.Metadata)
	adapter.input.Env["SAFE"] = "mutated"
	require.Equal(t, "value", input.Env["SAFE"])
}

func TestPermissionPolicyRejectsInvalidOrPanickingAdapterOutput(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	tests := []struct {
		name    string
		adapter InputAdapter
	}{
		{
			name: "invalid shape",
			adapter: &testAdapter{input: ScanInput{
				Operation: Operation("unknown"), Command: "go test ./...",
			}},
		},
		{
			name: "poll hides executable payload",
			adapter: &testAdapter{input: ScanInput{
				Operation: OperationSessionPoll,
				SessionID: "session-1",
				Command:   "rm -rf /",
				Env:       map[string]string{"TOKEN": "secret"},
			}},
		},
		{name: "panic", adapter: panicInputAdapter{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := BindCustom("custom.exec", test.adapter)
			policy, err := NewPermissionPolicy(guard, binding)
			require.NoError(t, err)
			decision, checkErr := policy.CheckToolPermission(
				context.Background(), &tool.PermissionRequest{ToolName: binding.ToolName},
			)
			require.NoError(t, checkErr)
			require.Equal(t, tool.PermissionActionAsk, decision.Action)
			require.Contains(t, decision.Reason, "TOOL_INPUT_UNPARSABLE")
		})
	}
	require.Len(t, auditor.events, len(tests))
}

func TestGuardScanRejectsInvalidPublicInputShape(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	report, err := guard.Scan(context.Background(), ScanInput{
		ToolName: "custom.exec", Kind: ExecutionKindCustom,
		Operation: OperationExecute, Command: "go test ./...",
		Args: []string{"go", "test"}, Backend: BackendCustom,
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	requireFinding(t, report, "SAFETY_INPUT_INVALID")
}

func TestPermissionPolicyMapsSafetyDecisions(t *testing.T) {
	auditor := &memoryAuditor{}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	policy, err := NewPermissionPolicy(
		guard,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)

	tests := []struct {
		name   string
		args   string
		action tool.PermissionAction
	}{
		{
			name:   "allow",
			args:   `{"command":"go test ./...","timeout_sec":30}`,
			action: tool.PermissionActionAllow,
		},
		{
			name:   "deny",
			args:   `{"command":"rm -rf /","timeout_sec":30}`,
			action: tool.PermissionActionDeny,
		},
		{
			name:   "ask",
			args:   `{"command":"go env | cat","timeout_sec":30}`,
			action: tool.PermissionActionAsk,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, checkErr := policy.CheckToolPermission(
				context.Background(),
				permissionRequest(test.args),
			)
			require.NoError(t, checkErr)
			require.Equal(t, test.action, decision.Action)
		})
	}
	require.Len(t, auditor.events, len(tests))
}

func TestPermissionPolicyRejectsConcurrencyEnvironment(t *testing.T) {
	policyConfig := DefaultPolicy()
	policyConfig.maxConcurrency = 2
	auditor := &memoryAuditor{}
	guard, err := NewGuard(policyConfig, WithAuditor(auditor))
	require.NoError(t, err)
	policy, err := NewPermissionPolicy(
		guard,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)

	for _, arguments := range []string{
		`{"command":"make","env":{"MAKEFLAGS":"-j1000"},"timeout_sec":30}`,
		`{"command":"make","env":{"MAKEFLAGS":"j1000"},"timeout_sec":30}`,
		`{"command":"go test ./...","env":{"GOFLAGS":"-p=1000"},"timeout_sec":30}`,
	} {
		decision, checkErr := policy.CheckToolPermission(
			context.Background(),
			permissionRequest(arguments),
		)
		require.NoError(t, checkErr)
		require.Equal(t, tool.PermissionActionDeny, decision.Action)
		require.Contains(t, decision.Reason, "RESOURCE_HIGH_CONCURRENCY")
	}
}

func TestPermissionPolicyMalformedInputFailsClosedAndAudits(t *testing.T) {
	auditor := &memoryAuditor{}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	policy, err := NewPermissionPolicy(
		guard,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)

	decision, err := policy.CheckToolPermission(
		context.Background(),
		permissionRequest(`{"command":"rm -rf /"`),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "TOOL_INPUT_UNPARSABLE")
	require.Len(t, auditor.events, 1)
	require.True(t, auditor.events[0].Blocked)
}

func TestPermissionPolicyFailsClosedAcrossMalformedBindingSchemas(t *testing.T) {
	auditor := &memoryAuditor{}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	bindings := []Binding{
		BindWorkspaceSession("workspace_session"),
		BindHostExec("exec_command", ""),
		BindHostSession("write_stdin"),
		BindCodeExec("execute_code", BackendLocal),
		BindCustom("custom.exec", &testAdapter{err: errors.New("invalid")}),
	}
	for _, binding := range bindings {
		policy, policyErr := NewPermissionPolicy(guard, binding)
		require.NoError(t, policyErr)
		decision, checkErr := policy.CheckToolPermission(
			context.Background(),
			&tool.PermissionRequest{
				ToolName: binding.ToolName, Arguments: []byte(`{}`),
			},
		)
		require.NoError(t, checkErr)
		require.Equal(t, tool.PermissionActionAsk, decision.Action)
	}
	require.Len(t, auditor.events, len(bindings))
}

func TestPermissionPolicySerializesAuditorCalls(t *testing.T) {
	const checks = 64
	auditor := &unsafeAuditor{}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	policy, err := NewPermissionPolicy(
		guard,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make(chan error, checks)
	var wait sync.WaitGroup
	for i := 0; i < checks; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, checkErr := policy.CheckToolPermission(
				context.Background(),
				permissionRequest(`{"command":"go test ./...","timeout_sec":30}`),
			)
			errs <- checkErr
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for checkErr := range errs {
		require.NoError(t, checkErr)
	}
	require.Len(t, auditor.events, checks)
}

func TestHostAdapterResolutionFailureNeedsHumanReview(t *testing.T) {
	guard, _ := newWrapperGuard(t, nil)
	binding := BindHostExec("exec_command", ".")
	binding.Adapter = hostExecAdapter{resolveErr: errors.New("resolve failed")}
	policy, err := NewPermissionPolicy(guard, binding)
	require.NoError(t, err)

	decision, err := policy.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName:  binding.ToolName,
			Arguments: []byte(`{"command":"date"}`),
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "TOOL_INPUT_UNPARSABLE")
}

func TestPermissionPolicyAuditFailurePreventsAllow(t *testing.T) {
	auditor := &memoryAuditor{err: errors.New("unavailable")}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	policy, err := NewPermissionPolicy(
		guard,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)

	decision, err := policy.CheckToolPermission(
		context.Background(),
		permissionRequest(`{"command":"go test ./...","timeout_sec":30}`),
	)
	require.ErrorContains(t, err, "record audit event")
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "AUDIT_WRITE_FAILED")
}

func TestPermissionPolicyRecoversAuditorPanic(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(panicAuditor{}))
	require.NoError(t, err)
	policy, err := NewPermissionPolicy(
		guard,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)

	decision, checkErr := policy.CheckToolPermission(
		context.Background(),
		permissionRequest(`{"command":"go test ./...","timeout_sec":30}`),
	)

	require.ErrorContains(t, checkErr, "auditor panicked")
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "AUDIT_WRITE_FAILED")
}

func TestPermissionPolicyLeavesUnboundToolsUnchanged(t *testing.T) {
	auditor := &memoryAuditor{}
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(auditor))
	require.NoError(t, err)
	policy, err := NewPermissionPolicy(
		guard,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)

	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "read_only_tool",
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
	require.Empty(t, auditor.events)
}

func permissionRequest(arguments string) *tool.PermissionRequest {
	return &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(arguments),
	}
}

type concurrencyProbeAdapter struct {
	active    atomic.Int32
	maxActive atomic.Int32
}

type blockingAdapter struct {
	entered chan struct{}
	release chan struct{}
}

func (adapter *blockingAdapter) Adapt(
	context.Context,
	AdaptRequest,
	Binding,
) (ScanInput, error) {
	close(adapter.entered)
	<-adapter.release
	return ScanInput{
		Operation: OperationExecute,
		Command:   "go test ./...",
		Timeout:   DefaultPolicy().maxTimeout,
	}, nil
}

func (adapter *concurrencyProbeAdapter) Adapt(
	context.Context,
	AdaptRequest,
	Binding,
) (ScanInput, error) {
	active := adapter.active.Add(1)
	defer adapter.active.Add(-1)
	for {
		maxActive := adapter.maxActive.Load()
		if active <= maxActive || adapter.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	return ScanInput{
		Operation: OperationExecute,
		Command:   "go test ./...",
		Timeout:   DefaultPolicy().maxTimeout,
	}, nil
}
