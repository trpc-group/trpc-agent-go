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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadPolicyYAMLOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_safety_policy.yaml")
	err := os.WriteFile(path, []byte(`
allowed_commands: [echo]
denied_commands: [curl]
allowed_domains: [example.com]
max_timeout_sec: 5
parse_error_action: deny
`), 0o600)
	require.NoError(t, err)
	p, err := LoadPolicy(path)
	require.NoError(t, err)
	require.Equal(t, []string{"echo"}, p.AllowedCommands)
	require.Equal(t, []string{"curl"}, p.DeniedCommands)
	require.Equal(t, []string{"example.com"}, p.AllowedDomains)
	require.Equal(t, 5, p.MaxTimeoutSec)
	require.Equal(t, DecisionDeny, p.ParseErrorAction)
}

func TestLoadPolicyStrictRejectsUnknownFieldAndInvalidLimit(t *testing.T) {
	dir := t.TempDir()
	unknown := filepath.Join(dir, "unknown.yaml")
	require.NoError(t, os.WriteFile(unknown, []byte("unknown_field: true\n"), 0o600))
	_, err := LoadPolicyStrict(unknown)
	require.Error(t, err)

	invalid := filepath.Join(dir, "invalid.yaml")
	require.NoError(t, os.WriteFile(invalid, []byte("max_timeout_sec: -1\n"), 0o600))
	_, err = LoadPolicyStrict(invalid)
	require.Error(t, err)

	badAudit := filepath.Join(dir, "bad_audit.yaml")
	require.NoError(t, os.WriteFile(badAudit, []byte("audit_failure_mode: panic\n"), 0o600))
	_, err = LoadPolicyStrict(badAudit)
	require.Error(t, err)

	goodAudit := filepath.Join(dir, "good_audit.yaml")
	require.NoError(t, os.WriteFile(goodAudit, []byte("audit_failure_mode: fail_closed\n"), 0o600))
	p, err := LoadPolicyStrict(goodAudit)
	require.NoError(t, err)
	require.Equal(t, AuditFailClosed, p.AuditFailureMode)
}

func TestProductionPolicyUsesFailClosedDefaults(t *testing.T) {
	p := ProductionPolicy()
	require.Equal(t, DecisionAsk, p.UnknownToolAction)
	require.True(t, p.FailClosedOnUnsupportedBackend)
	require.Equal(t, AuditFailClosed, p.AuditFailureMode)
	require.True(t, p.RedactSensitivePaths)
}
