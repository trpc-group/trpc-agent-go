// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSampleCorpusDecisions(t *testing.T) {
	scanner := sampleScanner(t)
	expected := map[string]Decision{
		"safe_go_test":       DecisionAllow,
		"dangerous_delete":   DecisionDeny,
		"read_secret":        DecisionDeny,
		"network_denied":     DecisionDeny,
		"network_allowed":    DecisionAllow,
		"shell_wrapper":      DecisionDeny,
		"pipeline":           DecisionAsk,
		"dependency_install": DecisionAsk,
		"long_running":       DecisionAsk,
		"large_output":       DecisionDeny,
		"host_pty":           DecisionDeny,
		"ask_review":         DecisionAsk,
	}
	criticalRules := map[string]string{
		"dangerous_delete": RuleDangerousDelete,
		"read_secret":      RuleForbiddenPath,
		"network_denied":   RuleNetworkDeniedDomain,
	}
	for name, want := range expected {
		t.Run(name, func(t *testing.T) {
			req := loadSample(t, name)
			report, err := scanner.Scan(context.Background(), req)
			if err != nil {
				t.Fatalf("%s scan: %v", name, err)
			}
			if report.Decision != want {
				t.Fatalf("%s decision = %s, want %s (rules=%v)", name, report.Decision, want, report.RuleIDs)
			}
			if report.ToolName == "" || report.Backend == "" ||
				report.RiskLevel == "" || len(report.RuleIDs) == 0 ||
				report.Recommendation == "" {
				t.Fatalf("%s report missing required fields: %#v", name, report)
			}
			if rule := criticalRules[name]; rule != "" && !contains(report.RuleIDs, rule) {
				t.Fatalf("%s rules = %v, want %s", name, report.RuleIDs, rule)
			}
		})
	}
}

func TestPolicyChangeAffectsNetworkDecision(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = append(policy.AllowedCommands, "curl")
	policy.DeniedCommands = nil
	policy.AllowedNetworkDomains = []string{"proxy.example.test"}
	scanner, err := NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	req := ExecutionRequest{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "curl -q https://proxy.example.test/archive.tar.gz",
	}
	report, err := scanner.Scan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionAllow {
		t.Fatalf("whitelisted decision = %s, want allow: %#v", report.Decision, report)
	}
	policy.AllowedNetworkDomains = nil
	scanner, err = NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	report, err = scanner.Scan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionDeny {
		t.Fatalf("non-whitelisted decision = %s, want deny: %#v", report.Decision, report)
	}
}

func TestPermissionPolicyBlocksAndAudits(t *testing.T) {
	scanner := sampleScanner(t)
	var buf bytes.Buffer
	policy, err := NewPermissionPolicy(scanner, WithAuditWriter(NewJSONLWriter(&buf)))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "workspace_exec",
		Arguments: []byte(`{
			"command": "rm -rf /tmp/project",
			"cwd": "."
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("permission action = %s, want deny", decision.Action)
	}
	if !strings.Contains(decision.Reason, RuleDangerousDelete) {
		t.Fatalf("permission reason missing rule id: %s", decision.Reason)
	}
	if !strings.Contains(buf.String(), `"blocked":true`) ||
		!strings.Contains(buf.String(), RuleDangerousDelete) {
		t.Fatalf("audit event missing blocked/rule fields: %s", buf.String())
	}
}

func TestRedactionRemovesSecretsFromReport(t *testing.T) {
	scanner := sampleScanner(t)
	report, err := scanner.Scan(context.Background(), ExecutionRequest{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "echo token=super-secret-token",
		Env: map[string]string{
			"OPENAI_API_KEY": "sk-fake-secret-token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("super-secret-token")) ||
		bytes.Contains(data, []byte("sk-fake-secret-token")) {
		t.Fatalf("report leaked secret: %s", string(data))
	}
	if !report.Redacted {
		t.Fatalf("report.Redacted = false, want true")
	}
}

func BenchmarkScan500Commands(b *testing.B) {
	scanner := sampleScanner(b)
	lines := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		lines = append(lines, "echo safe")
	}
	req := ExecutionRequest{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Script:   strings.Join(lines, "\n"),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := scanner.Scan(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNewPermissionPolicyRejectsNilScanner(t *testing.T) {
	policy, err := NewPermissionPolicy(nil)
	if policy != nil {
		t.Fatalf("policy = %#v, want nil", policy)
	}
	if err == nil || err.Error() != "nil safety scanner" {
		t.Fatalf("error = %v, want nil safety scanner", err)
	}
}

func TestLoadPolicyVersionValidation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contents    string
		wantVersion string
		wantErr     string
	}{
		{name: "unsupported.yaml", contents: "version: '2'\n", wantErr: "2"},
		{name: "unsupported.json", contents: `{"version":"10"}`, wantErr: "10"},
		{name: "whitespace.yaml", contents: "version: '  '\n", wantVersion: "1"},
		{name: "whitespace.json", contents: `{"version":" \t "}`, wantVersion: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			policy, err := LoadPolicy(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), "unsupported policy version") ||
					!strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("LoadPolicy error = %v, want unsupported version %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if policy.Version != tc.wantVersion {
				t.Fatalf("version = %q, want %q", policy.Version, tc.wantVersion)
			}
		})
	}
}

func sampleScanner(tb testing.TB) *Scanner {
	tb.Helper()
	policy, err := LoadPolicy(filepath.Join("..", "..", "examples", "tool_safety_guard", "tool_safety_policy.yaml"))
	if err != nil {
		tb.Fatal(err)
	}
	policy.Audit.Enabled = false
	scanner, err := NewScanner(policy)
	if err != nil {
		tb.Fatal(err)
	}
	return scanner
}

func loadSample(t *testing.T, name string) ExecutionRequest {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "tool_safety_guard", "samples", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var req ExecutionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func contains(in []string, want string) bool {
	for _, v := range in {
		if v == want {
			return true
		}
	}
	return false
}
