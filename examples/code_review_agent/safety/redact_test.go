//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety_test

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

// TestRedact verifies related behavior.
func TestRedact(t *testing.T) {
	in := `api_key=sk-abcdefghijklmnopqrstuvwxyz012345 password=SuperSecretPassword123 token=AKIAIOSFODNN7EXAMPLE Bearer abcdEFGHijklMNOP`
	out := safety.Redact(in)
	for _, banned := range []string{
		"sk-abcdefghijklmnopqrstuvwxyz012345",
		"SuperSecretPassword123",
		"AKIAIOSFODNN7EXAMPLE",
	} {
		if strings.Contains(out, banned) {
			t.Fatalf("secret still present %q in %q", banned, out)
		}
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected redaction markers: %q", out)
	}
}

// TestPermissionGate verifies related behavior.
func TestPermissionGate(t *testing.T) {
	g := safety.DefaultGate()
	if d := g.Check("go vet ./..."); d.Action != safety.ActionAllow {
		t.Fatalf("vet: %+v", d)
	}
	if d := g.Check("curl https://evil"); d.Action != safety.ActionDeny {
		t.Fatalf("curl: %+v", d)
	}
	if d := g.Check("go test ./..."); d.Action != safety.ActionAsk {
		t.Fatalf("go test: %+v", d)
	}
	if d := g.Check("rm -rf /"); d.Action != safety.ActionDeny {
		t.Fatalf("rm: %+v", d)
	}
}
