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

func TestAdversarialInlineInterpretersAndDestructiveVCSCommands(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(
		policy.Commands.Allowed,
		"find", "node", "perl", "php", "python", "ruby",
	)
	guard, err := New(policy)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name     string
		command  string
		decision tool.PermissionAction
		ruleID   string
	}{
		{name: "python inline", command: "python -c 'print(1)'", decision: tool.PermissionActionDeny, ruleID: "shell.wrapper"},
		{name: "node inline", command: "node --eval '1+1'", decision: tool.PermissionActionDeny, ruleID: "shell.wrapper"},
		{name: "perl inline", command: "perl -e 'print 1'", decision: tool.PermissionActionDeny, ruleID: "shell.wrapper"},
		{name: "ruby inline", command: "ruby -e 'puts 1'", decision: tool.PermissionActionDeny, ruleID: "shell.wrapper"},
		{name: "php inline", command: "php -r 'echo 1;'", decision: tool.PermissionActionDeny, ruleID: "shell.wrapper"},
		{name: "git clean force", command: "git clean -fdx", decision: tool.PermissionActionDeny, ruleID: "destructive.delete"},
		{name: "git reset hard", command: "git reset --hard", decision: tool.PermissionActionDeny, ruleID: "destructive.delete"},
		{name: "find delete", command: "find . -delete", decision: tool.PermissionActionDeny, ruleID: "destructive.delete"},
		{name: "git clean dry run", command: "git clean -n -fdx", decision: tool.PermissionActionAllow, ruleID: "SAFETY_ALLOW"},
		{name: "find print", command: "find . -print", decision: tool.PermissionActionAllow, ruleID: "SAFETY_ALLOW"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(),
				commandRequest(BackendWorkspace, test.command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != test.decision || !hasRule(report, test.ruleID) {
				t.Fatalf("report = %+v, want decision=%s rule=%s",
					report, test.decision, test.ruleID)
			}
		})
	}
}
