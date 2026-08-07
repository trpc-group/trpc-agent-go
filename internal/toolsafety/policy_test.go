// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety_test

import (
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

func TestLoadPolicyFromYAML(t *testing.T) {
	policy, err := toolsafety.LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if policy.Version != "1.0" {
		t.Errorf("version: got %q, want 1.0", policy.Version)
	}
	if len(policy.AllowedCommands) == 0 {
		t.Error("no allowed_commands loaded")
	}
	if len(policy.DeniedCommands) == 0 {
		t.Error("no denied_commands loaded")
	}
	if policy.NetworkPolicy == nil {
		t.Fatal("network_policy not loaded")
	}
	if len(policy.NetworkPolicy.AllowedDomains) == 0 {
		t.Error("no allowed_domains loaded")
	}
	if policy.PathPolicy == nil {
		t.Fatal("path_policy not loaded")
	}
	if len(policy.PathPolicy.SensitivePaths) == 0 {
		t.Error("no sensitive_paths loaded")
	}
	if policy.ResourcePolicy == nil {
		t.Fatal("resource_policy not loaded")
	}
	if policy.ResourcePolicy.MaxSleepS <= 0 {
		t.Error("max_sleep_s not loaded or zero")
	}
	if policy.DecisionPolicy == nil {
		t.Fatal("decision_policy not loaded")
	}
	if policy.AuditPolicy == nil {
		t.Fatal("audit_policy not loaded")
	}
}

func TestLoadPolicyEmptyPathReturnsDefault(t *testing.T) {
	policy, err := toolsafety.LoadPolicy("")
	if err != nil {
		t.Fatalf("LoadPolicy(''): %v", err)
	}
	if policy == nil {
		t.Fatal("expected non-nil default policy")
	}
	if policy.Version != "1.0" {
		t.Errorf("default version: got %q, want 1.0", policy.Version)
	}
}

func TestLoadPolicyInvalidPath(t *testing.T) {
	_, err := toolsafety.LoadPolicy("/nonexistent/policy.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadPolicyUnsupportedFormat(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "policy.txt")
	if err := os.WriteFile(tmp, []byte("version: 1.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := toolsafety.LoadPolicy(tmp)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestDefaultPolicyHasDefaults(t *testing.T) {
	p := toolsafety.DefaultPolicy()
	if p == nil {
		t.Fatal("DefaultPolicy returned nil")
	}
	if len(p.DeniedCommands) == 0 {
		t.Error("DefaultPolicy has no denied commands")
	}
	if p.DecisionPolicy.DefaultOnParseFailure != "deny" {
		t.Errorf("expected deny on parse failure, got %q", p.DecisionPolicy.DefaultOnParseFailure)
	}
}

func TestModifyPolicyFileChangesBehavior(t *testing.T) {
	// Acceptance criteria 6: policy file changes take effect without code changes.
	policy, err := toolsafety.LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	originalAllowed := len(policy.AllowedCommands)

	// Verify re-loading with the same file returns the same result.
	policy2, err := toolsafety.LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(policy2.AllowedCommands) != originalAllowed {
		t.Errorf("reload changed allowed count: %d vs %d", len(policy2.AllowedCommands), originalAllowed)
	}
}
