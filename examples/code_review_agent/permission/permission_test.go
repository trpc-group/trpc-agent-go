//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package permission

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestDecide(t *testing.T) {
	if got := Decide("go test ./...").Decision; got != DecisionAllow {
		t.Fatalf("go test decision=%s", got)
	}
	if got := Decide("curl https://example.com").Decision; got != DecisionDeny {
		t.Fatalf("curl decision=%s", got)
	}
	if got := Decide("make test").Decision; got != DecisionNeedsHumanReview {
		t.Fatalf("make decision=%s", got)
	}
}

func TestPolicyCheckToolPermission(t *testing.T) {
	var policy tool.PermissionPolicy = Policy{}
	cases := []struct {
		args string
		want tool.PermissionAction
	}{
		{`{"command":"go vet ./..."}`, tool.PermissionActionAllow},
		{`{"command":"sudo rm -rf /"}`, tool.PermissionActionDeny},
		{`{"command":"make build"}`, tool.PermissionActionAsk},
		{``, tool.PermissionActionDeny},
	}
	for _, tc := range cases {
		decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName:  "skill_run",
			Arguments: []byte(tc.args),
		})
		if err != nil {
			t.Fatalf("args %q: unexpected error %v", tc.args, err)
		}
		if decision.Action != tc.want {
			t.Fatalf("args %q: action=%s want %s", tc.args, decision.Action, tc.want)
		}
	}
	decision, err := policy.CheckToolPermission(context.Background(), nil)
	if err != nil {
		t.Fatalf("nil request: unexpected error %v", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("nil request action=%s want deny", decision.Action)
	}
}
