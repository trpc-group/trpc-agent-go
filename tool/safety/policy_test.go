//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if p.UnparsableAction != ActionDeny {
		t.Fatalf("unparsable_action = %q, want deny", p.UnparsableAction)
	}
	if p.DefaultAction != ActionAllow {
		t.Fatalf("default_action = %q, want allow", p.DefaultAction)
	}
	if p.Network.OnNonWhitelisted != ActionDeny {
		t.Fatalf("on_non_whitelisted = %q, want deny", p.Network.OnNonWhitelisted)
	}
	// compile so backendFor works on the default map.
	if err := p.compile(); err != nil {
		t.Fatalf("compile default: %v", err)
	}
	cases := map[string]string{
		"workspace_exec": BackendWorkspace,
		"exec_command":   BackendHost,
		"execute_code":   BackendCode,
		"search_file":    "", // non-exec tool: not mapped
	}
	for tool, want := range cases {
		if got := p.backendFor(tool); got != want {
			t.Errorf("backendFor(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestLoadPolicyYAML(t *testing.T) {
	p, err := LoadPolicy(filepath.Join("testdata", "tool_safety_policy.yaml"))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if p.Version != 1 {
		t.Errorf("version = %d, want 1", p.Version)
	}
	if p.UnparsableAction != ActionDeny {
		t.Errorf("unparsable_action = %q, want deny", p.UnparsableAction)
	}
	if got := len(p.DeniedSubcommands); got != 6 {
		t.Errorf("denied_subcommands = %d, want 6", got)
	}
	if p.backendFor("exec_command") != BackendHost {
		t.Errorf("exec_command not mapped to host backend")
	}
	// shellsafe policy must be active given the configured lists.
	if !p.shellPolicy().Active() {
		t.Errorf("shellPolicy not active")
	}
}

func TestLoadPolicyJSON(t *testing.T) {
	p, err := LoadPolicy(filepath.Join("testdata", "tool_safety_policy.json"))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if p.Version != 1 {
		t.Errorf("version = %d, want 1", p.Version)
	}
	if p.UnparsableAction != ActionDeny {
		t.Errorf("unparsable_action = %q, want deny", p.UnparsableAction)
	}
	if got := len(p.DeniedSubcommands); got != 6 {
		t.Errorf("denied_subcommands = %d, want 6", got)
	}
	if p.backendFor("exec_command") != BackendHost {
		t.Errorf("exec_command not mapped to host backend")
	}
	if !p.shellPolicy().Active() {
		t.Errorf("shellPolicy not active")
	}
	// The JSON sample must parse to the same policy as the YAML sample, proving
	// LoadPolicy's json and yaml branches are interchangeable (acceptance: the
	// policy file may be YAML or JSON).
	y, err := LoadPolicy(filepath.Join("testdata", "tool_safety_policy.yaml"))
	if err != nil {
		t.Fatalf("LoadPolicy yaml: %v", err)
	}
	p.compiled, y.compiled = compiledPolicy{}, compiledPolicy{} // compiled fields are not comparable
	if !reflect.DeepEqual(p, y) {
		t.Errorf("json and yaml policies differ:\njson=%+v\nyaml=%+v", p, y)
	}
}

func TestCompileOverrideNormalization(t *testing.T) {
	p := DefaultPolicy()
	p.RuleOverrides = map[string]Override{
		"R-DEP-001": {Action: "needs_human_review"},
	}
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if ov := p.RuleOverrides["R-DEP-001"]; ov.Action != ActionAsk {
		t.Errorf("override action = %q, want ask (canonicalized)", ov.Action)
	}
}

func TestCompileRejectsUnknownRiskLevel(t *testing.T) {
	p := DefaultPolicy()
	p.RuleOverrides = map[string]Override{
		"R-NET-001": {RiskLevel: "severe"}, // not one of none/low/medium/high/critical
	}
	err := p.compile()
	if err == nil {
		t.Fatalf("compile should reject an unknown risk_level")
	}
	if !strings.Contains(err.Error(), "unknown risk_level") {
		t.Errorf("error = %v, want unknown risk_level error", err)
	}
}

func TestCompileOverrideRiskLevelNormalization(t *testing.T) {
	p := DefaultPolicy()
	p.RuleOverrides = map[string]Override{
		"R-NET-001": {RiskLevel: "High"}, // mixed case
	}
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if ov := p.RuleOverrides["R-NET-001"]; ov.RiskLevel != RiskHigh {
		t.Errorf("override risk_level = %q, want high (canonicalized)", ov.RiskLevel)
	}
}

func TestLoadPolicyBadExtension(t *testing.T) {
	// A real file with an unsupported extension, so the failure is the extension
	// check and not a missing file (LoadPolicy reads before checking the ext).
	path := filepath.Join(t.TempDir(), "policy.txt")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write temp policy: %v", err)
	}
	_, err := LoadPolicy(path)
	if err == nil {
		t.Fatalf("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), "unsupported policy extension") {
		t.Errorf("error = %v, want unsupported-extension error", err)
	}
}

func TestCompileRejectsUnknownBackend(t *testing.T) {
	p := DefaultPolicy()
	p.Backends["hostexec"] = []string{"exec_command"} // typo: should be "host"
	if err := p.compile(); err == nil {
		t.Errorf("compile should reject an unknown backend name")
	}
}

func TestCompileRejectsDuplicateToolMapping(t *testing.T) {
	p := DefaultPolicy()
	p.Backends = map[string][]string{
		BackendWorkspace: {"shared_tool"},
		BackendHost:      {"shared_tool"},
	}
	if err := p.compile(); err == nil {
		t.Errorf("compile should reject a tool mapped to two backends")
	}
}

func TestCompileBadRegex(t *testing.T) {
	p := DefaultPolicy()
	p.Secrets.Patterns = []string{"("} // invalid regex
	if err := p.compile(); err == nil {
		t.Fatalf("expected compile error for bad secret regex")
	}
}

func TestCompileUnknownAction(t *testing.T) {
	p := DefaultPolicy()
	p.DefaultAction = "maybe"
	if err := p.compile(); err == nil {
		t.Fatalf("expected compile error for unknown action")
	}
}

func TestNeedsHumanReviewNormalized(t *testing.T) {
	p := DefaultPolicy()
	p.DefaultAction = "needs_human_review"
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if p.DefaultAction != ActionAsk {
		t.Fatalf("needs_human_review normalized to %q, want ask", p.DefaultAction)
	}
}

func TestForbiddenMatch(t *testing.T) {
	p := DefaultPolicy()
	p.ForbiddenPaths = []string{"~/.ssh", "**/.env", "**/id_rsa", "/etc/shadow", "/"}
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	hits := []string{
		"~/.ssh/id_rsa",
		"~/.ssh",
		"project/.env",
		".env",
		"deploy/secrets/id_rsa",
		"/etc/shadow",
		"/",
	}
	for _, c := range hits {
		if _, ok := p.forbiddenMatch(c); !ok {
			t.Errorf("forbiddenMatch(%q) = false, want true", c)
		}
	}
	misses := []string{
		"main.go",
		"src/app.py",
		"environment.txt", // must not match **/.env
	}
	for _, c := range misses {
		if pat, ok := p.forbiddenMatch(c); ok {
			t.Errorf("forbiddenMatch(%q) = true (pattern %q), want false", c, pat)
		}
	}
}

func TestDomainAllowed(t *testing.T) {
	p := DefaultPolicy()
	p.Network.AllowedDomains = []string{"github.com", "*.googleapis.com"}
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	allowed := []string{"github.com", "storage.googleapis.com", "googleapis.com"}
	for _, h := range allowed {
		if !p.domainAllowed(h) {
			t.Errorf("domainAllowed(%q) = false, want true", h)
		}
	}
	denied := []string{"evil.io", "github.com.evil.io", "notgithub.com", ""}
	for _, h := range denied {
		if p.domainAllowed(h) {
			t.Errorf("domainAllowed(%q) = true, want false", h)
		}
	}
}
