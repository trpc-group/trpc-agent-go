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
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestAuditFailurePolicyControlsPreExecutionDecision(t *testing.T) {
	tests := []struct {
		name        string
		action      tool.PermissionAction
		decision    tool.PermissionAction
		failureRule bool
	}{
		{name: "fail closed", action: tool.PermissionActionDeny, decision: tool.PermissionActionDeny, failureRule: true},
		{name: "require review", action: tool.PermissionActionAsk, decision: tool.PermissionActionAsk, failureRule: true},
		{name: "explicit fail open", action: tool.PermissionActionAllow, decision: tool.PermissionActionAllow, failureRule: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := testPolicy()
			policy.Actions.AuditFailure = test.action
			hookCalls := 0
			guard, err := New(
				policy,
				WithAuditSink(NewJSONLAuditSink(failingWriter{err: errors.New("disk unavailable")})),
				WithAuditErrorHook(func(error) { hookCalls++ }),
			)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			report, err := guard.Scan(
				context.Background(),
				commandRequest(BackendWorkspace, "go test ./tool/safety"),
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if report.Decision != test.decision {
				t.Fatalf("decision = %s, want %s; report=%+v",
					report.Decision, test.decision, report)
			}
			if hasRule(report, "audit.failure") != test.failureRule {
				t.Fatalf("audit.failure present = %v, want %v; matches=%+v",
					hasRule(report, "audit.failure"), test.failureRule, report.Matches)
			}
			if hookCalls != 1 {
				t.Fatalf("audit error hook calls = %d, want 1", hookCalls)
			}
		})
	}
}
