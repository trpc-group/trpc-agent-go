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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// =====================================================================
// LoadPolicy from file (YAML and JSON)
// =====================================================================

func TestLoadPolicy_FromYAMLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
version: "safetyguard/v1"
commands:
  denied:
    - rm
network:
  enabled: true
  allowed_domains:
    - localhost
decision:
  mode: fail_closed
  on_parse_error: deny
`), 0o644))

	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	require.True(t, policy.Active())
	require.Equal(t, "safetyguard/v1", policy.Version)
	require.Contains(t, policy.Commands.Denied, "rm")
}

func TestLoadPolicy_FromJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
  "version": "safetyguard/v1",
  "commands": {"denied": ["rm"]},
  "network": {"enabled": true, "allowed_domains": ["localhost"]},
  "decision": {"mode": "fail_closed", "on_parse_error": "deny"}
}`), 0o644))

	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	require.True(t, policy.Active())
	require.Contains(t, policy.Commands.Denied, "rm")
}

func TestLoadPolicy_FileNotFound(t *testing.T) {
	_, err := LoadPolicy("/nonexistent/path/policy.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read policy")
}

func TestLoadPolicy_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("{{invalid yaml"), 0o644))

	_, err := LoadPolicy(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse yaml")
}

func TestLoadPolicy_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{invalid json"), 0o644))

	_, err := LoadPolicy(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse json")
}

// =====================================================================
// detectFormat
// =====================================================================

func TestDetectFormat(t *testing.T) {
	require.Equal(t, FormatJSON, detectFormat("policy.json"))
	require.Equal(t, FormatJSON, detectFormat("policy.JSON"))
	require.Equal(t, FormatYAML, detectFormat("policy.yaml"))
	require.Equal(t, FormatYAML, detectFormat("policy.yml"))
	require.Equal(t, FormatYAML, detectFormat("policy.txt"))
	require.Equal(t, FormatYAML, detectFormat("policy"))
}

// =====================================================================
// Guard.Policy() accessor
// =====================================================================

func TestGuard_PolicyAccessor(t *testing.T) {
	policy := DefaultSafetyPolicy()
	g := NewGuard(policy, WithClock(fixedClock))
	got := g.Policy()
	require.Equal(t, policy.Version, got.Version)
	require.True(t, got.Active())
}

func TestGuard_PolicyAccessor_ZeroPolicy(t *testing.T) {
	g := NewGuard(SafetyPolicy{})
	got := g.Policy()
	// withDefaults fills DependencyChanges and PrivilegeEscalation with
	// defaults, so the zero policy is active after construction.
	require.True(t, got.Active())
	require.Equal(t, DefaultPolicyVersion, got.Version)
	require.NotEmpty(t, got.Commands.DependencyChanges)
	require.NotEmpty(t, got.Commands.PrivilegeEscalation)
}

// =====================================================================
// Policy validation edge cases
// =====================================================================

func TestSafetyPolicy_Validate_EmptyMode(t *testing.T) {
	p := SafetyPolicy{
		Decision: DecisionConfig{
			Mode:         "",
			OnParseError: ParseErrorDeny,
		},
	}
	// withDefaults fills Mode, so Validate should pass.
	err := p.withDefaults().Validate()
	require.NoError(t, err)
}

func TestSafetyPolicy_Validate_UnknownParseErrorAction(t *testing.T) {
	p := SafetyPolicy{
		Decision: DecisionConfig{
			Mode:         DecisionModeFailClosed,
			OnParseError: "bogus",
		},
	}
	err := p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown parse-error action")
}

func TestSafetyPolicy_Validate_InvalidRiskThresholdAsk(t *testing.T) {
	p := SafetyPolicy{
		Decision: DecisionConfig{
			Mode:              DecisionModeFailClosed,
			OnParseError:      ParseErrorDeny,
			RiskThresholdAsk:  "bogus",
			RiskThresholdDeny: RiskLevelHigh,
		},
	}
	err := p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid risk_threshold_ask")
}

func TestSafetyPolicy_Validate_InvalidRiskThresholdDeny(t *testing.T) {
	p := SafetyPolicy{
		Decision: DecisionConfig{
			Mode:              DecisionModeFailClosed,
			OnParseError:      ParseErrorDeny,
			RiskThresholdAsk:  RiskLevelMedium,
			RiskThresholdDeny: "bogus",
		},
	}
	err := p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid risk_threshold_deny")
}

func TestSafetyPolicy_Validate_EmptyOnParseError(t *testing.T) {
	p := SafetyPolicy{
		Decision: DecisionConfig{
			Mode:         DecisionModeFailClosed,
			OnParseError: "",
		},
	}
	err := p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "on_parse_error is empty")
}

// =====================================================================
// ParsePolicy error paths
// =====================================================================

func TestParsePolicy_InvalidYAML(t *testing.T) {
	_, err := ParsePolicy([]byte("{{bad"), FormatYAML)
	require.Error(t, err)
}

func TestParsePolicy_InvalidJSON(t *testing.T) {
	_, err := ParsePolicy([]byte("{bad"), FormatJSON)
	require.Error(t, err)
}

func TestParsePolicy_ValidationError(t *testing.T) {
	_, err := ParsePolicy([]byte(`decision:
  mode: bogus
  on_parse_error: deny
`), FormatYAML)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown decision mode")
}

// =====================================================================
// reportReason edge cases
// =====================================================================

func TestReportReason_NoFindings(t *testing.T) {
	reason := reportReason(ScanReport{})
	require.Equal(t, "tool call blocked by safety policy", reason)
}

func TestReportReason_MultipleFindings(t *testing.T) {
	reason := reportReason(ScanReport{
		Findings: []Finding{
			{Type: FindingDangerousCommand, Detail: "rm is denied"},
			{Type: FindingShellBypass, Detail: "sh is denied"},
		},
	})
	require.Contains(t, reason, "rm is denied")
	require.Contains(t, reason, "plus additional findings")
}

func TestReportReason_SingleFinding(t *testing.T) {
	reason := reportReason(ScanReport{
		Findings: []Finding{
			{Type: FindingDangerousCommand, Detail: "rm is denied"},
		},
	})
	require.Contains(t, reason, "rm is denied")
	require.NotContains(t, reason, "plus additional findings")
}

// =====================================================================
// AuditWriter edge cases
// =====================================================================

func TestNewAuditWriter_NilWriter(t *testing.T) {
	w := NewAuditWriter(nil)
	require.Nil(t, w)
}

func TestAuditWriter_NilReceiver(t *testing.T) {
	var a *AuditWriter
	err := a.Write(AuditEvent{ToolName: "test"})
	require.NoError(t, err)
}

func TestAuditWriter_EncodeError(t *testing.T) {
	// A writer that always fails.
	w := NewAuditWriter(&failingWriter{})
	require.Error(t, w.Write(AuditEvent{ToolName: "test"}))
}

// failingWriter always returns an error on Write.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) {
	return 0, errFailing
}

var errFailing = newFailingError()

func newFailingError() error {
	return &simpleError{msg: "write failed"}
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

// =====================================================================
// escalateHost coverage
// =====================================================================

func TestEscalateHost_NotHostExec(t *testing.T) {
	level := escalateHost(false, []Finding{{Type: FindingDangerousCommand, RiskLevel: RiskLevelHigh}})
	require.Equal(t, RiskLevelNone, level)
}

func TestEscalateHost_NoFindings(t *testing.T) {
	level := escalateHost(true, nil)
	require.Equal(t, RiskLevelNone, level)
}

func TestEscalateHost_LowToMedium(t *testing.T) {
	level := escalateHost(true, []Finding{{Type: FindingEnvViolation, RiskLevel: RiskLevelLow}})
	require.Equal(t, RiskLevelMedium, level)
}

func TestEscalateHost_HighToCritical(t *testing.T) {
	level := escalateHost(true, []Finding{{Type: FindingDangerousCommand, RiskLevel: RiskLevelHigh}})
	require.Equal(t, RiskLevelCritical, level)
}

func TestEscalateHost_CriticalStaysCritical(t *testing.T) {
	level := escalateHost(true, []Finding{{Type: FindingShellBypass, RiskLevel: RiskLevelCritical}})
	require.Equal(t, RiskLevelCritical, level)
}

func TestEscalateHost_NoneStaysNone(t *testing.T) {
	level := escalateHost(true, []Finding{{Type: FindingParseError, RiskLevel: RiskLevelNone}})
	require.Equal(t, RiskLevelNone, level)
}

// =====================================================================
// redactedValue coverage
// =====================================================================

func TestRedactedValue_String(t *testing.T) {
	out := redactedValue("hello world")
	require.NotEmpty(t, out)
}

func TestRedactedValue_NonString(t *testing.T) {
	out := redactedValue(42)
	require.NotEmpty(t, out)

	out2 := redactedValue(map[string]any{"key": "val"})
	require.NotEmpty(t, out2)
}

// =====================================================================
// findingTypes and boolStr coverage
// =====================================================================

func TestFindingTypes_Empty(t *testing.T) {
	require.Equal(t, "", findingTypes(nil))
}

func TestFindingTypes_Deduplicates(t *testing.T) {
	findings := []Finding{
		{Type: FindingDangerousCommand},
		{Type: FindingDangerousCommand},
		{Type: FindingShellBypass},
	}
	out := findingTypes(findings)
	require.NotEmpty(t, out)
	var types []string
	require.NoError(t, json.Unmarshal([]byte(out), &types))
	require.Contains(t, types, FindingDangerousCommand)
	require.Contains(t, types, FindingShellBypass)
	require.Len(t, types, 2)
}

func TestBoolStr(t *testing.T) {
	require.Equal(t, "true", boolStr(true))
	require.Equal(t, "false", boolStr(false))
}

// =====================================================================
// validRiskLevel coverage
// =====================================================================

func TestValidRiskLevel(t *testing.T) {
	require.True(t, validRiskLevel(RiskLevelNone))
	require.True(t, validRiskLevel(RiskLevelLow))
	require.True(t, validRiskLevel(RiskLevelMedium))
	require.True(t, validRiskLevel(RiskLevelHigh))
	require.True(t, validRiskLevel(RiskLevelCritical))
	require.False(t, validRiskLevel(RiskLevel("bogus")))
}

// =====================================================================
// intValue coverage
// =====================================================================

func TestIntValue_Float64(t *testing.T) {
	args := map[string]any{"timeout": float64(42)}
	require.Equal(t, 42, intValue(args, "timeout"))
}

func TestIntValue_Int(t *testing.T) {
	args := map[string]any{"timeout": 42}
	require.Equal(t, 42, intValue(args, "timeout"))
}

func TestIntValue_String(t *testing.T) {
	args := map[string]any{"timeout": "42"}
	require.Equal(t, 42, intValue(args, "timeout"))
}

func TestIntValue_InvalidString(t *testing.T) {
	args := map[string]any{"timeout": "not-a-number"}
	require.Equal(t, 0, intValue(args, "timeout"))
}

func TestIntValue_MissingKey(t *testing.T) {
	args := map[string]any{}
	require.Equal(t, 0, intValue(args, "timeout"))
}

func TestIntValue_MultipleKeys(t *testing.T) {
	args := map[string]any{"timeoutSec": float64(30)}
	require.Equal(t, 30, intValue(args, "timeout_sec", "timeoutSec", "timeout"))
}

// =====================================================================
// truncate coverage
// =====================================================================

func TestTruncate_ZeroMax(t *testing.T) {
	require.Equal(t, "hello", truncate("hello", 0))
}

func TestTruncate_NegativeMax(t *testing.T) {
	require.Equal(t, "hello", truncate("hello", -1))
}

func TestTruncate_ShortString(t *testing.T) {
	require.Equal(t, "hi", truncate("hi", 10))
}

func TestTruncate_LongString(t *testing.T) {
	out := truncate("hello world this is long", 5)
	require.Equal(t, "hello...", out)
}

// =====================================================================
// commandField with custom mapping
// =====================================================================

func TestCommandField_CustomMapping(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.ToolCommandFields["my_tool"] = "script"
	g := NewGuard(policy, WithClock(fixedClock))

	report := g.Scan(context.Background(), "my_tool",
		mustJSONArgs(t, map[string]any{
			"script": "rm -rf /tmp",
		}))
	require.True(t, hasFinding(report.Findings, FindingDangerousCommand))
}

func TestCommandField_DefaultField(t *testing.T) {
	policy := DefaultSafetyPolicy()
	g := NewGuard(policy, WithClock(fixedClock))

	report := g.Scan(context.Background(), "unknown_tool",
		mustJSONArgs(t, map[string]any{
			"command": "rm -rf /tmp",
		}))
	require.True(t, hasFinding(report.Findings, FindingDangerousCommand))
}

// =====================================================================
// toPermissionDecision direct coverage
// =====================================================================

func TestToPermissionDecision_Allow(t *testing.T) {
	d := toPermissionDecision(DecisionAllow, ScanReport{})
	require.Equal(t, tool.PermissionActionAllow, d.Action)
}

func TestToPermissionDecision_DenyWithNoFindings(t *testing.T) {
	d := toPermissionDecision(DecisionDeny, ScanReport{})
	require.Equal(t, tool.PermissionActionDeny, d.Action)
	require.Equal(t, "tool call blocked by safety policy", d.Reason)
}

func TestToPermissionDecision_AskWithFindings(t *testing.T) {
	d := toPermissionDecision(DecisionAsk, ScanReport{
		Findings: []Finding{
			{Type: FindingParseError, Detail: "bad command"},
		},
	})
	require.Equal(t, tool.PermissionActionAsk, d.Action)
	require.Contains(t, d.Reason, "bad command")
}

// =====================================================================
// Malformed JSON arguments
// =====================================================================

func TestGuard_MalformedJSONArguments(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		[]byte("{not valid json"))
	require.True(t, hasFindingRule(report.Findings, "arguments_decode"))
}

// =====================================================================
// Empty arguments
// =====================================================================

func TestGuard_EmptyArguments(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec", nil)
	require.Equal(t, string(DecisionAllow), report.Decision)
	require.Equal(t, RiskLevelNone, report.RiskLevel)
}

// =====================================================================
// Resource abuse: max_output_bytes
// =====================================================================

func TestGuard_ResourceAbuse_MaxOutputBytes(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command":          "echo hi",
			"max_output_bytes": 99999999,
		}))
	require.True(t, hasFindingRule(report.Findings, "max_output_bytes"))
}

// =====================================================================
// Network URL in arguments (not command)
// =====================================================================

func TestGuard_NetworkURL_InArguments(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	report := g.Scan(context.Background(), "some_tool",
		mustJSONArgs(t, map[string]any{
			"url": "https://evil.example.com/data",
		}))
	require.True(t, hasFinding(report.Findings, FindingNetworkEgress))
}

// =====================================================================
// Network URL with empty allowlist (no allowed_domains)
// =====================================================================

func TestGuard_NetworkURL_EmptyAllowlist(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.Network.AllowedDomains = nil
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "some_tool",
		mustJSONArgs(t, map[string]any{
			"url": "https://example.com/data",
		}))
	require.True(t, hasFindingRule(report.Findings, "network_url"))
}

// =====================================================================
// Sensitive info: DenyOnDetect
// =====================================================================

func TestGuard_SensitiveInfo_DenyOnDetect(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.SensitiveInfo.DenyOnDetect = true
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo sk-abcdefghijklmnopqrstuvwxyz1234567890",
		}))
	require.True(t, hasFinding(report.Findings, FindingSensitiveInfo))
	require.True(t, atLeast(report.RiskLevel, RiskLevelHigh))
	require.Equal(t, string(DecisionDeny), report.Decision)
}

// =====================================================================
// Advisory mode with critical risk
// =====================================================================

func TestGuard_AdvisoryMode_CriticalDenied(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.Decision.Mode = DecisionModeAdvisory
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "sh -c 'echo hello'",
		}))
	require.Equal(t, string(DecisionDeny), report.Decision)
	require.True(t, hasFinding(report.Findings, FindingShellBypass))
}

// =====================================================================
// hostOf coverage
// =====================================================================

func TestHostOf_ValidURL(t *testing.T) {
	require.Equal(t, "example.com", hostOf("https://example.com/path"))
}

func TestHostWithPort(t *testing.T) {
	require.Equal(t, "localhost", hostOf("http://localhost:8080"))
}

func TestHostOf_InvalidURL(t *testing.T) {
	require.Equal(t, "", hostOf("not a url"))
}

func TestHostOf_NoHost(t *testing.T) {
	require.Equal(t, "", hostOf("/just/a/path"))
}

// =====================================================================
// expandPath coverage
// =====================================================================

func TestExpandPath_EmptyString(t *testing.T) {
	require.Nil(t, expandPath("", "/home/user"))
}

func TestExpandPath_NoTilde(t *testing.T) {
	out := expandPath("/etc/passwd", "/home/user")
	require.Equal(t, []string{"/etc/passwd"}, out)
}

func TestExpandPath_TildeWithHome(t *testing.T) {
	out := expandPath("~/.ssh", "/home/user")
	require.Len(t, out, 2)
	require.Equal(t, "~/.ssh", out[0])
	require.Contains(t, out[1], ".ssh")
}

func TestExpandPath_TildeNoHome(t *testing.T) {
	out := expandPath("~/.ssh", "")
	require.Len(t, out, 1)
	require.Equal(t, "~/.ssh", out[0])
}

// =====================================================================
// argvBase coverage
// =====================================================================

func TestArgvBase_SimpleName(t *testing.T) {
	require.Equal(t, "ls", argvBase("ls"))
}

func TestArgvBase_FullPath(t *testing.T) {
	require.Equal(t, "ls", argvBase("/usr/bin/ls"))
}

func TestArgvBase_WindowsPath(t *testing.T) {
	require.Equal(t, "ls", argvBase("C:\\Users\\bin\\ls"))
}

// =====================================================================
// toSet coverage
// =====================================================================

func TestToSet_Empty(t *testing.T) {
	s := toSet(nil)
	require.Len(t, s, 0)
}

func TestToSet_WithWhitespace(t *testing.T) {
	s := toSet([]string{"  Hello  ", "WORLD", ""})
	require.Contains(t, s, "hello")
	require.Contains(t, s, "world")
	require.NotContains(t, s, "")
}

// =====================================================================
// looksSensitive coverage
// =====================================================================

func TestLooksSensitive_APIKey(t *testing.T) {
	require.True(t, looksSensitive("api_key=abcdef123456"))
}

func TestLooksSensitive_JWT(t *testing.T) {
	require.True(t, looksSensitive("eyJabc.def.ghi"))
}

func TestLooksSensitive_PrivateKey(t *testing.T) {
	require.True(t, looksSensitive("-----begin private key-----"))
}

func TestLooksSensitive_Benign(t *testing.T) {
	require.False(t, looksSensitive("hello world"))
}

// =====================================================================
// riskOrder coverage for unknown level
// =====================================================================

func TestRiskOrder_UnknownLevel(t *testing.T) {
	require.Equal(t, 0, riskOrder(RiskLevel("bogus")))
}

// =====================================================================
// sortFindings stability
// =====================================================================

func TestSortFindings_ByRiskThenType(t *testing.T) {
	findings := []Finding{
		{Type: FindingEnvViolation, RiskLevel: RiskLevelLow},
		{Type: FindingDangerousCommand, RiskLevel: RiskLevelHigh},
		{Type: FindingShellBypass, RiskLevel: RiskLevelCritical},
		{Type: FindingForbiddenPath, RiskLevel: RiskLevelHigh},
	}
	sortFindings(findings)
	require.Equal(t, FindingShellBypass, findings[0].Type)
	require.Equal(t, RiskLevelCritical, findings[0].RiskLevel)
	// High findings sorted by type alphabetically.
	require.Equal(t, FindingDangerousCommand, findings[1].Type)
	require.Equal(t, FindingForbiddenPath, findings[2].Type)
	require.Equal(t, FindingEnvViolation, findings[3].Type)
}

// =====================================================================
// CheckToolPermission with audit writer and zero policy (no audit emitted)
// =====================================================================

func TestGuard_AuditNotEmitted_WhenPolicyInactive(t *testing.T) {
	var buf bytes.Buffer
	audit := NewAuditWriter(&buf)
	// Construct a Guard directly (bypassing NewGuard/withDefaults) so the
	// policy is truly inactive and emitAudit returns early.
	g := &Guard{
		policy: SafetyPolicy{},
		now:    fixedClock,
		audit:  audit,
	}
	report := g.scan(scanContext{
		policy:     g.policy,
		toolName:   "workspace_exec",
		command:    "rm -rf /",
		hasCommand: true,
	})
	g.emitAudit(report)
	require.Empty(t, buf.String())
}

// =====================================================================
// Scan with command field from custom tool name
// =====================================================================

func TestGuard_Scan_WithCustomToolField(t *testing.T) {
	policy := DefaultSafetyPolicy()
	policy.ToolCommandFields["custom_shell"] = "cmd"
	g := NewGuard(policy, WithClock(fixedClock))

	report := g.Scan(context.Background(), "custom_shell",
		mustJSONArgs(t, map[string]any{
			"cmd": "rm -rf /tmp",
		}))
	require.True(t, hasFinding(report.Findings, FindingDangerousCommand))
}

// =====================================================================
// Environment scan: env is not a map (type assertion fails)
// =====================================================================

func TestGuard_EnvironmentNotMap(t *testing.T) {
	policy := DefaultSafetyPolicy()
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo hi",
			"env":     "not-a-map",
		}))
	// Should not panic; no env violation findings.
	for _, f := range report.Findings {
		require.NotEqual(t, FindingEnvViolation, f.Type)
	}
}

// =====================================================================
// Environment scan: no env key present
// =====================================================================

func TestGuard_EnvironmentNotPresent(t *testing.T) {
	policy := DefaultSafetyPolicy()
	g := NewGuard(policy, WithClock(fixedClock))
	report := g.Scan(context.Background(), "workspace_exec",
		mustJSONArgs(t, map[string]any{
			"command": "echo hi",
		}))
	for _, f := range report.Findings {
		require.NotEqual(t, FindingEnvViolation, f.Type)
	}
}

// =====================================================================
// Scan with nil args but non-nil rawArgs (malformed JSON)
// =====================================================================

func TestGuard_ScanResources_NilArgs(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(fixedClock))
	// Non-active policy should skip scanResources.
	report := g.Scan(context.Background(), "workspace_exec",
		[]byte("not json"))
	// arguments_decode finding should fire.
	require.True(t, hasFindingRule(report.Findings, "arguments_decode"))
}

// =====================================================================
// WithClock nil is a no-op
// =====================================================================

func TestWithClock_Nil(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithClock(nil))
	require.NotNil(t, g.now)
}

// =====================================================================
// WithAuditWriter nil is a no-op
// =====================================================================

func TestWithAuditWriter_Nil(t *testing.T) {
	g := NewGuard(DefaultSafetyPolicy(), WithAuditWriter(nil))
	require.Nil(t, g.audit)
}
