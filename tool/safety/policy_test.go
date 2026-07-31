//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadPolicyYAMLAndJSON(t *testing.T) {
	for _, document := range []string{
		`schema_version: v1
policy_id: test
allowed_commands: [go, curl]
allowed_network_domains: [api.example.com, "*.github.com"]
max_timeout_seconds: 30
allowed_env_vars: [CI, LC_*]
tool_profiles:
  custom_exec:
    backend: host
    command_field: request.command
`,
		`{"schema_version":"v1","policy_id":"test","allowed_commands":["go"],"max_timeout_seconds":30}`,
	} {
		policy, err := LoadPolicy(strings.NewReader(document))
		require.NoError(t, err)
		require.Contains(t, policy.AllowedCommands, "go")
		require.Equal(t, 30, policy.MaxTimeoutSeconds)
	}
}

func TestLoadPolicyAllowsExplicitHostAndIPEntries(t *testing.T) {
	policy, err := LoadPolicy(strings.NewReader(
		"schema_version: v1\npolicy_id: test\nallowed_network_domains: [localhost, 127.0.0.1, '::1']\n",
	))
	require.NoError(t, err)
	require.Equal(t, []string{"localhost", "127.0.0.1", "::1"}, policy.AllowedNetworkDomains)
}

func TestLoadPolicyRejectsInvalidDocuments(t *testing.T) {
	tests := []string{
		"schema_version: v1\npolicy_id: test\nunknown_field: true\n",
		"schema_version: v1\npolicy_id: test\nmax_timeout_seconds: -1\n",
		"schema_version: v1\npolicy_id: test\nallowed_network_domains: [https://example.com]\n",
		"schema_version: v1\npolicy_id: test\ntool_profiles:\n  x:\n    backend: other\n",
		"schema_version: v1\npolicy_id: test\nallowed_commands: [go]\n---\nallowed_commands: [curl]\n",
		"policy_id: test\n",
		"schema_version: v1\n",
		"schema_version: '   '\npolicy_id: test\n",
		"schema_version: v1\npolicy_id: '   '\n",
		"schema_version: v2\npolicy_id: test\n",
		"schema_version: v1\npolicy_id: 'not valid'\n",
	}
	for _, document := range tests {
		_, err := LoadPolicy(strings.NewReader(document))
		require.Error(t, err, document)
	}
}

func TestPolicyIdentityAndRevision(t *testing.T) {
	policy := Policy{PolicyID: "production", AllowedCommands: []string{"go"}}
	first, err := NewScanner(policy)
	require.NoError(t, err)
	second, err := NewScanner(policy)
	require.NoError(t, err)
	changed, err := NewScanner(Policy{
		PolicyID:        "production",
		AllowedCommands: []string{"go", "curl"},
	})
	require.NoError(t, err)

	input := ScanInput{ToolName: "workspace_exec", Command: "go test ./..."}
	firstReport, err := first.Scan(context.Background(), input)
	require.NoError(t, err)
	secondReport, err := second.Scan(context.Background(), input)
	require.NoError(t, err)
	changedReport, err := changed.Scan(context.Background(), input)
	require.NoError(t, err)

	require.Equal(t, currentSchemaVersion, firstReport.SchemaVersion)
	require.Equal(t, "production", firstReport.PolicyID)
	require.Len(t, firstReport.PolicyRevision, 64)
	require.Equal(t, firstReport.PolicyRevision, secondReport.PolicyRevision)
	require.NotEqual(t, firstReport.PolicyRevision, changedReport.PolicyRevision)
}

func TestNewScannerValidatesLiteralPolicy(t *testing.T) {
	_, err := NewScanner(Policy{MaxOutputBytes: -1})
	require.Error(t, err)
}

func TestForbiddenPathMatchesRawExpandedAndCWD(t *testing.T) {
	scanner := newTestScanner(t)
	for _, input := range []ScanInput{
		{ToolName: "x", Backend: BackendGeneric, Command: "cat ~/.ssh/config"},
		{ToolName: "x", Backend: BackendGeneric, Command: "cat .env", WorkingDirectory: "/work"},
		{ToolName: "x", Backend: BackendGeneric, Command: "cat /etc/passwd"},
	} {
		report, err := scanner.Scan(context.Background(), input)
		require.NoError(t, err)
		require.Equal(t, RuleForbiddenPath, report.RuleID)
	}
}
