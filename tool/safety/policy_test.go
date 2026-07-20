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
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultPolicy_IsValidAndConservative(t *testing.T) {
	p := DefaultPolicy()
	require.NoError(t, p.Validate())
	require.Equal(t, DecisionDeny, p.DecisionThreshold.Critical)
	require.Equal(t, DecisionDeny, p.Rules.DangerousCommands.Action)
	require.Equal(t, DecisionAsk, p.Rules.Dependencies.Action)
	require.True(t, p.Rules.SecretLeak.Enabled)
}

func TestLoadPolicy_NormalizesDefaultsAndReviewAlias(t *testing.T) {
	policy, err := LoadPolicyFromBytes([]byte(`
version: 1
max_timeout: 30s
decision_threshold:
  critical: deny
  high: deny
  medium: needs_human_review
  low: allow
`))
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, policy.MaxTimeout)
	require.Equal(t, DecisionAsk, policy.DecisionThreshold.Medium)
	require.Equal(t, DecisionDeny, policy.DecisionThreshold.Critical)
}

func TestLoadPolicy_RejectsUnknownFields(t *testing.T) {
	_, err := LoadPolicyFromBytes([]byte(`
version: 1
bogus_field: 7
`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus_field")
}

func TestLoadPolicy_RejectsInvalidDomain(t *testing.T) {
	_, err := LoadPolicyFromBytes([]byte(`
version: 1
network:
  allowed_domains: ["*.example.com", "bad *.com"]
`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "domain")
}

func TestLoadPolicy_RejectsCriticalAllow(t *testing.T) {
	_, err := LoadPolicyFromBytes([]byte(`
version: 1
decision_threshold:
  critical: allow
`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "critical")
}

func TestLoadPolicy_FromJSONEquivalent(t *testing.T) {
	policy, err := LoadPolicyFromBytes([]byte(`{
"version": 1,
"max_timeout": "15s",
"max_output_size": 2048,
"rules": {
  "dependencies": {"enabled": true, "action": "ask"}
}
}`))
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, policy.MaxTimeout)
	require.Equal(t, int64(2048), policy.MaxOutputSize)
	require.Equal(t, DecisionAsk, policy.Rules.Dependencies.Action)
}

func TestLoadPolicy_TestdataFile(t *testing.T) {
	policy, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	require.NoError(t, err)
	require.Equal(t, 1, policy.Version)
	require.Equal(t, []string{
		"github.com", "api.github.com", "proxy.golang.org", "sum.golang.org",
		"pypi.org", "files.pythonhosted.org", "registry.npmjs.org",
	}, policy.Network.AllowedDomains)
	require.True(t, policy.Audit.Required)
}

// TestLoadPolicy_PreservesExplicitFalseOverrides locks in the
// unset-vs-explicit-false semantics of the pre-filled decoder: a boolean
// switch written as false in the policy file must override the
// DefaultPolicy true, while omitted fields and sibling rule families
// must keep their defaults. A regression here would silently re-enable
// a rule family or required audit the operator explicitly turned off.
func TestLoadPolicy_PreservesExplicitFalseOverrides(t *testing.T) {
	policy, err := LoadPolicyFromBytes([]byte(`
version: 1
rules:
  network: {enabled: false}
audit:
  required: false
  redact_secrets: false
`))
	require.NoError(t, err)

	// The explicit false overrides the DefaultPolicy true.
	require.False(t, policy.Rules.Network.Enabled)
	require.False(t, policy.Audit.Required)
	require.False(t, policy.Audit.RedactSecrets)
	// Omitted sibling fields and rule families keep their defaults.
	require.Equal(t, DecisionDeny, policy.Rules.Network.Action)
	require.True(t, policy.Rules.DangerousCommands.Enabled)
	require.True(t, policy.Rules.SecretLeak.Enabled)
	require.Equal(t, "tool_safety_audit.jsonl", policy.Audit.Path)

	// The disabled rule family must stop firing end to end.
	scanner := NewScanner(policy)
	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "curl https://unlisted.example/x",
	})
	require.NoError(t, err)
	require.NotContains(t, ruleIDsFromFindings(report.Findings),
		"network.non_whitelisted_domain")
}

func TestPolicyValidate_RejectsNegativeTimeout(t *testing.T) {
	p := DefaultPolicy()
	p.MaxTimeout = -1 * time.Second
	require.Error(t, p.Validate())
}

func TestPolicyValidate_RejectsDangerousAllow(t *testing.T) {
	p := DefaultPolicy()
	p.Rules.DangerousCommands.Action = DecisionAllow
	err := p.Validate()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "dangerous_commands"),
		"err=%v", err)
}

func TestCommandPolicyLists_CleansBlanks(t *testing.T) {
	p := DefaultPolicy()
	p.AllowedCommands = []string{"go", "  ", "", "git"}
	allow, deny := CommandPolicyLists(p)
	require.Equal(t, []string{"go", "git"}, allow)
	require.NotEmpty(t, deny)
}
