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
	"errors"
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
			// Exactly one audit line per scanned exec call. Require non-empty
			// output first so a broken audit path cannot pass as "1 line".
			out := strings.TrimRight(buf.String(), "\n")
			if out == "" {
				t.Fatalf("no audit line written")
			}
			if got := strings.Count(out, "\n") + 1; got != 1 {
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

func TestWithPolicyNilRejected(t *testing.T) {
	if _, err := NewGuard(WithPolicy(nil)); err == nil {
		t.Fatal("WithPolicy(nil) should error")
	}
}

func TestWithPolicyCompilesUncompiledPolicy(t *testing.T) {
	// A programmatically built policy (never run through LoadPolicy) must still
	// get its matchers compiled by WithPolicy; otherwise the secret pattern is
	// empty and the value goes un-redacted.
	p := DefaultPolicy()
	p.Secrets.Patterns = []string{`(?i)bearer\s+[a-z0-9._-]+`}

	var last Report
	g, err := NewGuard(WithPolicy(&p), WithReportSink(func(r Report) { last = r }))
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"curl -H \"Authorization: Bearer demo-token-not-a-real-secret\" https://github.com/x"}`),
	}
	if _, err := g.CheckToolPermission(context.Background(), req); err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if !last.Redacted {
		t.Error("expected redaction; WithPolicy must compile the policy copy")
	}
}

// TestWithPolicyPartialPolicyStillScans pins that a partial programmatic
// policy — one that sets only a restriction and leaves Backends nil — cannot
// silently disable scanning. Without the default tool→backend mapping the
// backend index would be empty, workspace_exec would resolve to backend ""
// and CheckToolPermission would allow the call without ever evaluating the
// configured forbidden path.
func TestWithPolicyPartialPolicyStillScans(t *testing.T) {
	g, err := NewGuard(WithPolicy(&Policy{ForbiddenPaths: []string{"/etc/shadow"}}))
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"cat /etc/shadow"}`),
	}
	dec, err := g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("action = %q, want deny; a partial programmatic policy must still scan workspace_exec", dec.Action)
	}
	// The unparsable fail-closed default must survive the partial policy too.
	req = &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo $(id)"}`),
	}
	dec, err = g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("action = %q, want deny for unparsable command under a partial policy", dec.Action)
	}
}

// TestWithPolicyDeepCopyIsolation verifies WithPolicy takes a private deep copy:
// mutating the caller's policy maps/slices after NewGuard must not change the
// guard's decisions, and compile() must not have rewritten the caller's maps.
func TestWithPolicyDeepCopyIsolation(t *testing.T) {
	p := DefaultPolicy()
	p.Commands.Denied = []string{"rm"}
	p.Network.AllowedDomains = []string{"github.com"}
	p.RuleOverrides = map[string]Override{
		"R-NET-001": {Action: "needs_human_review"},
	}

	g, err := NewGuard(WithPolicy(&p))
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	// compile() canonicalizes "needs_human_review" -> ask; that rewrite must
	// happen on the guard's copy, not the caller's original map.
	if got := p.RuleOverrides["R-NET-001"].Action; got != "needs_human_review" {
		t.Errorf("caller override mutated to %q; WithPolicy must deep-copy RuleOverrides", got)
	}

	// Mutating the caller's slices after NewGuard must not affect the guard.
	p.Commands.Denied[0] = "ls"
	p.Network.AllowedDomains[0] = "evil.io"

	// "rm" must still be denied by the guard's private copy.
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm file"}`),
	}
	dec, err := g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("decision = %q, want deny; caller mutation leaked into guard policy", dec.Action)
	}
}

// failingWriter always fails, simulating a full disk / closed sink.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("disk full")
}

// TestAuditWriteFailureSurfaced pins that a broken audit sink is not silent:
// the registered error handler receives every write failure while the tool
// decision itself is still returned.
func TestAuditWriteFailureSurfaced(t *testing.T) {
	var got error
	g, err := NewGuard(
		WithPolicyFile(filepath.Join("testdata", "tool_safety_policy.yaml")),
		WithAuditWriter(failingWriter{}),
		WithAuditErrorHandler(func(e error) { got = e }),
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	req := &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: []byte(`{"command":"go test ./..."}`)}
	dec, err := g.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("audit failure must not change the decision, got %q", dec.Action)
	}
	if got == nil {
		t.Fatalf("audit error handler was not called")
	}
	if !strings.Contains(got.Error(), "disk full") {
		t.Errorf("handler error = %v, want the writer failure", got)
	}
}

// TestWithExecutorBaseDir pins that a relative (or omitted) working directory
// is resolved the way the executor resolves it, so a workdir that escapes into
// a system path cannot hide behind "..". Unset, the value is matched as
// written, which keeps the previous behavior.
func TestWithExecutorBaseDir(t *testing.T) {
	newGuard := func(t *testing.T, base string, sink func(Report)) *Guard {
		t.Helper()
		opts := []Option{
			WithPolicyFile(filepath.Join("testdata", "tool_safety_policy.yaml")),
			WithReportSink(sink),
		}
		if base != "" {
			opts = append(opts, WithExecutorBaseDir(base))
		}
		g, err := NewGuard(opts...)
		if err != nil {
			t.Fatalf("NewGuard: %v", err)
		}
		return g
	}
	check := func(t *testing.T, g *Guard, args string) tool.PermissionDecision {
		t.Helper()
		dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName:  "exec_command",
			Arguments: []byte(args),
		})
		if err != nil {
			t.Fatalf("CheckToolPermission: %v", err)
		}
		return dec
	}

	// A relative workdir that climbs out of the base directory into /etc: the
	// delete targets the system, and the guard must see that.
	var last Report
	g := newGuard(t, "/srv/agent/work", func(r Report) { last = r })
	if dec := check(t, g, `{"command":"rm -rf .","workdir":"../../../etc"}`); dec.Action != tool.PermissionActionDeny {
		t.Errorf("escaping workdir: action = %q, want deny (findings: %+v)", dec.Action, last.Findings)
	}
	if !hasRule(last.Findings, ruleDangerousID) {
		t.Errorf("missing %s for the resolved workdir: %+v", ruleDangerousID, last.Findings)
	}

	// An omitted workdir means the executor's base directory.
	g = newGuard(t, "/etc", func(r Report) { last = r })
	if dec := check(t, g, `{"command":"rm -rf ."}`); dec.Action != tool.PermissionActionDeny {
		t.Errorf("omitted workdir: action = %q, want deny (findings: %+v)", dec.Action, last.Findings)
	}

	// A workdir under the base directory stays allowed: no over-blocking.
	g = newGuard(t, "/srv/agent/work", func(r Report) { last = r })
	if dec := check(t, g, `{"command":"ls -la","workdir":"repo/sub"}`); dec.Action != tool.PermissionActionAllow {
		t.Errorf("in-base workdir: action = %q, want allow (findings: %+v)", dec.Action, last.Findings)
	}
	if last.Command != "ls -la" {
		t.Errorf("unexpected report command: %+v", last)
	}

	// Absolute and home-rooted workdirs are already what the executor uses.
	for _, c := range []struct {
		cwd  string
		want string
	}{
		{"", "/base"},
		{"sub/dir", "/base/sub/dir"},
		{"/abs/dir", "/abs/dir"},
		{"~/dir", "~/dir"},
		{`C:\Windows`, `C:\Windows`},
	} {
		if got := resolveExecCwd(c.cwd, "/base"); got != c.want {
			t.Errorf("resolveExecCwd(%q) = %q, want %q", c.cwd, got, c.want)
		}
	}
	// Without a configured base directory nothing is rewritten.
	if got := resolveExecCwd("sub/dir", ""); got != "sub/dir" {
		t.Errorf("resolveExecCwd without a base dir = %q, want the value as written", got)
	}
}
