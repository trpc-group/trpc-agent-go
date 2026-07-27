//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPolicyFromJSON(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "policy.json")
	jsonContent := `{
  "version": "1.0",
  "denied_commands": ["rm", "dd"],
  "dangerous_patterns": [
    {"pattern": "rm\\s+(-rf?\\s+)?/?$", "risk_level": "critical", "description": "rm root"}
  ],
  "network_policy": {
    "allowed_domains": ["api.github.com"],
    "blocked_domains": [],
    "default_action": "deny"
  },
  "path_policy": {
    "denied_paths": ["/etc/**"],
    "sensitive_paths": ["**/.env"],
    "allowed_paths": ["/tmp/**"]
  },
  "resource_policy": {
    "max_timeout_s": 300,
    "max_output_bytes": 10485760,
    "max_sleep_s": 60
  },
  "decision_policy": {
    "default_on_parse_failure": "deny",
    "default_on_unknown_risk": "ask",
    "ask_on_risk_level": "high"
  },
  "audit_policy": {
    "enabled": true,
    "output_path": "/tmp/audit.jsonl"
  }
}`
	if err := os.WriteFile(tmp, []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	policy, err := LoadPolicy(tmp)
	if err != nil {
		t.Fatalf("LoadPolicy(json): %v", err)
	}
	if policy.Version != "1.0" {
		t.Errorf("version: got %q, want 1.0", policy.Version)
	}
	if len(policy.DeniedCommands) != 2 {
		t.Errorf("denied_commands: got %d, want 2", len(policy.DeniedCommands))
	}
	if policy.NetworkPolicy == nil || len(policy.NetworkPolicy.AllowedDomains) != 1 {
		t.Error("network_policy not parsed correctly")
	}
	if policy.DecisionPolicy == nil || policy.DecisionPolicy.AskOnRiskLevel != "high" {
		t.Error("decision_policy not parsed correctly")
	}
}

// TestLoadPolicyFromInvalidYAML covers the parse error path in loadYAML (75%).
func TestLoadPolicyFromInvalidYAML(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "invalid.yaml")
	content := "version: 1.0\n  invalid indentation\nbroken"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPolicy(tmp)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

// TestLoadPolicyFromInvalidJSON covers the parse error path in loadJSON (75%).
func TestLoadPolicyFromInvalidJSON(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "invalid.json")
	content := `{"version": "1.0", invalid json`
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPolicy(tmp)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
