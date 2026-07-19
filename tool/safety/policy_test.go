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
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPolicyYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	content := `version: 1
allowed_commands: [go, git, ls]
denied_commands: [curl, wget]
network:
  allowed_hosts: [proxy.golang.org]
  decision: deny
limits:
  max_timeout_sec: 300
dependency_install_decision: deny
parse_error_decision: deny
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(p.AllowedCommands) != 3 {
		t.Errorf("allowed = %v", p.AllowedCommands)
	}
	if p.DependencyInstallDecision != DecisionDeny {
		t.Errorf("dep decision = %q", p.DependencyInstallDecision)
	}
	if p.Limits.MaxTimeoutSec != 300 {
		t.Errorf("timeout = %d", p.Limits.MaxTimeoutSec)
	}
	// Defaults preserved for unspecified fields.
	if len(p.DeniedPaths) == 0 {
		t.Error("denied paths default should be preserved")
	}
}

func TestLoadPolicyJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	content := `{"version":1,"denied_commands":["nc"],"parse_error_decision":"ask"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.ParseErrorDecision != DecisionAsk {
		t.Errorf("parse decision = %q", p.ParseErrorDecision)
	}
}

func TestLoadPolicyRejectsAllowParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("parse_error_decision: allow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(path); err == nil {
		t.Fatal("expected rejection of allow parse-error decision")
	}
}

func TestLoadPolicyMissingFile(t *testing.T) {
	if _, err := LoadPolicy(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDefaultPolicyValid(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatalf("default policy invalid: %v", err)
	}
}
