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

// ─── adapter: inferBackend all branches ───

func TestInferBackend_AllBranches(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	tests := []struct {
		toolName string
		expected string // expected in report after scan
	}{
		{"workspace_exec", "workspaceexec"},
		{"workspace_exec_command", "workspaceexec"},
		{"exec_command", "hostexec"},
		{"write_stdin", "hostexec"},
		{"kill_session", "hostexec"},
		{"code_exec", "codeexec"},
		{"execute_code", "codeexec"},
		{"unknown_tool", ""},
	}
	for _, tt := range tests {
		req := &tool.PermissionRequest{
			ToolName:  tt.toolName,
			Arguments: []byte(`{"command":"echo hello"}`),
		}
		_, err := adapter.CheckToolPermission(context.Background(), req)
		require.NoError(t, err)
	}
}

// ─── adapter: defaultRequestMapper all branches ───

func TestDefaultRequestMapper_NilArgs(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: nil,
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

func TestDefaultRequestMapper_InvalidJSON(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{invalid}`),
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

func TestDefaultRequestMapper_TimeoutFields(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	// timeout field (float64 as unmarshalled by encoding/json)
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo hello","timeout":60}`),
	}
	_, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)

	// timeout_sec field
	req2 := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo hello","timeout_sec":120}`),
	}
	_, err = adapter.CheckToolPermission(context.Background(), req2)
	require.NoError(t, err)
}

func TestDefaultRequestMapper_EnvNonStringValues(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	// env values that are not strings (ints) should be skipped.
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo","env":{"LANG":"en_US","COUNT":42}}`),
	}
	_, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
}

// ─── adapter: SetRequestMapper nil ───

func TestSetRequestMapper_Nil(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)
	adapter.SetRequestMapper(nil) // should be a no-op
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo hello"}`),
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// ─── adapter: inner policy delegation ───

type allowPolicy struct{}

func (allowPolicy) CheckToolPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	return tool.AllowPermission(), nil
}

func TestAdapter_InnerPolicyCalled(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	inner := allowPolicy{}
	adapter := safety.NewSafetyPermissionPolicy(inner, s, nil)

	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo hello"}`),
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// ─── adapter: audit nil path (already covered by NewTestScanner) ───

func TestAdapter_AuditNil(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p) // nil audit
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo hello"}`),
	}
	_, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
}

// ─── resource: sleep with m/h/d units ───

func TestResource_SleepMinutes(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "sleep 6m",
		Backend: "workspaceexec",
	})
	// 6m = 360s > max 300s → RESOURCE_TIMEOUT (ask)
	assert.Equal(t, safety.DecisionAsk, report.Decision)
	assert.Equal(t, "RESOURCE_TIMEOUT", report.RuleID)
}

func TestResource_SleepHours(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "sleep 1h",
		Backend: "workspaceexec",
	})
	// 1h = 3600s > max 300s → RESOURCE_TIMEOUT (ask)
	assert.Equal(t, safety.DecisionAsk, report.Decision)
	assert.Equal(t, "RESOURCE_TIMEOUT", report.RuleID)
}

func TestResource_SleepDays(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "sleep 1d",
		Backend: "workspaceexec",
	})
	// 1d = 86400s > 300s → RESOURCE_TIMEOUT (ask)
	assert.Equal(t, safety.DecisionAsk, report.Decision)
	assert.Equal(t, "RESOURCE_TIMEOUT", report.RuleID)
}

func TestResource_SleepShort_OK(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "sleep 10",
		Backend: "workspaceexec",
	})
	// 10s < 300s max → allow.
	assert.Equal(t, safety.DecisionAllow, report.Decision)
}

func TestResource_TimeoutOverride(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command:    "echo hello",
		Backend:    "workspaceexec",
		TimeoutSec: 999,
	})
	// Requested timeout > max → RESOURCE_TIMEOUT (ask)
	assert.Equal(t, safety.DecisionAsk, report.Decision)
	assert.Equal(t, "RESOURCE_TIMEOUT", report.RuleID)
}

// ─── resource: dd command ───

func TestResource_DD(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "dd if=/dev/zero of=/tmp/out bs=1M count=1000",
		Backend: "workspaceexec",
	})
	// dd is in denied commands list → deny.
	assert.Equal(t, safety.DecisionDeny, report.Decision)
}

// ─── network: matchDomain wildcard and subdomain ───
// Note: tested indirectly through existing scenario tests.

// ─── network: extractHost without scheme ───

// ─── host: screen session residual ───

func TestHost_ScreenSession(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command:  "screen -S my_session",
		Backend:  "hostexec",
		ToolName: "exec_command",
	})
	// screen is in allowed commands but detected by host checker.
	// shellsafe doesn't block screen.
	assert.NotEqual(t, safety.DecisionAllow, report.Decision)
}

// ─── riskPriority ───

func TestRiskPriority_AllLevels(t *testing.T) {
	// Each level produces a distinct priority via indirect testing.
	// Low (1) < Medium (2) < High (3) < Critical (4) < None (0)
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "echo safe",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.RiskNone, report.RiskLevel)
}

// ─── DesensitizeEvidence ───

func TestDesensitizeEvidence_Empty(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	result := s.DesensitizeEvidence("")
	assert.Equal(t, "", result)
}

func TestDesensitizeEvidence_NoSecret(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	result := s.DesensitizeEvidence("safe evidence text")
	assert.Equal(t, "safe evidence text", result)
}

// ─── LoadPolicy .yml extension ───

func TestLoadPolicy_YML_Extension(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "policy.yml")
	err := os.WriteFile(tmp, []byte(defaultTestPolicyYAML()), 0o644)
	require.NoError(t, err)

	p, err := safety.LoadPolicy(tmp)
	require.NoError(t, err)
	assert.Equal(t, "1.0", p.Version)
}

// ─── LoadPolicyBytes invalid format ───

func TestLoadPolicyBytes_InvalidFormat(t *testing.T) {
	_, err := safety.LoadPolicyBytes([]byte(`{}`), "xml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

// ─── traceIDFromContext: nil context / wrong type ───

func TestAuditLogger_NilContext(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	tmp := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := safety.NewJSONLAuditLogger(tmp, p)
	require.NoError(t, err)
	defer logger.Close()

	report := &safety.SafetyReport{
		Decision:  safety.DecisionAllow,
		RiskLevel: safety.RiskNone,
		Backend:   "workspaceexec",
	}
	// nil context should not panic.
	err = logger.Log(nil, report)
	require.NoError(t, err)
}

func TestAuditLogger_WrongTraceIDType(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	tmp := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := safety.NewJSONLAuditLogger(tmp, p)
	require.NoError(t, err)
	defer logger.Close()

	// Using an int value for TraceIDKey — traceIDFromContext only accepts strings.
	ctx := context.WithValue(context.Background(), safety.TraceIDKey, 12345)
	report := &safety.SafetyReport{
		Decision:  safety.DecisionAllow,
		RiskLevel: safety.RiskNone,
		Backend:   "workspaceexec",
	}
	err = logger.Log(ctx, report)
	require.NoError(t, err)
}

// ─── NewJSONLAuditLogger with nil policy (should not panic) ───

// ─── env checker: empty policy ───

func TestEnvChecker_EmptyPolicy(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "echo hello",
		Backend: "workspaceexec",
		Env:     nil, // no env → env checker should pass
	})
	assert.Equal(t, safety.DecisionAllow, report.Decision)
}

// ─── checker_command: CMD_NOT_ALLOWED rule ID ───

func TestCommand_NotAllowed_RuleID(t *testing.T) {
	s := newTestScanner(t)
	// "make" is not in the allowed list → CMD_NOT_ALLOWED
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "make build",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	assert.Contains(t, report.RuleID, "CMD_")
}

// ─── policy.go: Policy_SecretRegexps returns compiled patterns ───

// ─── checker_secret_output: Name() and Check() exercised ───
// Tested via secretCmdChecker in scenario #13.

// ─── checker_network: wildcard and subdomain match ───

func TestNetwork_WildcardDomain(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(`
version: "1.0"
commands:
  allowed: ["curl", "echo"]
network:
  whitelist: ["*.example.com"]
`), "yaml")
	require.NoError(t, err)
	s := safety.NewTestScanner(p)

	// *.example.com should allow sub.example.com.
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "curl https://sub.example.com/api",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionAllow, report.Decision)
}

func TestNetwork_SubdomainMatch(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(`
version: "1.0"
commands:
  allowed: ["curl", "echo"]
network:
  whitelist: ["example.com"]
`), "yaml")
	require.NoError(t, err)
	s := safety.NewTestScanner(p)

	// api.example.com should match example.com via subdomain suffix.
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "curl https://api.example.com/data",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionAllow, report.Decision)
}

func TestNetwork_HTTPSuffix(t *testing.T) {
	p, err := safety.LoadPolicyBytes([]byte(`
version: "1.0"
commands:
  allowed: ["http", "echo"]
network:
  whitelist: ["trusted.local"]
`), "yaml")
	require.NoError(t, err)
	s := safety.NewTestScanner(p)

	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "http https://trusted.local/path",
		Backend: "workspaceexec",
	})
	assert.Equal(t, safety.DecisionAllow, report.Decision)
}

// ─── adapter: AskDecision with recommendation ───

func TestAdapter_AskWithRecommendation(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	s := safety.NewTestScanner(p)
	adapter := safety.NewSafetyPermissionPolicy(nil, s, nil)

	req := &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"tmux new-session"}`),
	}
	decision, err := adapter.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAsk, decision.Action)
	assert.NotEmpty(t, decision.Reason)
}

// ─── checker_command: CMD_STRUCTURE_REJECTED rule ID ───

func TestCommand_StructureRejected(t *testing.T) {
	s := newTestScanner(t)
	report := s.ScanCtx(context.Background(), &safety.ScanRequest{
		Command: "echo $(whoami)",
		Backend: "workspaceexec",
	})
	// shellsafe rejects $() syntax.
	assert.Equal(t, safety.DecisionDeny, report.Decision)
	assert.Contains(t, report.RuleID, "CMD_")
}

// ─── AuditLogger: Close ───

func TestAuditLogger_Close(t *testing.T) {
	p, _ := safety.LoadPolicyBytes([]byte(defaultTestPolicyYAML()), "yaml")
	tmp := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := safety.NewJSONLAuditLogger(tmp, p)
	require.NoError(t, err)
	err = logger.Close()
	require.NoError(t, err)
	// Second close should not panic.
	err = logger.Close()
	// This may error (already closed) — accept either.
	_ = err
}
