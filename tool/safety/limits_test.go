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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExecutionLimitsMustBeExplicitPositiveAndBounded(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		name     string
		mutate   func(*Request)
		decision tool.PermissionAction
		ruleID   string
	}{
		{
			name:     "missing timeout",
			mutate:   func(request *Request) { request.TimeoutMS = 0 },
			decision: tool.PermissionActionAsk,
			ruleID:   "limits.timeout_unspecified",
		},
		{
			name:     "missing output limit",
			mutate:   func(request *Request) { request.MaxOutputBytes = 0 },
			decision: tool.PermissionActionAsk,
			ruleID:   "limits.output_unspecified",
		},
		{
			name:     "negative timeout",
			mutate:   func(request *Request) { request.Timeout = -time.Second },
			decision: tool.PermissionActionDeny,
			ruleID:   "limits.invalid",
		},
		{
			name:     "negative output limit",
			mutate:   func(request *Request) { request.MaxOutputBytes = -1 },
			decision: tool.PermissionActionDeny,
			ruleID:   "limits.invalid",
		},
		{
			name:     "timeout over policy",
			mutate:   func(request *Request) { request.TimeoutMS = 121_000 },
			decision: tool.PermissionActionAsk,
			ruleID:   "limits.timeout",
		},
		{
			name:     "output over policy",
			mutate:   func(request *Request) { request.MaxOutputBytes = (1 << 20) + 1 },
			decision: tool.PermissionActionAsk,
			ruleID:   "limits.output",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := commandRequest(BackendWorkspace, "go test ./tool/safety")
			test.mutate(&request)
			report, scanErr := guard.Scan(context.Background(), request)
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
