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

// ----------------------------------------------------------------------
// DeniedPackages edge-case tests
// ----------------------------------------------------------------------

func TestDeniedPackage_VersionSpecifier(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = []string{"pip"}
	policy.DependencyPolicy.AllowedManagers = []string{"pip"}
	policy.DependencyPolicy.DeniedPackages = []string{"malicious-pkg"}

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "pip install malicious-pkg==1.0", BackendWorkspaceExec)
	if report.Verdict != VerdictDeny {
		t.Errorf("denied package with version specifier must be denied: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
}

func TestDeniedPackage_NotInCommand(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = []string{"pip"}
	policy.DependencyPolicy.AllowedManagers = []string{"pip"}
	policy.DependencyPolicy.DeniedPackages = []string{"malicious-pkg"}

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "pip install safe-pkg", BackendWorkspaceExec)
	// A non-denied package from an allowed manager should be medium
	// risk (ask), not deny.
	if report.Verdict == VerdictDeny {
		t.Errorf("non-denied package should not be denied: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
}

func TestDeniedPackage_MultiplePackages(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = []string{"pip"}
	policy.DependencyPolicy.AllowedManagers = []string{"pip"}
	policy.DependencyPolicy.DeniedPackages = []string{"malicious-pkg"}

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "pip install foo malicious-pkg bar", BackendWorkspaceExec)
	if report.Verdict != VerdictDeny {
		t.Errorf("denied package among multiple must be denied: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
}

func TestDeniedPackage_NoSubstringMatch(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = []string{"pip"}
	policy.DependencyPolicy.AllowedManagers = []string{"pip"}
	policy.DependencyPolicy.DeniedPackages = []string{"malicious"}

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	// "malicious-pkg" is not an exact match for "malicious", so it
	// should not be denied by the denied-packages check (though it
	// may still be ask due to the medium-risk dependency rule).
	report := scan(t, s, "pip install malicious-pkg", BackendWorkspaceExec)
	if report.Verdict == VerdictDeny {
		t.Errorf("substring should not match denied package: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
}

func TestDeniedPackage_GoImportPath(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = []string{"go"}
	policy.DependencyPolicy.AllowedManagers = []string{"go"}
	policy.DependencyPolicy.DeniedPackages = []string{"github.com/malicious/pkg"}

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "go install github.com/malicious/pkg@latest", BackendWorkspaceExec)
	if report.Verdict != VerdictDeny {
		t.Errorf("denied go package must be denied: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
}

// ----------------------------------------------------------------------
// AllowedSleepSeconds edge-case tests
// ----------------------------------------------------------------------

func TestAllowedSleepSeconds_UnderLimit(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = nil
	policy.Commands.Denied = nil
	policy.ResourceLimits.AllowedSleepSeconds = 5

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "sleep 3", BackendWorkspaceExec)
	// sleep 3 with maxSleep=5 should not trigger the resource_abuse
	// rule.  The command may still be denied for other reasons, but
	// the resource_abuse rule must not fire.
	for _, r := range report.Risks {
		if r.RuleID == "resource_abuse" {
			t.Errorf("sleep under limit should not trigger resource_abuse: %+v", r)
		}
	}
}

func TestAllowedSleepSeconds_ZeroDisables(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = nil
	policy.Commands.Denied = nil
	policy.ResourceLimits.AllowedSleepSeconds = 0

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "sleep 999999", BackendWorkspaceExec)
	for _, r := range report.Risks {
		if r.RuleID == "resource_abuse" {
			t.Errorf("maxSleep=0 should disable sleep check: %+v", r)
		}
	}
}

func TestAllowedSleepSeconds_EqualLimit(t *testing.T) {
	policy := DefaultPolicy()
	policy.Commands.Allowed = nil
	policy.Commands.Denied = nil
	policy.ResourceLimits.AllowedSleepSeconds = 5

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "sleep 5", BackendWorkspaceExec)
	// Equal to the limit should not trigger the check (only > triggers).
	for _, r := range report.Risks {
		if r.RuleID == "resource_abuse" {
			t.Errorf("sleep equal to limit should not trigger resource_abuse: %+v", r)
		}
	}
}

// ----------------------------------------------------------------------
// Backward compatibility: old YAML with removed fields must still load
// ----------------------------------------------------------------------

func TestPolicy_RemovedFields_StillLoadsYAML(t *testing.T) {
	yamlData := `version: "1.0"
default_verdict: allow
env_whitelist:
  - PATH
  - HOME
resource_limits:
  max_timeout_seconds: 600
  max_output_bytes: 10485760
  max_concurrent_processes: 10
  allowed_sleep_seconds: 60
backend_policies:
  workspaceexec:
    allow_pty: true
    allow_background: false
    require_human_review: false
  hostexec:
    allow_pty: false
    allow_background: false
    require_human_review: true
`
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(yamlData), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	policy, err := LoadPolicy(policyPath)
	if err != nil {
		t.Fatalf("LoadPolicy with removed fields should succeed: %v", err)
	}

	// The supported field should still be loaded correctly.
	if policy.ResourceLimits.AllowedSleepSeconds != 60 {
		t.Errorf("allowed_sleep_seconds: got %d want 60",
			policy.ResourceLimits.AllowedSleepSeconds)
	}
}
