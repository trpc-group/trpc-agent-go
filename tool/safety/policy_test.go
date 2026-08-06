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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadPolicyChangesBehaviorWithoutCodeChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool_safety_policy.yaml")
	writePolicy := func(domain string) {
		t.Helper()
		content := `version: 1
allowed_commands: [curl]
denied_commands: [rm]
forbidden_paths: [~/.ssh, .env]
network:
  allowed_domains: [` + domain + `]
  deny_by_default: true
limits:
  max_timeout_seconds: 60
  max_output_bytes: 4096
  max_concurrency: 4
allowed_environment_variables: [PATH]
actions:
  unparsable: ask
  command_not_allowed: ask
  dependency_change: ask
  host_background: deny
  host_tty: ask
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	writePolicy("one.example")
	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	scanner, err := NewScanner(policy)
	require.NoError(t, err)
	report := scanner.Scan(context.Background(), Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "curl https://two.example/data",
	})
	require.Equal(t, DecisionDeny, report.Decision)

	writePolicy("two.example")
	policy, err = LoadPolicy(path)
	require.NoError(t, err)
	scanner, err = NewScanner(policy)
	require.NoError(t, err)
	report = scanner.Scan(context.Background(), Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "curl https://two.example/data",
	})
	require.Equal(t, DecisionAllow, report.Decision)
}

func TestLoadPolicyRejectsUnknownFieldsAndInvalidActions(t *testing.T) {
	tests := []string{
		"version: 1\nunknown_field: true\n",
		"version: 1\nactions:\n  unparsable: ignore\n",
		"version: 2\n",
		"version: 1\nlimits:\n  max_timeout_seconds: -1\n",
	}
	for _, content := range tests {
		path := filepath.Join(t.TempDir(), "policy.yaml")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		_, err := LoadPolicy(path)
		require.Error(t, err)
	}
}

func TestLoadPolicyDefaultsNetworkToDenyByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	content := `version: 1
allowed_commands: [curl]
network:
  allowed_domains: [example.com]
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	require.True(t, policy.Network.DenyByDefault)
	scanner, err := NewScanner(policy)
	require.NoError(t, err)
	report := scanner.Scan(context.Background(), Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "curl https://collector.invalid/data",
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, RuleNetworkDomain, report.RuleID)
}

func TestPolicyDefensiveCopiesCallerSlices(t *testing.T) {
	policy := testPolicy()
	scanner, err := NewScanner(policy)
	require.NoError(t, err)

	policy.AllowedCommands[0] = "rm"
	policy.Network.AllowedDomains[0] = "collector.invalid"

	report := scanner.Scan(context.Background(), Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "cat README.md",
	})
	require.Equal(t, DecisionAllow, report.Decision)
	report = scanner.Scan(context.Background(), Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "curl https://collector.invalid/data",
	})
	require.Equal(t, DecisionDeny, report.Decision)
}
