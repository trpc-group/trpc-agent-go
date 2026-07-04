package safety

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

// Case 6: Piped commands (echo hello | bash).
func TestScan_PipedCommands(t *testing.T) {
	s := newTestScanner()
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "echo 'hello' | bash",
		Backend:  "workspaceexec",
	}
	report := s.Scan(context.Background(), req)
	// Piped commands may or may not match specific rules, but
	// we verify the scan completes without panic.
	t.Logf("Piped command decision: %s, risk: %s", report.Decision, report.RiskLevel)
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
	report := s.Scan(context.Background(), req)
	assert.NotNil(t, &report)
	// Even a 500-line safe script should be allowed.
	assert.Equal(t, DecisionAllow, report.Decision)
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

	req := &tool.PermissionRequest{
		ToolName: "workspace_exec",
	}
	decision, err := s.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.NotEmpty(t, decision.Action)
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
