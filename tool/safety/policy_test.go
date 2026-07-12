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

func TestProgrammaticPartialPolicyKeepsDefaultGuardrails(t *testing.T) {
	p := Policy{
		AllowedCommands: []string{"rm"},
		DeniedCommands:  []string{},
	}
	normalized := p.Normalize()
	require.True(t, normalized.DenyDangerousRecursiveDelete)
	require.True(t, normalized.DenySecretLeakage)
	require.True(t, normalized.ReviewShellPipelines)
	require.True(t, normalized.RedactSensitiveEvidence)
}

func TestProgrammaticPolicyKeepsDefaultGuardrailsWhenOtherwiseComplete(t *testing.T) {
	p := Policy{
		AllowedCommands:                []string{"rm"},
		DeniedCommands:                 []string{},
		AllowedDomains:                 []string{"example.com"},
		DeniedPaths:                    []string{".env"},
		EnvAllowlist:                   []string{"TMPDIR"},
		MaxTimeoutSec:                  10,
		MaxOutputBytes:                 1024,
		LongSleepSeconds:               5,
		ParseErrorAction:               DecisionAsk,
		UnknownToolAction:              DecisionAllow,
		HostExecTTYAction:              DecisionAsk,
		BackgroundAction:               DecisionAsk,
		NonWhitelistedNetworkAction:    DecisionDeny,
		DependencyInstallAction:        DecisionAsk,
		ShellBypassAction:              DecisionAsk,
		DisallowedEnvironmentAction:    DecisionAsk,
		SensitivePathReadAction:        DecisionDeny,
		FailClosedOnUnsupportedBackend: true,
		AuditFailureMode:               AuditBestEffort,
	}

	normalized := p.Normalize()
	require.True(t, normalized.DenyDangerousRecursiveDelete)
	require.True(t, normalized.DenySecretLeakage)
	require.True(t, normalized.ReviewShellPipelines)
	require.True(t, normalized.RedactSensitiveEvidence)
}

func TestDefaultPolicyDerivedProgrammaticPolicyCanDisableGuardrails(t *testing.T) {
	p := DefaultPolicy()
	p.DenyDangerousRecursiveDelete = false
	p.DenySecretLeakage = false
	p.ReviewShellPipelines = false
	p.RedactSensitiveEvidence = false

	normalized := p.Normalize()
	require.False(t, normalized.DenyDangerousRecursiveDelete)
	require.False(t, normalized.DenySecretLeakage)
	require.False(t, normalized.ReviewShellPipelines)
	require.False(t, normalized.RedactSensitiveEvidence)
}

func TestPolicyConfigPreservesExplicitFalseAndZero(t *testing.T) {
	allowed := []string{"echo"}
	explicitFalse := false
	explicitZero := 0
	cfg := PolicyConfig{
		AllowedCommands:              &allowed,
		MaxTimeoutSec:                &explicitZero,
		MaxOutputBytes:               &explicitZero,
		LongSleepSeconds:             &explicitZero,
		DenyDangerousRecursiveDelete: &explicitFalse,
		DenySecretLeakage:            &explicitFalse,
		ReviewShellPipelines:         &explicitFalse,
		RedactSensitiveEvidence:      &explicitFalse,
	}

	p := PolicyFromConfig(cfg)
	allowed[0] = "rm"

	require.Equal(t, []string{"echo"}, p.AllowedCommands)
	require.Zero(t, p.MaxTimeoutSec)
	require.Zero(t, p.MaxOutputBytes)
	require.Zero(t, p.LongSleepSeconds)
	require.False(t, p.DenyDangerousRecursiveDelete)
	require.False(t, p.DenySecretLeakage)
	require.False(t, p.ReviewShellPipelines)
	require.False(t, p.RedactSensitiveEvidence)
}

func TestPolicyConfigPartialInheritsDefaultGuardrails(t *testing.T) {
	allowed := []string{"echo"}
	p := PolicyFromConfig(PolicyConfig{AllowedCommands: &allowed})

	require.Equal(t, []string{"echo"}, p.AllowedCommands)
	require.True(t, p.DenyDangerousRecursiveDelete)
	require.True(t, p.DenySecretLeakage)
	require.True(t, p.ReviewShellPipelines)
	require.True(t, p.RedactSensitiveEvidence)
	require.Equal(t, DefaultPolicy().MaxTimeoutSec, p.MaxTimeoutSec)
}

func TestLoadPolicyPreservesExplicitFalseGuardrails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
review_shell_pipelines: false
deny_dangerous_recursive_delete: false
deny_secret_leakage: false
redact_sensitive_evidence: false
`), 0o600))

	p, err := LoadPolicy(path)
	require.NoError(t, err)
	require.False(t, p.ReviewShellPipelines)
	require.False(t, p.DenyDangerousRecursiveDelete)
	require.False(t, p.DenySecretLeakage)
	require.False(t, p.RedactSensitiveEvidence)
}

func TestLoadPartialPolicyKeepsDefaultGuardrails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
allowed_commands: [echo]
allowed_domains: [example.com]
`), 0o600))

	p, err := LoadPolicy(path)
	require.NoError(t, err)
	require.True(t, p.ReviewShellPipelines)
	require.True(t, p.DenyDangerousRecursiveDelete)
	require.True(t, p.DenySecretLeakage)
	require.True(t, p.RedactSensitiveEvidence)
	require.Equal(t, DefaultPolicy().MaxTimeoutSec, p.MaxTimeoutSec)
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
