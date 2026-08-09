//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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

// TestLoadPolicy_YAML_RejectsUnknownField verifies that a typo in a
// security-relevant YAML field causes LoadPolicy to fail instead of
// silently leaving the field at its zero value (which would fail-open
// an intended review requirement).
//
// Example: "require_human_reveiw" (missing the 'e' in "review") must
// produce an error, not silently leave RequireHumanReview as false.
func TestLoadPolicy_YAML_RejectsUnknownField(t *testing.T) {
	// "require_human_reveiw" is a typo of "require_human_review".
	// With KnownFields(true), this must fail to load.
	yamlData := `version: "1.0"
default_verdict: allow
backend_policies:
  hostexec:
    allow_background: false
    require_human_reveiw: true
`
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(yamlData), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	_, err := LoadPolicy(policyPath)
	if err == nil {
		t.Fatal("LoadPolicy with typo'd field 'require_human_reveiw' must " +
			"fail, not silently leave RequireHumanReview at its zero value")
	}
}

// TestLoadPolicy_YAML_RejectsUnknownTopLevelField verifies that an
// unknown top-level field is rejected by the YAML decoder.
func TestLoadPolicy_YAML_RejectsUnknownTopLevelField(t *testing.T) {
	yamlData := `version: "1.0"
default_verdict: allow
nonexistent_field: "this should cause an error"
`
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(yamlData), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	_, err := LoadPolicy(policyPath)
	if err == nil {
		t.Fatal("LoadPolicy with unknown top-level field must fail")
	}
}

// TestLoadPolicy_YAML_RejectsUnknownNestedField verifies that an
// unknown field nested inside a sub-struct is also rejected.
func TestLoadPolicy_YAML_RejectsUnknownNestedField(t *testing.T) {
	yamlData := `version: "1.0"
default_verdict: allow
resource_limits:
  allowed_sleep_seconds: 30
  nonexistent_limit: 999
`
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(yamlData), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	_, err := LoadPolicy(policyPath)
	if err == nil {
		t.Fatal("LoadPolicy with unknown nested field must fail")
	}
}

// TestLoadPolicy_YAML_ShippedPolicyLoads verifies that the shipped
// tool_safety_policy.yaml loads without error under KnownFields(true),
// ensuring strict mode does not produce false positives on the real
// policy.
func TestLoadPolicy_YAML_ShippedPolicyLoads(t *testing.T) {
	policyPath := filepath.Join("..", "safety", "tool_safety_policy.yaml")
	_, err := LoadPolicy(policyPath)
	if err != nil {
		t.Fatalf("shipped tool_safety_policy.yaml must load under "+
			"KnownFields(true) without error: %v", err)
	}
}
