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

func TestLoadPolicyMergesHostExecFlagsWithoutDecision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hostexec.yaml")
	// Only allow_background/allow_pty are set (no host_exec.decision).
	// These must survive the merge instead of being dropped, and the
	// default decision must be preserved.
	content := `version: 1
host_exec:
  allow_background: true
  allow_pty: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !p.HostExec.AllowBackground {
		t.Error("host_exec.allow_background should survive merge")
	}
	if !p.HostExec.AllowPTY {
		t.Error("host_exec.allow_pty should survive merge")
	}
	if p.HostExec.Decision != DecisionAsk {
		t.Errorf("host_exec.decision default should be preserved, got %q", p.HostExec.Decision)
	}
}

// TestMergePolicyListsAndScalars exercises the per-section merge helpers
// introduced when mergePolicy was decomposed to satisfy gocyclo: lists
// present in the loaded policy replace the defaults (and an explicit
// empty list clears them), scalars replace only when set, and untouched
// sections keep their defaults.
func TestMergePolicyListsAndScalars(t *testing.T) {
	loaded := Policy{
		DeniedCommands:            []string{"nc"}, // replaces default
		DeniedPaths:               []string{},     // explicit clear
		Network:                   NetworkPolicy{AllowedHosts: []string{"proxy.golang.org"}},
		Limits:                    LimitsPolicy{MaxSleepSec: 5},
		Env:                       EnvPolicy{AllowedNames: []string{"PATH"}},
		DependencyInstallDecision: DecisionDeny,
	}
	out := mergePolicy(DefaultPolicy(), loaded)

	if len(out.DeniedCommands) != 1 || out.DeniedCommands[0] != "nc" {
		t.Errorf("denied_commands not replaced: %v", out.DeniedCommands)
	}
	if len(out.DeniedPaths) != 0 {
		t.Errorf("explicit empty denied_paths should clear defaults, got %v", out.DeniedPaths)
	}
	if len(out.Network.AllowedHosts) != 1 {
		t.Errorf("network.allowed_hosts not merged: %v", out.Network.AllowedHosts)
	}
	// Network.Decision was not set in loaded, so the default (deny) stays.
	if out.Network.Decision != DecisionDeny {
		t.Errorf("network.decision default not preserved: %q", out.Network.Decision)
	}
	// A scalar that was set replaces; one that was not keeps the default.
	if out.Limits.MaxSleepSec != 5 {
		t.Errorf("limits.max_sleep_sec not merged: %d", out.Limits.MaxSleepSec)
	}
	if out.Limits.MaxTimeoutSec != DefaultPolicy().Limits.MaxTimeoutSec {
		t.Errorf("limits.max_timeout_sec default not preserved: %d", out.Limits.MaxTimeoutSec)
	}
	if out.DependencyInstallDecision != DecisionDeny {
		t.Errorf("dependency_install_decision not merged: %q", out.DependencyInstallDecision)
	}
	// Env denied names untouched -> default preserved.
	if len(out.Env.DeniedNames) == 0 {
		t.Error("env.denied_names default should be preserved when not overridden")
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
