//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// These tests guard the contract that non-execution tools (file, search,
// MCP, and ordinary tools that neither declare a backend nor match an
// exec-like name) are not intercepted by the command/code safety scanner.
//
// Before the fix, inferBackend's name heuristic defaulted every unknown
// tool to BackendWorkspaceExec, and extractScanRequest then demanded a
// "command" argument.  Any non-exec tool whose valid arguments lack a
// "command" field therefore failed extraction and CheckToolPermission
// returned Ask — blocking tools the safety guard has no business
// scanning.  These tests drive the public CheckToolPermission seam and
// assert that such tools get Allow, while genuine exec tools still reach
// their rule sets.
//
// The seam is the public PermissionPolicy.CheckToolPermission method.
// fakeNoBackendTool (declared in backend_provider_test.go) stands in for
// an ordinary tool that does not implement BackendProvider.

// newNonExecPolicy returns a policy whose DefaultVerdict is Deny.  This
// makes the tests strict: if a non-exec tool were ever scanned (instead
// of short-circuited), the empty/absent command would be denied, so an
// Allow result can only come from the short-circuit — not from the
// scanner happening to allow the input.
func newNonExecPolicy(t *testing.T) *PermissionPolicy {
	t.Helper()
	policy := DefaultPolicy()
	policy.DefaultVerdict = VerdictDeny
	scanner, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	return NewPermissionPolicyFromScanner(scanner, nil)
}

// checkNonExec invokes CheckToolPermission for a tool that does not
// declare a backend, using the given model-visible name and arguments.
func checkNonExec(t *testing.T, pp *PermissionPolicy, name string, args []byte) tool.PermissionDecision {
	t.Helper()
	req := &tool.PermissionRequest{
		Tool:      &fakeNoBackendTool{name: name},
		ToolName:  name,
		Arguments: args,
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	return dec
}

// TestCheckToolPermission_NonExecTool_AllowedWithoutCommand proves that a
// tool whose name carries no exec signal (e.g. "file_read") and that does
// not declare a backend is allowed even though its arguments have no
// "command" field.  Before the fix this returned Ask.
func TestCheckToolPermission_NonExecTool_AllowedWithoutCommand(t *testing.T) {
	pp := newNonExecPolicy(t)
	args := []byte(`{"path":"/tmp/data.txt"}`)

	dec := checkNonExec(t, pp, "file_read", args)

	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("non-exec tool without command: got action %q want %q "+
			"(non-execution tools must not be blocked for lacking a command field)",
			dec.Action, tool.PermissionActionAllow)
	}
}

// TestCheckToolPermission_NonExecTool_AllowedEvenWithDangerousArgs proves
// that a non-exec tool is allowed even when its arguments happen to
// contain a string that the scanner would deny if it were a command
// (e.g. "rm -rf /" as a search query).  The safety guard must not
// interpret arbitrary argument fields as commands to scan.
func TestCheckToolPermission_NonExecTool_AllowedEvenWithDangerousArgs(t *testing.T) {
	pp := newNonExecPolicy(t)
	args := []byte(`{"query":"rm -rf /"}`)

	dec := checkNonExec(t, pp, "search", args)

	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("non-exec tool with dangerous-looking args: got action %q want %q "+
			"(non-execution tools must not be scanned for command risks)",
			dec.Action, tool.PermissionActionAllow)
	}
}

// TestCheckToolPermission_NonExecTool_AllowedWithEmptyArgs proves that a
// non-exec tool with no arguments at all is allowed.  This guards against
// a regression where empty arguments were treated as a missing "command".
func TestCheckToolPermission_NonExecTool_AllowedWithEmptyArgs(t *testing.T) {
	pp := newNonExecPolicy(t)

	dec := checkNonExec(t, pp, "mcp_ping", nil)

	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("non-exec tool with empty args: got action %q want %q",
			dec.Action, tool.PermissionActionAllow)
	}
}

// TestCheckToolPermission_NonExecTool_MCPStyleName proves that a tool name
// resembling an MCP tool (which is not an execution tool) is allowed.
// This is the MCP case called out in the issue.
func TestCheckToolPermission_NonExecTool_MCPStyleName(t *testing.T) {
	pp := newNonExecPolicy(t)
	args := []byte(`{"server":"weather","method":"get_forecast"}`)

	dec := checkNonExec(t, pp, "mcp_weather_get_forecast", args)

	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("mcp-style tool: got action %q want %q",
			dec.Action, tool.PermissionActionAllow)
	}
}

// TestCheckToolPermission_ExecTool_StillScanned proves that the
// short-circuit does not over-apply: a genuine exec tool that declares
// BackendHostExec still reaches the scanner and is denied for a dangerous
// command.  This guards against an over-broad "allow all non-declared"
// implementation.
func TestCheckToolPermission_ExecTool_StillScanned(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultVerdict = VerdictAllow
	scanner, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	pp := NewPermissionPolicyFromScanner(scanner, nil)

	req := &tool.PermissionRequest{
		Tool:      &fakeBackendTool{declared: BackendHostExec, name: "exec_command"},
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("exec tool with dangerous command: got action %q want %q "+
			"(declared exec tools must still be scanned)",
			dec.Action, tool.PermissionActionDeny)
	}
}

// TestCheckToolPermission_ExecTool_WorkspaceExecStillScanned proves that a
// workspace_exec tool that declares its backend is still scanned (the
// other half of the over-broad-short-circuit guard).
func TestCheckToolPermission_ExecTool_WorkspaceExecStillScanned(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultVerdict = VerdictAllow
	scanner, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	pp := NewPermissionPolicyFromScanner(scanner, nil)

	req := &tool.PermissionRequest{
		Tool:      &fakeBackendTool{declared: BackendWorkspaceExec, name: "workspace_exec"},
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("workspace_exec with dangerous command: got action %q want %q",
			dec.Action, tool.PermissionActionDeny)
	}
}

// TestCheckToolPermission_ExecTool_CodeExecStillScanned proves that an
// execute_code tool that declares BackendCodeExec is still scanned.
func TestCheckToolPermission_ExecTool_CodeExecStillScanned(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultVerdict = VerdictAllow
	scanner, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	pp := NewPermissionPolicyFromScanner(scanner, nil)

	req := &tool.PermissionRequest{
		Tool:      &fakeBackendTool{declared: BackendCodeExec, name: "execute_code"},
		ToolName:  "execute_code",
		Arguments: []byte(`{"code_blocks":[{"language":"python","code":"import os; os.system('rm -rf /')"}]}`),
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("execute_code with dangerous block: got action %q want %q",
			dec.Action, tool.PermissionActionDeny)
	}
}
