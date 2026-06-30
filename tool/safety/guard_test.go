//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newTestGuard(t *testing.T, audit *bytes.Buffer) *Guard {
	t.Helper()
	g, err := NewGuard(
		WithPolicyFile(filepath.Join("testdata", "tool_safety_policy.yaml")),
		WithAuditWriter(audit),
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return g
}

func TestGuardDecisions(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		args   string
		action tool.PermissionAction
	}{
		{"safe", "workspace_exec", `{"command":"go test ./..."}`, tool.PermissionActionAllow},
		{"delete", "workspace_exec", `{"command":"rm -rf /"}`, tool.PermissionActionDeny},
		{"secret", "workspace_exec", `{"command":"cat ~/.ssh/id_rsa"}`, tool.PermissionActionDeny},
		{"dep", "workspace_exec", `{"command":"pip install requests"}`, tool.PermissionActionAsk},
		{"host", "exec_command", `{"command":"sleep 5","background":true,"tty":true}`, tool.PermissionActionDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			g := newTestGuard(t, &buf)
			req := &tool.PermissionRequest{ToolName: tc.tool, Arguments: []byte(tc.args)}
			dec, err := g.CheckToolPermission(context.Background(), req)
			if err != nil {
				t.Fatalf("CheckToolPermission: %v", err)
			}
			if dec.Action != tc.action {
				t.Errorf("action = %q, want %q (reason: %s)", dec.Action, tc.action, dec.Reason)
			}
			// A non-allow decision carries a reason.
			if dec.Action != tool.PermissionActionAllow && dec.Reason == "" {
				t.Errorf("non-allow decision must have a reason")
			}
			// Exactly one audit line per scanned exec call.
			if got := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n") + 1; got != 1 {
				t.Errorf("audit lines = %d, want 1", got)
			}
		})
	}
}

func TestGuardNonExecToolShortCircuits(t *testing.T) {
	var buf bytes.Buffer
	g := newTestGuard(t, &buf)
	req := &tool.PermissionRequest{ToolName: "search_file", Arguments: []byte(`{"query":"x"}`)}
	dec, err := g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("non-exec tool action = %q, want allow", dec.Action)
	}
	if buf.Len() != 0 {
		t.Errorf("non-exec tool should not be audited, got %q", buf.String())
	}
}

func TestGuardMalformedArgsFailsClosed(t *testing.T) {
	var buf bytes.Buffer
	g := newTestGuard(t, &buf)
	req := &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: []byte(`{not json`)}
	dec, err := g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("malformed args action = %q, want deny (fail closed)", dec.Action)
	}
	if buf.Len() == 0 {
		t.Errorf("malformed args should still be audited")
	}
}

func TestGuardReportSink(t *testing.T) {
	var got Report
	called := false
	g, err := NewGuard(
		WithPolicyFile(filepath.Join("testdata", "tool_safety_policy.yaml")),
		WithReportSink(func(r Report) { got, called = r, true }),
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	req := &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: []byte(`{"command":"rm -rf /"}`)}
	if _, err := g.CheckToolPermission(context.Background(), req); err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if !called {
		t.Fatalf("report sink not called")
	}
	if got.Decision != DecisionDeny || !hasRule(got.Findings, ruleDangerousID) {
		t.Errorf("sink report = %+v, want deny + R-DEL-001", got)
	}
}

func TestGuardDefaultPolicy(t *testing.T) {
	// No policy file: DefaultPolicy still fails closed on unparsable commands.
	g, err := NewGuard()
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	req := &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: []byte(`{"command":"echo $(id)"}`)}
	dec, err := g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("default policy unparsable action = %q, want deny", dec.Action)
	}
}
