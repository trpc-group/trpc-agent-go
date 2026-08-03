//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRedirectionScannerHonorsQuotesAndProtectsSystemPaths(t *testing.T) {
	tests := []struct {
		command     string
		redirects   bool
		systemWrite bool
	}{
		{command: "echo value > output.txt", redirects: true},
		{command: "echo value > /etc/profile", redirects: true, systemWrite: true},
		{command: `echo value > "/etc/profile"`, redirects: true, systemWrite: true},
		{command: `echo value > "C:\\Windows\\system.ini"`, redirects: true, systemWrite: true},
		{command: "echo 'value > output.txt'", redirects: false},
		{command: "echo '> /etc/profile'", redirects: false, systemWrite: false},
		{command: `echo \> output.txt`, redirects: false},
	}
	for _, test := range tests {
		if got := containsActiveRedirection(test.command); got != test.redirects {
			t.Errorf("containsActiveRedirection(%q) = %v, want %v",
				test.command, got, test.redirects)
		}
		if got := containsActiveSystemWrite(test.command); got != test.systemWrite {
			t.Errorf("containsActiveSystemWrite(%q) = %v, want %v",
				test.command, got, test.systemWrite)
		}
	}
}

func TestSystemWriteCommandsAndLiteralOperators(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed,
		"cp", "dd", "install", "mv", "sed", "tee", "truncate")
	guard, err := New(policy)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		command  string
		decision tool.PermissionAction
		ruleID   string
	}{
		{command: "echo '> /etc/profile'", decision: tool.PermissionActionDeny, ruleID: "path.denied"},
		{command: "echo value > output.txt", decision: tool.PermissionActionAsk, ruleID: "shell.dynamic"},
		{command: "echo value > /etc/profile", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "tee /etc/profile", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "truncate /var/log/system.log", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "dd if=/dev/zero of=/boot/image", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "cp input /tmp/../etc/passwd", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "mv output /var/lib/output", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "install -t /usr/local/bin tool", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "curl -o /etc/profile https://go.dev/x", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "wget -P /var/tmp https://go.dev/x", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "sed -ibak s/a/b/ /etc/hosts", decision: tool.PermissionActionDeny, ruleID: "destructive.system_write"},
		{command: "tee output.txt", decision: tool.PermissionActionAllow, ruleID: "SAFETY_ALLOW"},
	}
	for _, test := range tests {
		report, scanErr := guard.Scan(
			context.Background(),
			commandRequest(BackendWorkspace, test.command),
		)
		if scanErr != nil {
			t.Fatalf("Scan(%q) error = %v", test.command, scanErr)
		}
		if report.Decision != test.decision || !hasRule(report, test.ruleID) {
			t.Errorf("Scan(%q) = %+v, want decision=%s rule=%s",
				test.command, report, test.decision, test.ruleID)
		}
	}
}
func TestSystemWriteCommandTargetSelection(t *testing.T) {
	if commandWritesSystemPath("cp", []string{"/etc/hosts", "workspace-hosts"}) {
		t.Fatal("copying from a system path into the workspace was treated as a system write")
	}
	if !commandWritesSystemPath("wget", []string{"-P", "/tmp/../var/cache", "https://go.dev/x"}) {
		t.Fatal("wget directory-prefix traversal was not treated as a system write")
	}
	if !commandWritesSystemPath("curl", []string{"--output-dir", "/etc", "-O"}) {
		t.Error("curl --output-dir system path was not detected")
	}
	if !commandWritesSystemPath("sed", []string{"-ibak", "s/a/b/", "/etc/hosts"}) {
		t.Fatal("sed attached in-place suffix was not treated as a system write")
	}
}
