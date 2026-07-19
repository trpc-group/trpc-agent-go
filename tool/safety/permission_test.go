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

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

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

func TestNewPermissionPolicyValidatesRemoteProviderSlugs(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy(), WithAuditor(&memoryAuditor{}))
	require.NoError(t, err)
	for _, provider := range []Provider{"vendor", "vendor-1.cloud", "vendor_name"} {
		_, policyErr := NewPermissionPolicy(
			guard, BindRemoteCodeExec("execute_remote", provider),
		)
		require.NoError(t, policyErr, provider)
	}
	for _, provider := range []Provider{
		"", "Vendor", "-vendor", "vendor-", "vendor..cloud",
		Provider(strings.Repeat("x", 65)),
	} {
		_, policyErr := NewPermissionPolicy(
			guard, BindRemoteCodeExec("execute_remote", provider),
		)
		require.Error(t, policyErr, provider)
	}
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
				BackendCustom,
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
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "CMD_DANGEROUS_DELETE")
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
		BindCustom("custom.exec", BackendCustom, &testAdapter{err: errors.New("invalid")}),
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
