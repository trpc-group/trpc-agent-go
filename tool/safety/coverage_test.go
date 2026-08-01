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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPathHit_EnvVariants(t *testing.T) {
	t.Parallel()
	require.True(t, pathHit("cat .env.local", ".env"))
	require.True(t, pathHit("cat /app/.env.production", ".env"))
	require.True(t, pathHit("cat .env", ".env"))
	require.False(t, pathHit("cat .environment", ".env"))
	require.True(t, pathHit("cat /.SSH/id_rsa", "/.ssh"))
}

func TestCodeBlockTexts_Shapes(t *testing.T) {
	t.Parallel()
	got, err := codeBlockTexts(json.RawMessage(`[{"language":"python","code":"print(1)"}]`), 0)
	require.NoError(t, err)
	require.Equal(t, []string{"print(1)"}, got)
	got, err = codeBlockTexts(json.RawMessage(`{"language":"bash","code":"echo hi"}`), 0)
	require.NoError(t, err)
	require.Equal(t, []string{"echo hi"}, got)
	got, err = codeBlockTexts(json.RawMessage(`["rm -rf /"]`), 0)
	require.NoError(t, err)
	require.Equal(t, []string{"rm -rf /"}, got)
	got, err = codeBlockTexts(json.RawMessage(`"x=1"`), 0)
	require.NoError(t, err)
	require.Equal(t, []string{"x=1"}, got)
	raw, err := json.Marshal(`[{"code":"echo nested"}]`)
	require.NoError(t, err)
	got, err = codeBlockTexts(json.RawMessage(raw), 0)
	require.NoError(t, err)
	require.Equal(t, []string{"echo nested"}, got)
	got, err = codeBlockTexts(nil, 0)
	require.NoError(t, err)
	require.Nil(t, got)
	got, err = codeBlockTexts(json.RawMessage(`true`), 0)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestParseDurationToken_Units(t *testing.T) {
	t.Parallel()
	sec, ok := parseDurationToken("1h")
	require.True(t, ok)
	require.Equal(t, 3600, sec)
	sec, ok = parseDurationToken("2.5m")
	require.True(t, ok)
	require.Equal(t, 150, sec)
	_, ok = parseDurationToken("nope")
	require.False(t, ok)
}

func TestRiskRank(t *testing.T) {
	t.Parallel()
	require.Greater(t, riskRank(RiskCritical), riskRank(RiskHigh))
	require.Greater(t, riskRank(RiskHigh), riskRank(RiskMedium))
	require.Greater(t, riskRank(RiskMedium), riskRank(RiskLow))
	require.Equal(t, 0, riskRank(RiskLevel("")))
}

func TestHostOfAndNetworkFromText(t *testing.T) {
	t.Parallel()
	require.Equal(t, "evil.example", hostOf("https://evil.example/x"))
	require.Equal(t, "", hostOf("notaurl"))
	f, ok := scanNetworkFromText("please curl https://evil.example/x", nil)
	require.True(t, ok)
	require.Equal(t, DecisionDeny, f.Decision)
	f, ok = scanNetworkFromText("curl evil.example/x", []string{"api.github.com"})
	require.True(t, ok)
	require.Contains(t, f.Evidence, "evil.example")
}

func TestRedactSecrets_PEMAndKeyValue(t *testing.T) {
	t.Parallel()
	pem := "-----BEGIN PRIVATE KEY-----\nABC\n-----END PRIVATE KEY-----"
	out := redactSecrets(pem)
	require.Contains(t, out, "REDACTED")
	require.NotContains(t, out, "BEGIN PRIVATE KEY-----\nABC")
	out = redactSecrets("token=supersecretvalue123")
	require.Contains(t, out, "REDACTED")
	require.NotContains(t, out, "supersecretvalue123")
}

func TestScanEnvAllowlist(t *testing.T) {
	t.Parallel()
	ex := Extracted{Env: map[string]string{"LD_PRELOAD": "/tmp/x.so"}}
	f, ok := scanEnvAllowlist(ex, []string{"LANG", "TERM"})
	require.True(t, ok)
	require.Equal(t, "env.not_allowed", f.RuleID)
	_, ok = scanEnvAllowlist(ex, nil)
	require.False(t, ok)
	_, ok = scanEnvAllowlist(Extracted{Env: map[string]string{"lang": "C"}}, []string{"LANG"})
	require.False(t, ok)
}

func TestPolicyOverlayApply_AllFields(t *testing.T) {
	t.Parallel()
	trueVal := true
	n := 12
	bytes := 99
	cmds := []string{"echo"}
	deny := []string{"rm"}
	paths := []string{"/.ssh"}
	hosts := []string{"example.com"}
	envs := []string{"LANG"}
	ask := []string{"npm"}
	o := policyOverlay{
		AllowedCommands:     &cmds,
		DeniedCommands:      &deny,
		DeniedPaths:         &paths,
		AllowedHosts:        &hosts,
		AllowedEnvVars:      &envs,
		AskCommands:         &ask,
		MaxTimeoutSeconds:   &n,
		MaxOutputBytes:      &bytes,
		HostExecRequiresAsk: &trueVal,
	}
	p := o.apply(DefaultPolicy())
	require.Equal(t, cmds, p.AllowedCommands)
	require.Equal(t, deny, p.DeniedCommands)
	require.Equal(t, paths, p.DeniedPaths)
	require.Equal(t, hosts, p.AllowedHosts)
	require.Equal(t, envs, p.AllowedEnvVars)
	require.Equal(t, ask, p.AskCommands)
	require.Equal(t, 12, p.MaxTimeoutSeconds)
	require.Equal(t, 99, p.MaxOutputBytes)
	require.True(t, p.HostExecRequiresAsk)
}

func TestLoadAndParsePolicy_JSONAndMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"ask_commands":["cargo"]}`), 0o600))
	p, err := LoadPolicyFile(path)
	require.NoError(t, err)
	require.Contains(t, p.AskCommands, "cargo")
	require.Contains(t, p.DeniedCommands, "rm")

	p2, err := LoadPolicyFile(filepath.Join(dir, "missing.yaml"))
	require.Error(t, err)
	require.Contains(t, p2.DeniedCommands, "rm")

	_, err = ParsePolicy([]byte(`{"denied_command":["rm"]}`), "p.json")
	require.Error(t, err)

	p3, err := ParsePolicy([]byte(``), "empty.yaml")
	require.NoError(t, err)
	require.Contains(t, p3.DeniedCommands, "rm")
}

func TestWithPolicyFile_AndReportAudit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := filepath.Join(dir, "ok.yaml")
	require.NoError(t, os.WriteFile(good, []byte("allowed_hosts:\n  - api.github.com\n"), 0o600))
	g := NewGuard(WithPolicyFile(good))
	require.Contains(t, g.Policy().AllowedHosts, "api.github.com")
	require.Contains(t, g.Policy().DeniedCommands, "rm")

	bad := NewGuard(WithPolicyFile(filepath.Join(dir, "nope.yaml")))
	dec, err := bad.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo hi"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)

	var nilGuard *Guard
	require.Equal(t, DefaultPolicy().DeniedCommands, nilGuard.Policy().DeniedCommands)
	dec, err = nilGuard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "workspace_exec",
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)

	mem := NewMemoryAuditor()
	g2 := NewGuard(WithAuditor(mem))
	_, err = g2.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /tmp/x"}`),
	})
	require.NoError(t, err)
	require.NotEmpty(t, mem.Events())
	require.NotEmpty(t, g2.LastResults())

	report := filepath.Join(dir, "report.json")
	require.NoError(t, WriteReportJSON(report, g2.LastResults()))
	data, err := os.ReadFile(report)
	require.NoError(t, err)
	require.Contains(t, string(data), "decision")

	auditPath := filepath.Join(dir, "audit.jsonl")
	fa, err := NewFileAuditor(auditPath)
	require.NoError(t, err)
	require.NoError(t, fa.Append(AuditEvent{ToolName: "t", Decision: DecisionAllow, RuleID: "allow"}))
	_, err = NewFileAuditor("")
	require.Error(t, err)
}

func TestExtract_CodeBlocksAndEmptyNonExec(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]any{
		"code_blocks": []string{"pip install evil"},
		"cwd":         "/tmp",
	})
	require.NoError(t, err)
	ex, err := Extract(&tool.PermissionRequest{ToolName: "execute_code", Arguments: raw})
	require.NoError(t, err)
	require.Equal(t, BackendCode, ex.Backend)
	require.Contains(t, ex.CodeBlocks, "pip install evil")

	ex, err = Extract(&tool.PermissionRequest{ToolName: "search", Arguments: []byte(`{}`)})
	require.NoError(t, err)
	dec, err := NewGuard().CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "search",
		Arguments: []byte(`{}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, dec.Action)
	_ = ex

	_, err = Extract(&tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo","env":null}`),
	})
	require.NoError(t, err)
}

func TestScan_InstallInCodeBlocksAndSleepHour(t *testing.T) {
	t.Parallel()
	res := Scan(Extracted{
		Backend:    BackendCode,
		ToolName:   "execute_code",
		CodeBlocks: []string{"pip install evilpkg"},
		RawText:    "pip install evilpkg",
	}, DefaultPolicy())
	require.Equal(t, DecisionAsk, res.Decision)
	require.Contains(t, res.RuleID, "install")

	res = Scan(Extracted{
		Backend:  BackendWorkspace,
		ToolName: "workspace_exec",
		Command:  "sleep 1h",
		RawText:  "sleep 1h",
	}, DefaultPolicy())
	require.Equal(t, DecisionAsk, res.Decision)
	require.Contains(t, res.RuleID, "resource")
}

func TestEmitSpan_Recording(t *testing.T) {
	t.Parallel()
	g := NewGuard()
	ctx, span := trace.NewNoopTracerProvider().Tracer("t").Start(context.Background(), "perm")
	g.emitSpan(ctx, Result{
		Decision:  DecisionDeny,
		RiskLevel: RiskHigh,
		RuleID:    "test",
		Backend:   BackendWorkspace,
	})
	span.End()
}

func TestLooksLikeInstallAndSecretEvidence(t *testing.T) {
	t.Parallel()
	require.True(t, looksLikeInstall("please npm install lodash"))
	require.False(t, looksLikeInstall("echo hi"))
	require.True(t, containsSecretEvidence("sk-"+strings.Repeat("b", 32)))
}
