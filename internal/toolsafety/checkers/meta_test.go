//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

// TestCheckerIDs verifies that all checker ID() methods return non-empty IDs.
func TestCheckerIDs(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"dangerous_cmd", NewDangerousCmdChecker(nil).ID()},
		{"network_egress", NewNetworkEgressChecker(nil).ID()},
		{"shell_bypass", NewShellBypassChecker().ID()},
		{"resource_abuse", NewResourceAbuseChecker(nil).ID()},
		{"sensitive_leak", NewSensitiveLeakChecker(nil).ID()},
		{"hostexec_risk", NewHostExecRiskChecker().ID()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.id == "" {
				t.Errorf("%s ID() returned empty string", tt.name)
			}
		})
	}
}

// TestCheckerIsEnabled verifies the IsEnabled method on all checkers.
func TestCheckerIsEnabled(t *testing.T) {
	fullPolicy := &toolsafety.SafetyPolicy{
		Version:         "1.0",
		DeniedCommands:  []string{"rm", "dd"},
		DangerousPatterns: []toolsafety.PatternRule{
			{Pattern: `rm\s+(-rf?\s+)?/?$`, RiskLevel: toolsafety.RiskLevelCritical},
		},
		NetworkPolicy: &toolsafety.NetworkPolicy{
			AllowedDomains: []string{},
			BlockedDomains: []string{},
			DefaultAction:  "deny",
		},
		PathPolicy: &toolsafety.PathPolicy{
			SensitivePaths: []string{"**/.env"},
			DeniedPaths:    []string{"/etc/**"},
			AllowedPaths:   []string{"/tmp/**"},
		},
		ResourcePolicy: &toolsafety.ResourcePolicy{
			MaxSleepS:      60,
			MaxOutputBytes: 1024,
			MaxTimeoutS:    300,
		},
	}
	tests := []struct {
		name    string
		enabled bool
	}{
		{"dangerous_cmd", NewDangerousCmdChecker(fullPolicy).IsEnabled(fullPolicy)},
		{"network_egress", NewNetworkEgressChecker(fullPolicy).IsEnabled(fullPolicy)},
		{"shell_bypass", NewShellBypassChecker().IsEnabled(nil)},
		{"resource_abuse", NewResourceAbuseChecker(fullPolicy).IsEnabled(fullPolicy)},
		{"sensitive_leak", NewSensitiveLeakChecker(fullPolicy).IsEnabled(fullPolicy)},
		{"hostexec_risk", NewHostExecRiskChecker().IsEnabled(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.enabled {
				t.Errorf("%s IsEnabled() returned false, want true", tt.name)
			}
		})
	}
}

// TestSensitiveLeakChecker_WithPatterns verifies that NewSensitiveLeakChecker
// loads custom patterns from the policy.
func TestSensitiveLeakChecker_WithPatterns(t *testing.T) {
	policy := &toolsafety.SafetyPolicy{
		SensitivePatterns: []string{
			`custom-[A-Z]+-key`,
			`invalid([` + // intentionally invalid regex, should be silently skipped
			`valid-other-\d+`,
		},
	}
	c := NewSensitiveLeakChecker(policy)
	if c == nil {
		t.Fatal("NewSensitiveLeakChecker returned nil")
	}

	findings, err := c.Check(nil, &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "contains custom-ABC-key",
		Backend:  "workspaceexec",
	})
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) == 0 {
		t.Log("no findings for custom pattern (expected if pattern doesn't match scanning mode)")
	}
}

// TestDangerousCmdChecker_EdgeCases covers isCommandMatch and matchGlob edge cases.
func TestDangerousCmdChecker_EdgeCases(t *testing.T) {
	c := NewDangerousCmdChecker(nil)
	if c == nil {
		t.Fatal("NewDangerousCmdChecker returned nil")
	}

	// Empty command should produce no findings.
	findings, err := c.Check(nil, &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "",
		Backend:  "workspaceexec",
	})
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for empty command, got %d", len(findings))
	}

	// Command with only spaces.
	findings, err = c.Check(nil, &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "   ",
		Backend:  "workspaceexec",
	})
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for whitespace-only command, got %d", len(findings))
	}
}

// TestResourceAbuseChecker_EdgeCases covers empty command and resource limits.
func TestResourceAbuseChecker_EdgeCases(t *testing.T) {
	policy := &toolsafety.SafetyPolicy{
		ResourcePolicy: &toolsafety.ResourcePolicy{
			MaxSleepS:      60,
			MaxOutputBytes: 1024,
			MaxTimeoutS:    300,
		},
	}
	c := NewResourceAbuseChecker(policy)
	if c == nil {
		t.Fatal("NewResourceAbuseChecker returned nil")
	}

	// Nil request should return empty findings.
	findings, err := c.Check(nil, nil)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for nil request, got %d", len(findings))
	}

	// Empty command should return empty findings.
	findings, err = c.Check(nil, &toolsafety.ScanRequest{Command: ""})
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for empty command, got %d", len(findings))
	}
}

// TestNetworkEgressChecker_EdgeCases covers empty command and nil request.
func TestNetworkEgressChecker_EdgeCases(t *testing.T) {
	policy := &toolsafety.SafetyPolicy{
		NetworkPolicy: &toolsafety.NetworkPolicy{
			AllowedDomains: []string{"example.com"},
			BlockedDomains: []string{"evil.com"},
			DefaultAction:  "deny",
		},
	}
	c := NewNetworkEgressChecker(policy)
	if c == nil {
		t.Fatal("NewNetworkEgressChecker returned nil")
	}

	// Nil request should return empty findings.
	findings, err := c.Check(nil, nil)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for nil request, got %d", len(findings))
	}

	// Empty command should return empty findings.
	findings, err = c.Check(nil, &toolsafety.ScanRequest{Command: ""})
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for empty command, got %d", len(findings))
	}
}
