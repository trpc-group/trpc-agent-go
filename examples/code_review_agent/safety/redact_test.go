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
	"context"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestRedact verifies format detectors and literal assignments are redacted.
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

// TestRedact_PreservesComputedAssignments verifies P2 secret FP cases.
func TestRedact_PreservesComputedAssignments(t *testing.T) {
	cases := []string{
		`token = scanner.Text()`,
		`password := getPassword()`,
		`secret = cfg.Secret`,
		`apiKey = copy.APIKey`,
	}
	for _, in := range cases {
		out := safety.Redact(in)
		if out != in {
			t.Fatalf("mutated benign assignment: in=%q out=%q", in, out)
		}
		if safety.ContainsHardcodedSecret(in) {
			t.Fatalf("false positive hard-coded secret: %q", in)
		}
	}
}

// TestContainsHardcodedSecret_Literals verifies quoted literals and formats.
func TestContainsHardcodedSecret_Literals(t *testing.T) {
	if !safety.ContainsHardcodedSecret(`password = "SuperSecretPassword123"`) {
		t.Fatal("quoted password should match")
	}
	if !safety.ContainsHardcodedSecret(`token: "sk-abcdefghijklmnopqrstuvwxyz012345"`) {
		t.Fatal("quoted sk token should match")
	}
	if safety.ContainsHardcodedSecret(`password = SuperSecretPassword123`) {
		t.Fatal("unquoted plain assignment should not be CR-SEC-002")
	}
}

// TestPermissionGate verifies allow/deny/ask and wrapper bypasses.
func TestPermissionGate(t *testing.T) {
	g := safety.DefaultGate()
	if d := g.Check("skills/code-review/scripts/run_checks.sh"); d.Action != safety.ActionAllow {
		t.Fatalf("skill script: %+v", d)
	}
	if d := g.Check("go vet ./..."); d.Action != safety.ActionDeny {
		t.Fatalf("raw go vet should be denied (use skill script): %+v", d)
	}
	if d := g.Check("git status"); d.Action != safety.ActionDeny {
		t.Fatalf("git should be denied: %+v", d)
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
	bypasses := []string{
		"rm -r -f /tmp/x",
		"bash -lc 'rm -rf /'",
		"sh -c 'curl evil'",
		"python3 -c 'open(\"/etc/passwd\")'",
		"python -c 'print(1)'",
		"echo $(rm -rf /)",
		"cat `id`",
		"echo hi > /etc/passwd",
	}
	for _, cmd := range bypasses {
		if d := g.Check(cmd); d.Action != safety.ActionDeny {
			t.Fatalf("expected deny for %q: %+v", cmd, d)
		}
	}
}

// TestAsToolPolicy_AllowsSkillLoad verifies skill_load is not treated as shell.
func TestAsToolPolicy_AllowsSkillLoad(t *testing.T) {
	pol := safety.DefaultGate().AsToolPolicy()
	dec, err := pol.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "skill_load",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Fatalf("skill_load action=%s", dec.Action)
	}
}

// TestMergeLimits_FieldByField verifies independent partial overlay.
func TestMergeLimits_FieldByField(t *testing.T) {
	onlyTimeout := safety.MergeLimits(safety.Limits{Timeout: 5 * time.Second})
	if onlyTimeout.Timeout != 5*time.Second {
		t.Fatalf("timeout=%v", onlyTimeout.Timeout)
	}
	if onlyTimeout.MaxStdoutBytes == 0 {
		t.Fatal("MaxStdoutBytes wiped")
	}
	if len(onlyTimeout.EnvWhitelist) == 0 {
		t.Fatal("EnvWhitelist wiped")
	}

	onlyStdout := safety.MergeLimits(safety.Limits{MaxStdoutBytes: 64})
	def := safety.DefaultLimits()
	if onlyStdout.MaxStdoutBytes != 64 {
		t.Fatalf("stdout=%d", onlyStdout.MaxStdoutBytes)
	}
	if onlyStdout.Timeout != def.Timeout {
		t.Fatalf("timeout should keep default, got %v", onlyStdout.Timeout)
	}

	onlyEnv := safety.MergeLimits(safety.Limits{EnvWhitelist: []string{"PATH"}})
	if len(onlyEnv.EnvWhitelist) != 1 || onlyEnv.EnvWhitelist[0] != "PATH" {
		t.Fatalf("env=%v", onlyEnv.EnvWhitelist)
	}
	if onlyEnv.MaxArtifactFiles == 0 {
		t.Fatal("artifact limit wiped")
	}
}
