//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

//

package safety

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func testPolicy() *Policy {
	return DefaultPolicy()
}

func newTestScanner() *Scanner {
	return NewScanner(testPolicy())
}

// Case 1: Safe "go test" command — should be allowed.
func TestScan_SafeGoTest(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "go",
		Args:     []string{"test", "./..."},
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionAllow, report.Decision, "safe go test should be allowed")
	assert.Equal(t, RiskLow, report.RiskLevel)
}

// Case 2: Dangerous deletion (rm -rf /) — should be denied.
func TestScan_DangerousDeletion(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "rm",
		Args:     []string{"-rf", "/"},
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision, "rm -rf / must be denied")
	assert.True(t, report.Intercepted, "must be intercepted")
}

// Case 3: Reading secrets (accessing .env) — should be denied.
func TestScan_ReadingSecrets(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "cat",
		Args:     []string{".env"},
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision, "accessing .env must be denied")
}

// Case 4: Non-allowlisted network egress (curl to unknown host).
func TestScan_NonAllowlistedNetworkEgress(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "curl https://evil.example.com/data",
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	assert.True(t,
		report.Decision == DecisionAsk || report.Decision == DecisionDeny,
		"curl to non-allowlisted host must be ask or deny, got %s", report.Decision,
	)
}

// Case 5: Shell wrapper bypass (sh -c) — should be denied.
func TestScan_ShellWrapperBypass(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "sh",
		Args:     []string{"-c", "echo hacked"},
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision, "sh -c must be denied")
}

// Case 6: Piped commands with shell bypass (echo hello | bash -c ...).
func TestScan_PipedCommands(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "echo 'hello' | bash -c 'cat /etc/passwd'",
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	// Pipe into bash -c should be denied by shell_bypass rule.
	assert.Equal(t, DecisionDeny, report.Decision,
		"pipe into bash -c must be denied, got %s", report.Decision)
}

// Case 7: Dependency installation (pip install) — should be ask.
func TestScan_DependencyInstallation(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "pip",
		Args:     []string{"install", "malicious-package"},
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionAsk, report.Decision, "pip install must be ask")
	assert.Equal(t, "dependency_changes", report.Category)
}

// Case 8: Long-running execution (sleep 9999).
func TestScan_LongRunningExecution(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "sleep",
		Args:     []string{"9999"},
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	// sleep with 4+ digits should match the resource_abuse rule.
	assert.Equal(t, DecisionDeny, report.Decision,
		"long sleep must be denied, got %s", report.Decision)
}

// Case 9: Empty command — should be safe.
func TestScan_EmptyCommand(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "",
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionAllow, report.Decision)
}

// Case 10: Hostexec long-session risk (sudo).
func TestScan_HostexecSudoRisk(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "host_exec",
		Command:  "sudo rm -rf /var/log",
		Backend:  "hostexec",
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision,
		"hostexec sudo must be denied, got %s", report.Decision)
}

// Case 11: Ask/human-review scenario (curl to unknown).
func TestScan_AskScenario(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "curl -X POST https://unknown-api.example/data",
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	// curl must at least be ask, not allow.
	assert.NotEqual(t, DecisionAllow, report.Decision,
		"curl must not be auto-allowed")
}

// Case 12: 500-line script — must complete in ≤ 1 second.
func TestScan_LargeScript(t *testing.T) {
	s := newTestScanner()
	// Build a 500-line script.
	script := ""
	for i := 0; i < 500; i++ {
		script += "echo \"line " + string(rune('0'+i%10)) + "\"\n"
	}

	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  script,
		Backend:  "workspaceexec",
	}

	// Time the scan; it must complete within 1 second.
	start := time.Now()
	report := s.Scan(context.Background(), req)
	elapsed := time.Since(start)

	assert.NotNil(t, &report)
	// Even a 500-line safe script should be allowed.
	assert.Equal(t, DecisionAllow, report.Decision)
	assert.Less(t, elapsed, time.Second,
		"500-line script scan took %v, expected <1s", elapsed)
}

// =============================================================================
// Policy Tests
// =============================================================================

func TestDefaultPolicy_LoadsCorrectly(t *testing.T) {
	p := DefaultPolicy()
	assert.Equal(t, "1.0", p.Version)
	assert.NotEmpty(t, p.Rules, "default policy must have rules")
	assert.NotEmpty(t, p.AllowedCommands)
	assert.NotEmpty(t, p.ForbiddenPaths)

	// Verify all 7 categories are covered.
	categories := make(map[string]bool)
	for _, r := range p.Rules {
		categories[r.Category] = true
	}
	expectedCategories := []string{
		"dangerous_commands",
		"sensitive_info",
		"network_egress",
		"shell_bypass",
		"host_execution",
		"dependency_changes",
		"resource_abuse",
	}
	for _, cat := range expectedCategories {
		assert.True(t, categories[cat], "missing category: %s", cat)
	}
}

func TestLoadPolicyFromFile(t *testing.T) {
	// Write a test policy file.
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "test_policy.yaml")
	content := `
version: "1.0"
allowed_commands:
  - echo
  - ls
denied_commands:
  - shutdown
forbidden_paths:
  - /etc/passwd
allowlisted_hosts:
  - api.example.com
rules:
  - id: "test_rule_001"
    category: "dangerous_commands"
    description: "Test rule"
    patterns:
      - "rm -rf /"
    risk_level: "critical"
    action: "deny"
`
	err := os.WriteFile(policyPath, []byte(content), 0644)
	require.NoError(t, err)

	p, err := LoadPolicy(policyPath)
	require.NoError(t, err)
	assert.Equal(t, "1.0", p.Version)
	assert.Equal(t, "shutdown", p.DeniedCommands[0])
	assert.Len(t, p.Rules, 1)
	assert.Equal(t, "test_rule_001", p.Rules[0].ID)
}

// =============================================================================
// Audit Tests
// =============================================================================

func TestAuditor_RecordsAndFlushes(t *testing.T) {
	a := NewAuditor()
	a.Record(AuditEvent{ToolName: "test", Decision: DecisionAllow})
	a.Record(AuditEvent{ToolName: "test2", Decision: DecisionDeny})

	assert.Len(t, a.Events(), 2)

	// Flush to temp file.
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.jsonl")
	err := a.Flush(auditPath)
	require.NoError(t, err)

	// After flush, events cleared.
	assert.Empty(t, a.Events())

	// Verify file content.
	data, err := os.ReadFile(auditPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"tool_name":"test"`)
	assert.Contains(t, string(data), `"tool_name":"test2"`)
}

// =============================================================================
// PermissionPolicy Integration
// =============================================================================

func TestScanner_ImplementsPermissionPolicy(t *testing.T) {
	s := newTestScanner()
	// Compile-time check: Scanner should implement tool.PermissionPolicy.
	var _ tool.PermissionPolicy = s

	// Test with a dangerous command in Arguments.
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	}
	decision, err := s.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.NotEmpty(t, decision.Action)
	// Dangerous command must be denied.
	assert.Equal(t, tool.PermissionAction(DecisionDeny), decision.Action,
		"dangerous command in Arguments must be denied, got %s", decision.Action)
}

// =============================================================================
// Report Structure Tests
// =============================================================================

func TestScanReport_ContainsAllFields(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "test_tool",
		Command:  "echo hello world",
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)

	assert.Equal(t, DecisionAllow, report.Decision)
	assert.Equal(t, RiskLow, report.RiskLevel)
	assert.Equal(t, "test_tool", report.ToolName)
	assert.Equal(t, "workspaceexec", report.Backend)
	assert.False(t, report.Intercepted)
	// For a safe command, rule_id should be empty.
	assert.Empty(t, report.RuleID)
}

// =============================================================================
// OTel Span Attributes
// =============================================================================

func TestSafetySpanAttributesConstants(t *testing.T) {
	assert.Equal(t, "tool.safety.decision", SpanAttrDecision)
	assert.Equal(t, "tool.safety.risk_level", SpanAttrRiskLevel)
	assert.Equal(t, "tool.safety.rule_id", SpanAttrRuleID)
	assert.Equal(t, "tool.safety.backend", SpanAttrBackend)
	assert.Equal(t, "tool.safety.check", SpanNameToolSafety)
}

// =============================================================================
// Additional coverage tests
// =============================================================================

func TestScanner_NilPolicyDefaults(t *testing.T) {
	s := NewScanner(nil)
	assert.NotNil(t, s.policy)
	assert.Equal(t, "1.0", s.policy.Version)
	report := s.Scan(context.Background(), ScanRequest{Command: "echo hello"})
	assert.Equal(t, DecisionAllow, report.Decision)
}

func TestScan_DeniedCommandExplicitlyBlocked(t *testing.T) {
	p := DefaultPolicy()
	p.DeniedCommands = []string{"shutdown", "reboot"}
	s := NewScanner(p)
	req := ScanRequest{Command: "shutdown -h now"}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision)
	assert.Equal(t, "denied_command", report.RuleID)
}

func TestScan_NonAllowlistedCommandMarked(t *testing.T) {
	p := DefaultPolicy()
	// "xyz_mystery" is not in AllowedCommands.
	p.AllowedCommands = []string{"echo", "ls"}
	s := NewScanner(p)
	req := ScanRequest{Command: "xyz_mystery arg1"}
	report := s.Scan(context.Background(), req)
	assert.NotEqual(t, DecisionAllow, report.Decision)
}

func TestScan_AllowlistedHostEnforcement(t *testing.T) {
	p := DefaultPolicy()
	p.AllowlistedHosts = []string{"api.github.com"}
	s := NewScanner(p)
	req := ScanRequest{Command: "curl https://evil.example.com/data"}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision)
	assert.Equal(t, "non_allowlisted_host", report.RuleID)
}

func TestScan_AllowlistedHostPermitted(t *testing.T) {
	p := DefaultPolicy()
	p.AllowlistedHosts = []string{"api.github.com"}
	s := NewScanner(p)
	req := ScanRequest{Command: "curl https://api.github.com/repos"}
	report := s.Scan(context.Background(), req)
	// curl matches network_egress rule (ask), host is allowlisted → no deny from host check.
	assert.NotEqual(t, DecisionDeny, report.Decision,
		"allowlisted host should not cause deny")
}

func TestScan_EnvVarNotAllowlisted(t *testing.T) {
	p := DefaultPolicy()
	p.EnvAllowlist = []string{"PATH", "HOME"}
	s := NewScanner(p)
	req := ScanRequest{
		Command: "echo hello",
		EnvVars: []string{"SECRET_KEY=abc123"},
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision)
	assert.Equal(t, "env_not_allowlisted", report.RuleID)
}

func TestScan_EnvVarAllowlisted(t *testing.T) {
	p := DefaultPolicy()
	p.EnvAllowlist = []string{"PATH", "HOME"}
	s := NewScanner(p)
	req := ScanRequest{
		Command: "echo hello",
		EnvVars: []string{"PATH=/usr/bin", "HOME=/root"},
	}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionAllow, report.Decision)
}

func TestExtractCommandName(t *testing.T) {
	assert.Equal(t, "rm", extractCommandName("rm -rf /"))
	assert.Equal(t, "curl", extractCommandName("  curl https://x.com"))
	assert.Equal(t, "go", extractCommandName("/usr/local/bin/go test ./..."))
	assert.Equal(t, "", extractCommandName(""))
}

func TestExtractHostTarget(t *testing.T) {
	assert.Equal(t, "evil.com", extractHostTarget("curl https://evil.com/path"))
	assert.Equal(t, "api.github.com", extractHostTarget("curl http://api.github.com/repos"))
	assert.Equal(t, "10.0.0.1", extractHostTarget("curl 10.0.0.1:8080/data"))
	assert.Equal(t, "host", extractHostTarget("ssh host -p 22"))
	assert.Equal(t, "", extractHostTarget("curl"))
}

func TestExtractEnvVarName(t *testing.T) {
	assert.Equal(t, "SECRET", extractEnvVarName("SECRET=value"))
	assert.Equal(t, "PATH", extractEnvVarName("PATH=/usr/bin"))
	assert.Equal(t, "NOEQ", extractEnvVarName("NOEQ"))
}

func TestIsAllowed(t *testing.T) {
	list := []string{"echo", "ls", "cat"}
	assert.True(t, isAllowed("echo", list))
	assert.True(t, isAllowed("cat", list))
	assert.False(t, isAllowed("rm", list))
	assert.False(t, isAllowed("", list))
}

func TestRiskOrderAllCases(t *testing.T) {
	assert.Equal(t, 0, riskOrder(RiskLow))
	assert.Equal(t, 1, riskOrder(RiskMedium))
	assert.Equal(t, 2, riskOrder(RiskHigh))
	assert.Equal(t, 3, riskOrder(RiskCritical))
	assert.Equal(t, 0, riskOrder("unknown"))
}

func TestActionOrderAllCases(t *testing.T) {
	assert.Equal(t, 0, actionOrder(DecisionAllow))
	assert.Equal(t, 1, actionOrder(DecisionAsk))
	assert.Equal(t, 2, actionOrder(DecisionNeedsReview))
	assert.Equal(t, 3, actionOrder(DecisionDeny))
	assert.Equal(t, 0, actionOrder("unknown"))
}

func TestExtractCommandFromArgsAllKeys(t *testing.T) {
	assert.Equal(t, "fallback", extractCommandFromArgs(nil, "fallback"))
	assert.Equal(t, "fallback", extractCommandFromArgs([]byte{}, "fallback"))
	assert.Equal(t, "fallback", extractCommandFromArgs([]byte("not json"), "fallback"))
	assert.Equal(t, "rm -rf /", extractCommandFromArgs([]byte(`{"command":"rm -rf /"}`), "fb"))
	assert.Equal(t, "ls -la", extractCommandFromArgs([]byte(`{"cmd":"ls -la"}`), "fb"))
	assert.Equal(t, "print(1)", extractCommandFromArgs([]byte(`{"code":"print(1)"}`), "fb"))
	assert.Equal(t, "echo hi", extractCommandFromArgs([]byte(`{"script":"echo hi"}`), "fb"))
	assert.Equal(t, "fb", extractCommandFromArgs([]byte(`{"other":"x"}`), "fb"))
}

func TestAddSafetySpanAttributes(t *testing.T) {
	report := ScanReport{
		Decision:  DecisionDeny,
		RiskLevel: RiskCritical,
		RuleID:    "test_001",
		Backend:   "test",
	}
	// No span in context — should not panic.
	assert.NotPanics(t, func() {
		AddSafetySpanAttributes(context.Background(), report)
	})
}

func TestStartSafetySpan(t *testing.T) {
	ctx, span := StartSafetySpan(context.Background(), "test_tool")
	assert.NotNil(t, ctx)
	assert.NotNil(t, span)
	span.End()
}

func TestScan_ForbiddenPathDenied(t *testing.T) {
	s := NewScanner(DefaultPolicy())
	req := ScanRequest{Command: "cat /etc/passwd"}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision)
	assert.Equal(t, "forbidden_path", report.RuleID)
}

func TestScan_ExcessiveLengthUpgradeOnly(t *testing.T) {
	// Long safe command — excessive length upgrades to ask/medium.
	s := NewScanner(DefaultPolicy())
	// Build a 11000+ char command that won't match other regex rules.
	// Use "echo x" repeated with line breaks to avoid matching
	// sensitive_leak pattern (consecutive 20+ alphanumerics).
	longCmd := "echo"
	for i := 0; i < 5000; i++ {
		longCmd += " x"
	}
	req := ScanRequest{Command: longCmd}
	report := s.Scan(context.Background(), req)
	// excessive_length adds ask + medium, only if not already worse.
	assert.True(t, report.Decision == DecisionAsk || report.Decision == DecisionDeny,
		"long command should be at least ask, got %s", report.Decision)
	assert.True(t, riskOrder(report.RiskLevel) >= riskOrder(RiskMedium),
		"long command should be at least medium risk, got %s", report.RiskLevel)
}

func TestScan_WorstRuleMetadataPreserved(t *testing.T) {
	// Command that matches both a critical rule and a medium rule.
	// The report should reference the critical rule.
	s := NewScanner(DefaultPolicy())
	req := ScanRequest{Command: "rm -rf / && pip install xyz"}
	report := s.Scan(context.Background(), req)
	assert.Equal(t, DecisionDeny, report.Decision)
	// Should reference the critical rm rule, not the medium pip rule.
	assert.NotEqual(t, "dependency_changes", report.Category,
		"should not be overwritten by lower-severity rule")
}

func TestCompileRules_InvalidRegexWarning(t *testing.T) {
	p := DefaultPolicy()
	p.Rules = []Rule{
		{ID: "bad_regex", Category: "test", Patterns: []string{`[invalid`}},
		{ID: "good_regex", Category: "test", Patterns: []string{`echo`}},
	}
	s := NewScanner(p)
	// Scanner should be created with only the valid rule.
	report := s.Scan(context.Background(), ScanRequest{Command: "echo hello"})
	// The good rule should still match.
	assert.NotEmpty(t, s.compiledRe)
	assert.NotEmpty(t, report.Decision)
}
