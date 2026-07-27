// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety_test

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety/checkers"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestPermissionPolicyInterceptsDangerousCommand verifies acceptance criteria 7:
// the PermissionPolicy wrapper rejects dangerous commands before execution
// and records an audit event.
func TestPermissionPolicyInterceptsDangerousCommand(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	scanner := toolsafety.NewScanner(policy)
	scanner.Add(checkers.NewDangerousCmdChecker(policy))
	scanner.Add(checkers.NewNetworkEgressChecker(policy))
	scanner.Add(checkers.NewShellBypassChecker())
	scanner.Add(checkers.NewResourceAbuseChecker(policy))
	scanner.Add(checkers.NewSensitiveLeakChecker(policy))
	scanner.Add(checkers.NewHostExecRiskChecker())

	auditCalled := false
	var auditReport *toolsafety.ScanReport

	guard := toolsafety.NewSafetyGuardPermissionPolicy(scanner)
	guard.WithAuditLog(func(r *toolsafety.ScanReport) {
		auditCalled = true
		auditReport = r
	})

	args, _ := json.Marshal(map[string]string{"command": "rm -rf /"})

	req := &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		ToolCallID: "call_001",
		Arguments:  args,
	}

	decision, err := guard.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission error: %v", err)
	}

	if decision.Action != tool.PermissionActionDeny {
		t.Errorf("expected deny decision, got %q", decision.Action)
	}
	if decision.Reason == "" {
		t.Error("expected non-empty reason for deny")
	}

	if !auditCalled {
		t.Error("audit callback was not called")
	}
	if auditReport == nil {
		t.Fatal("audit report is nil")
	}
	if auditReport.Decision != toolsafety.DecisionDeny {
		t.Errorf("audit report decision: got %q, want %q", auditReport.Decision, toolsafety.DecisionDeny)
	}
	if auditReport.Intercepted != true {
		t.Error("audit report should mark as intercepted")
	}
	if len(auditReport.Findings) == 0 {
		t.Error("audit report should have findings")
	}
}

// TestPermissionPolicyAllowsSafeCommand verifies that safe commands pass through.
func TestPermissionPolicyAllowsSafeCommand(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	scanner := toolsafety.NewScanner(policy)
	scanner.Add(checkers.NewDangerousCmdChecker(policy))

	auditCalled := false
	guard := toolsafety.NewSafetyGuardPermissionPolicy(scanner)
	guard.WithAuditLog(func(r *toolsafety.ScanReport) {
		auditCalled = true
	})

	args, _ := json.Marshal(map[string]string{"command": "echo hello"})

	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	}

	decision, err := guard.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission error: %v", err)
	}

	if decision.Action != tool.PermissionActionAllow {
		t.Errorf("expected allow decision for safe command, got %q", decision.Action)
	}
	if !auditCalled {
		t.Error("audit callback should still be called for allowed commands")
	}
}

// TestPermissionPolicySkipsUnknownTool verifies that non-execution tools pass through.
func TestPermissionPolicySkipsUnknownTool(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	scanner := toolsafety.NewScanner(policy)

	guard := toolsafety.NewSafetyGuardPermissionPolicy(scanner)

	req := &tool.PermissionRequest{
		ToolName: "file_search",
	}

	decision, err := guard.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission error: %v", err)
	}

	if decision.Action != tool.PermissionActionAllow {
		t.Errorf("expected allow for unknown tool, got %q", decision.Action)
	}
}
