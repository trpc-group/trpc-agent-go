//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
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
