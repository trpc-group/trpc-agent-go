// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

func TestHostExecRiskChecker_InteractiveCommand(t *testing.T) {
	c := NewHostExecRiskChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "exec_command",
		Command:  "top",
		Backend:  "hostexec",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasPTY := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleHostExecPTY {
			hasPTY = true
			break
		}
	}
	if !hasPTY {
		t.Errorf("expected HOSTEXEC_PTY_SESSION for interactive command 'top', got %+v", findings)
	}
}

func TestHostExecRiskChecker_PrivilegeEscalation(t *testing.T) {
	c := NewHostExecRiskChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "exec_command",
		Command:  "sudo apt install nginx",
		Backend:  "hostexec",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasPrivilege := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RulePrivilegeEscalation {
			hasPrivilege = true
			break
		}
	}
	if !hasPrivilege {
		t.Errorf("expected PRIVILEGE_ESCALATION for sudo, got %+v", findings)
	}
}

func TestHostExecRiskChecker_BackgroundProcess(t *testing.T) {
	c := NewHostExecRiskChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "exec_command",
		Command:  "sleep 100 &",
		Backend:  "hostexec",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasBackground := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleBackgroundProcess {
			hasBackground = true
			break
		}
	}
	if !hasBackground {
		t.Errorf("expected BACKGROUND_PROCESS for background command, got %+v", findings)
	}
}

func TestHostExecRiskChecker_SkipsNonHostExec(t *testing.T) {
	c := NewHostExecRiskChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "workspace_exec",
		Command:  "top",
		Backend:  "workspaceexec",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for non-hostexec backend, got %+v", findings)
	}
}

func TestHostExecRiskChecker_SafeCommandOnHost(t *testing.T) {
	c := NewHostExecRiskChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "exec_command",
		Command:  "echo hello",
		Backend:  "hostexec",
		TimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A bounded safe command should not trigger hostexec risks beyond the
	// no-timeout warning (which doesn't apply here since we set TimeoutS=30).
	for _, f := range findings {
		if f.RuleID == toolsafety.RulePrivilegeEscalation ||
			f.RuleID == toolsafety.RuleBackgroundProcess ||
			f.RuleID == toolsafety.RuleHostExecPTY {
			t.Errorf("unexpected finding for safe command: %+v", f)
		}
	}
}
