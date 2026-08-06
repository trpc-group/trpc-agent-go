//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPolicyYAML(t *testing.T) {
	p, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load yaml: %v", err)
	}
	if p.Version != 1 {
		t.Errorf("version=%d want 1", p.Version)
	}
	if p.DefaultDecisionOnParseFailure != DecisionDeny {
		t.Errorf("parse-failure decision=%s want deny", p.DefaultDecisionOnParseFailure)
	}
	if !p.isDomainAllowed("proxy.golang.org") {
		t.Errorf("proxy.golang.org should be allowed")
	}
	if p.isDomainAllowed("evil.example.com") {
		t.Errorf("evil.example.com should not be allowed")
	}
}

func TestLoadPolicyJSONEquivalent(t *testing.T) {
	p, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load yaml: %v", err)
	}
	blob, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	jsonPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(jsonPath, blob, 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}
	pj, err := LoadPolicy(jsonPath)
	if err != nil {
		t.Fatalf("load json: %v", err)
	}
	if pj.Version != p.Version || len(pj.DeniedCommands) != len(p.DeniedCommands) {
		t.Errorf("json policy differs from yaml policy")
	}
	if !pj.isDomainAllowed("github.com") {
		t.Errorf("json policy lost allowed domains")
	}
}

func TestLoadPolicyFromEnv(t *testing.T) {
	t.Setenv(EnvPolicyPath, "testdata/tool_safety_policy.yaml")
	p, err := LoadPolicyFromEnv()
	if err != nil {
		t.Fatalf("load from env: %v", err)
	}
	if p.Version != 1 {
		t.Errorf("version=%d want 1", p.Version)
	}
	t.Setenv(EnvPolicyPath, "")
	if p2, err := LoadPolicyFromEnv(); err != nil || p2 == nil {
		t.Errorf("empty env should fall back to default policy, got %v", err)
	}
}

func TestPolicyRejectsInvalidParseDecision(t *testing.T) {
	p := &Policy{DefaultDecisionOnParseFailure: DecisionAllow}
	if err := p.compile(); err == nil {
		t.Fatalf("expected compile error for allow parse-failure decision")
	}
}

func TestPolicyRejectsBadRegex(t *testing.T) {
	p := &Policy{SecretPatterns: []SecretPattern{{Name: "bad", Regex: "("}}}
	if err := p.compile(); err == nil {
		t.Fatalf("expected compile error for bad secret regex")
	}
}

func TestMatchesDeniedPath(t *testing.T) {
	p := DefaultPolicy()
	cases := []struct {
		arg  string
		want bool
	}{
		{"~/.ssh/id_rsa", true},
		{"$HOME/.ssh/id_rsa", true},
		{"/root/.ssh/id_rsa", true},
		{"/home/deploy/.aws/credentials", true},
		{"/home/bob/.netrc", true},
		{"/home/u/project/.env", true},
		{"/var/www/.env.production", true},
		{"deploy/keys/server.pem", true},
		{"/etc/shadow", true},
		{"./main.go", false},
		{"data.txt", false},
	}
	for _, c := range cases {
		if _, ok := p.matchesDeniedPath(c.arg); ok != c.want {
			t.Errorf("matchesDeniedPath(%q)=%v want %v", c.arg, ok, c.want)
		}
	}
}

func TestPartialPolicyGetsDefaults(t *testing.T) {
	p := &Policy{Version: 1}
	if err := p.compile(); err != nil {
		t.Fatalf("compile minimal policy: %v", err)
	}
	if len(p.networkCmdSet) == 0 {
		t.Errorf("network commands should default")
	}
	if p.Limits.MaxTimeoutSec == 0 {
		t.Errorf("max timeout should default")
	}
	if len(p.secrets) == 0 {
		t.Errorf("secret patterns should default")
	}
}
