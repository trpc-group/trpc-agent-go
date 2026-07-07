//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadPolicyYAML_StrictAndDefaults(t *testing.T) {
	policy, err := LoadPolicyYAML(strings.NewReader(`
allowed_commands:
  - go
network_allowlist:
  - proxy.golang.org
dependency_install_action: deny
`))
	require.NoError(t, err)
	require.Equal(t, []string{"go"}, policy.AllowedCommands)
	require.Equal(t, []string{"proxy.golang.org"}, policy.NetworkAllowlist)
	require.Equal(t, DecisionDeny, policy.DependencyInstallAction)
	require.Equal(t, AuditFailureModeBestEffort, policy.AuditFailureMode)
	require.NotEmpty(t, policy.DeniedPaths)
}

func TestLoadPolicyYAML_RejectsUnknownFields(t *testing.T) {
	_, err := LoadPolicyYAML(strings.NewReader("network_allow_list: []\n"))
	require.Error(t, err)
}

func TestLoadPolicyJSON_RejectsInvalidDecision(t *testing.T) {
	_, err := LoadPolicyJSON(strings.NewReader(`{"secret_action":"block"}`))
	require.ErrorContains(t, err, "invalid decision")
}

func TestLoadPolicyFile_DispatchesByExtension(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "policy.json")
	yamlPath := filepath.Join(dir, "policy.yaml")
	ymlPath := filepath.Join(dir, "policy.yml")
	txtPath := filepath.Join(dir, "policy.txt")

	require.NoError(t, os.WriteFile(jsonPath, []byte(`{"allowed_commands":["go"]}`), 0o644))
	require.NoError(t, os.WriteFile(yamlPath, []byte("allowed_commands:\n  - go\n"), 0o644))
	require.NoError(t, os.WriteFile(ymlPath, []byte("allowed_commands:\n  - go\n"), 0o644))
	require.NoError(t, os.WriteFile(txtPath, []byte("allowed_commands:\n  - go\n"), 0o644))

	jsonPolicy, err := LoadPolicyFile(jsonPath)
	require.NoError(t, err)
	require.Equal(t, []string{"go"}, jsonPolicy.AllowedCommands)

	yamlPolicy, err := LoadPolicyFile(yamlPath)
	require.NoError(t, err)
	require.Equal(t, []string{"go"}, yamlPolicy.AllowedCommands)

	ymlPolicy, err := LoadPolicyFile(ymlPath)
	require.NoError(t, err)
	require.Equal(t, []string{"go"}, ymlPolicy.AllowedCommands)

	_, err = LoadPolicyFile(txtPath)
	require.ErrorContains(t, err, "unsupported policy file extension")
}

func TestPolicyValidate_RejectsNegativeBounds(t *testing.T) {
	fields := []struct {
		name   string
		policy Policy
		err    string
	}{
		{
			name: "max_timeout_sec",
			policy: Policy{
				MaxTimeoutSec: -1,
			},
			err: "max_timeout_sec",
		},
		{
			name: "max_output_bytes",
			policy: Policy{
				MaxOutputBytes: -1,
			},
			err: "max_output_bytes",
		},
		{
			name: "max_command_bytes",
			policy: Policy{
				MaxCommandBytes: -1,
			},
			err: "max_command_bytes",
		},
		{
			name: "max_script_bytes",
			policy: Policy{
				MaxScriptBytes: -1,
			},
			err: "max_script_bytes",
		},
	}
	for _, tc := range fields {
		t.Run(tc.name, func(t *testing.T) {
			policy := tc.policy.WithDefaults()
			err := policy.Validate()
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func TestPolicyWithDefaults_PreservesDefaultDeniesUnlessExplicitlyDisabled(t *testing.T) {
	policy, err := LoadPolicyYAML(strings.NewReader(`
denied_commands: []
denied_paths: []
`))
	require.NoError(t, err)
	require.NotEmpty(t, policy.DeniedCommands)
	require.NotEmpty(t, policy.DeniedPaths)

	disabled, err := LoadPolicyYAML(strings.NewReader(`
disable_default_denies: true
denied_commands: []
denied_paths: []
`))
	require.NoError(t, err)
	require.True(t, disabled.DisableDefaultDenies)
	require.Empty(t, disabled.DeniedCommands)
	require.Empty(t, disabled.DeniedPaths)
}

func TestPolicyValidate_RejectsEmptyDeniesWithoutOptOut(t *testing.T) {
	err := Policy{
		DependencyInstallAction: DecisionAsk,
		UnparsableShellAction:   DecisionAsk,
		HostUnparsableAction:    DecisionDeny,
		SecretAction:            DecisionDeny,
		AuditFailureMode:        AuditFailureModeBestEffort,
	}.Validate()
	require.ErrorContains(t, err, "denied_commands cannot be empty")
}
