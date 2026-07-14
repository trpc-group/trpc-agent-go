//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGuardWithAuditFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	g, err := NewGuard(
		WithPolicyFile(filepath.Join("testdata", "tool_safety_policy.yaml")),
		WithAuditFile(path),
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	req := &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: []byte(`{"command":"rm -rf /"}`)}
	if _, err := g.CheckToolPermission(context.Background(), req); err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if len(data) == 0 {
		t.Errorf("audit file is empty")
	}
}

func TestWithAuditFileError(t *testing.T) {
	// Opening a directory for writing fails, exercising the error path.
	if _, err := NewGuard(WithAuditFile(t.TempDir())); err == nil {
		t.Errorf("WithAuditFile on a directory should error")
	}
}

func TestGuardCloseNoAudit(t *testing.T) {
	g, err := NewGuard()
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Errorf("Close without an audit writer: %v", err)
	}
}

func TestWriteReportJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteReportJSON(&buf, Report{ToolName: "x", Decision: DecisionAllow}); err != nil {
		t.Fatalf("WriteReportJSON: %v", err)
	}
	if !strings.Contains(buf.String(), `"tool_name": "x"`) {
		t.Errorf("unexpected JSON: %s", buf.String())
	}
}

func TestExtractCodeBlockShapes(t *testing.T) {
	cases := []struct{ name, args, want string }{
		{"array", `{"code_blocks":[{"code":"import os","language":"python"},{"code":"print(1)"}]}`, "import os\nprint(1)"},
		{"object", `{"code_blocks":{"code":"x=1"}}`, "x=1"},
		{"double_encoded", `{"code_blocks":"[{\"code\":\"y=2\"}]"}`, "y=2"},
		{"empty", `{}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			er, err := extract([]byte(tc.args), BackendCode)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if er.Command != tc.want {
				t.Errorf("command = %q, want %q", er.Command, tc.want)
			}
		})
	}
}

func TestExtractTimeoutAliasPrecedence(t *testing.T) {
	cases := []struct {
		args string
		want int
	}{
		{`{"command":"x","timeout_sec":10}`, 10},
		{`{"command":"x","timeoutSec":20}`, 20},
		{`{"command":"x","timeout":30}`, 30},
		{`{"command":"x"}`, 0},
	}
	for _, c := range cases {
		er, err := extract([]byte(c.args), BackendWorkspace)
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if er.TimeoutSec != c.want {
			t.Errorf("args %s -> timeout %d, want %d", c.args, er.TimeoutSec, c.want)
		}
	}
}

func TestParseSleep(t *testing.T) {
	cases := []struct {
		in  string
		sec int
		ok  bool
	}{
		{"30", 30, true}, {"5s", 5, true}, {"2m", 120, true}, {"1h", 3600, true},
		{"", 0, false}, {"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseSleep(c.in)
		if ok != c.ok || (ok && got != c.sec) {
			t.Errorf("parseSleep(%q) = %d,%v want %d,%v", c.in, got, ok, c.sec, c.ok)
		}
	}
}

func TestLoadPolicyJSONFile(t *testing.T) {
	p, err := LoadPolicy(filepath.Join("testdata", "tool_safety_policy.json"))
	if err != nil {
		t.Fatalf("LoadPolicy json: %v", err)
	}
	if p.backendFor("execute_code") != BackendCode {
		t.Errorf("json backend map not loaded")
	}
}

func TestLoadPolicyReadError(t *testing.T) {
	if _, err := LoadPolicy(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Errorf("expected read error for a missing file")
	}
}

func TestLoadPolicyParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("version: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadPolicy(path); err == nil {
		t.Errorf("expected parse error for malformed yaml")
	}
}

func TestRiskAndActionHelpers(t *testing.T) {
	riskActs := map[RiskLevel]Action{
		RiskCritical: ActionDeny, RiskHigh: ActionDeny,
		RiskMedium: ActionAsk, RiskLow: "", RiskNone: "",
	}
	for r, want := range riskActs {
		if got := riskToAction(r); got != want {
			t.Errorf("riskToAction(%q) = %q, want %q", r, got, want)
		}
	}
	if riskRank(RiskLow) == 0 || riskRank(RiskNone) != 0 {
		t.Errorf("riskRank low/none ranks wrong")
	}
	if actionRank(ActionAsk) != 1 || actionRank(ActionAllow) != 0 || actionRank(ActionDeny) != 2 {
		t.Errorf("actionRank mapping wrong")
	}
}

func TestArgsHavePrefix(t *testing.T) {
	if argsHavePrefix([]string{"install"}, nil) {
		t.Errorf("empty prefix must never match")
	}
	if !argsHavePrefix([]string{"-U", "install", "pkg"}, []string{"install"}) {
		t.Errorf("prefix should match after skipping leading flags")
	}
	if argsHavePrefix([]string{"list"}, []string{"install"}) {
		t.Errorf("non-matching prefix should not match")
	}
	// A leading option that consumes the next token as its value must not
	// hide the subcommand: "go -C /tmp install pkg".
	if !argsHavePrefix([]string{"-C", "/tmp", "install", "pkg"}, []string{"install"}) {
		t.Errorf("option value before the subcommand must not hide it")
	}
	// An inline "=" value never consumes the following token.
	if !argsHavePrefix([]string{"--dir=/tmp", "install"}, []string{"install"}) {
		t.Errorf("inline option value must not consume the next token")
	}
	// A non-option operand that does not match stops the scan: "go build
	// install" is not "go install".
	if argsHavePrefix([]string{"build", "install"}, []string{"install"}) {
		t.Errorf("an unmatched operand must stop the prefix scan")
	}
}

// TestGuardCodeBackend exercises the execute_code path end to end: a secret in
// the code is detected and redacted even though the shell-structure rules see no
// pipeline.
func TestGuardCodeBackend(t *testing.T) {
	var last Report
	g, err := NewGuard(
		WithPolicyFile(filepath.Join("testdata", "tool_safety_policy.yaml")),
		WithReportSink(func(r Report) { last = r }),
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	args := `{"code_blocks":[{"code":"token = \"` + fakeBearerToken() + `\"","language":"python"}]}`
	req := &tool.PermissionRequest{ToolName: "execute_code", Arguments: []byte(args)}
	if _, err := g.CheckToolPermission(context.Background(), req); err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if last.Backend != BackendCode {
		t.Errorf("backend = %q, want %q", last.Backend, BackendCode)
	}
	if !hasRule(last.Findings, ruleSecretID) || !last.Redacted {
		t.Errorf("expected redacted secret finding, got %+v", last)
	}
}
