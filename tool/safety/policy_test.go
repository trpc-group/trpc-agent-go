// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestLoadPolicyRejectsUnknownField(t *testing.T) {
	path := writePolicy(t, "unknown_field: true\n")
	_, err := safety.LoadPolicy(path)
	require.ErrorContains(t, err, "unknown_field")
}

func TestLoadPolicyRejectsUnknownJSONField(t *testing.T) {
	path := writeJSONPolicy(t, `{"unknown_field":true}`)
	_, err := safety.LoadPolicy(path)
	require.ErrorContains(t, err, "unknown_field")
}

func TestLoadPolicyPreservesExplicitEmptyList(t *testing.T) {
	path := writePolicy(t, "denied_commands: []\n")
	policy, err := safety.LoadPolicy(path)
	require.NoError(t, err)
	require.Empty(t, policy.DeniedCommands)
}

func TestLoadPolicyRejectsTrailingDocument(t *testing.T) {
	path := writePolicy(t, "max_timeout_seconds: 30\n---\n{}\n")
	_, err := safety.LoadPolicy(path)
	require.Error(t, err)
}

func TestLoadPolicyRejectsTrailingJSONValue(t *testing.T) {
	path := writeJSONPolicy(t, `{"max_timeout_seconds":30} {}`)
	_, err := safety.LoadPolicy(path)
	require.Error(t, err)
}

func TestLoadPolicyOverlaysEveryField(t *testing.T) {
	path := writePolicy(t, `allowed_commands: [echo]
denied_commands: [danger]
denied_paths: [/private]
network_allowlist: [example.com]
env_allowlist: [PATH]
review_commands: [go install]
max_timeout_seconds: 30
max_output_bytes: 1024
parse_error_action: ask
pipeline_action: deny
`)
	policy, err := safety.LoadPolicy(path)
	require.NoError(t, err)
	require.Equal(t, []string{"echo"}, policy.AllowedCommands)
	require.Equal(t, []string{"danger"}, policy.DeniedCommands)
	require.Equal(t, []string{"/private"}, policy.DeniedPaths)
	require.Equal(t, []string{"example.com"}, policy.NetworkAllowlist)
	require.Equal(t, []string{"PATH"}, policy.EnvAllowlist)
	require.Equal(t, []string{"go install"}, policy.ReviewCommands)
	require.Equal(t, 30, policy.MaxTimeoutSeconds)
	require.Equal(t, int64(1024), policy.MaxOutputBytes)
	require.Equal(t, safety.DecisionAsk, policy.ParseErrorAction)
	require.Equal(t, safety.DecisionDeny, policy.PipelineAction)
}

func TestNewGuardAppliesConservativeDefaults(t *testing.T) {
	guard, err := safety.NewGuard(safety.Policy{})
	require.NoError(t, err)
	report := guard.Scan(safety.Request{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "sh -c 'echo unsafe'",
	})
	require.Equal(t, safety.DecisionDeny, report.Decision)
	require.NotEmpty(t, report.RuleID)
	require.NotEmpty(t, report.Evidence)
	require.NotEmpty(t, report.Recommendation)
}

func TestNewGuardCopiesPolicySlices(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.DeniedCommands = []string{"danger"}
	guard, err := safety.NewGuard(policy)
	require.NoError(t, err)
	policy.DeniedCommands[0] = "changed"
	report := guard.Scan(safety.Request{Command: "danger"})
	require.Equal(t, safety.DecisionDeny, report.Decision)
}

func writePolicy(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func writeJSONPolicy(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
