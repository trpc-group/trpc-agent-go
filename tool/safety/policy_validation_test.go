//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNormalizePolicyAppliesDefaultsAndCleansLists(t *testing.T) {
	got, err := normalizeAndValidatePolicy(Policy{})
	require.NoError(t, err)
	defaults := DefaultPolicy()
	require.Equal(t, defaults.Version, got.Version)
	require.Equal(t, defaults.PolicyID, got.PolicyID)
	require.Equal(t, defaults.Limits, got.Limits)
	require.Equal(t, defaults.HostExec, got.HostExec)
	require.Equal(t, defaults.Actions, got.Actions)
	require.ElementsMatch(t, defaults.Paths.Denied, got.Paths.Denied)
	require.ElementsMatch(t, defaults.Network.Commands, got.Network.Commands)
	require.ElementsMatch(t, defaults.Environment.DeniedVariables, got.Environment.DeniedVariables)
	require.Empty(t, got.Commands.Allowed)

	policy := DefaultPolicy()
	policy.PolicyID = "  normalized-policy  "
	policy.Commands.Allowed = []string{" Zed ", "go", "GO", ""}
	policy.Commands.Denied = []string{" rm ", "RM"}
	policy.Commands.Review = []string{" npm ", ""}
	policy.Paths.Denied = []string{" .env ", ".ENV"}
	policy.Network.Commands = []string{" curl ", "CURL"}
	policy.Network.AllowedDomains = []string{" example.com ", "*.go.dev", "EXAMPLE.COM"}
	policy.Environment.AllowedVariables = []string{" CI ", "ci"}
	policy.Environment.DeniedVariables = []string{" PATH ", "path"}

	got, err = normalizeAndValidatePolicy(policy)
	require.NoError(t, err)
	require.Equal(t, "normalized-policy", got.PolicyID)
	require.Equal(t, []string{"go", "Zed"}, got.Commands.Allowed)
	require.Equal(t, []string{"rm"}, got.Commands.Denied)
	require.Equal(t, []string{"npm"}, got.Commands.Review)
	require.Equal(t, []string{".env"}, got.Paths.Denied)
	require.Equal(t, []string{"curl"}, got.Network.Commands)
	require.Equal(t, []string{"*.go.dev", "example.com"}, got.Network.AllowedDomains)
	require.Equal(t, []string{"CI"}, got.Environment.AllowedVariables)
	require.Equal(t, []string{"PATH"}, got.Environment.DeniedVariables)

	encoded, err := json.Marshal(got.Actions)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"unparsable"`)
}

func TestNormalizePolicyRejectsInvalidLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Policy)
	}{
		{name: "timeout", mutate: func(p *Policy) { p.Limits.MaxTimeoutSeconds = -1 }},
		{name: "output", mutate: func(p *Policy) { p.Limits.MaxOutputBytes = -1 }},
		{name: "command bytes", mutate: func(p *Policy) { p.Limits.MaxCommandBytes = -1 }},
		{name: "script bytes", mutate: func(p *Policy) { p.Limits.MaxScriptBytes = -1 }},
		{name: "session input bytes", mutate: func(p *Policy) {
			p.Limits.MaxSessionInputBytes = -1
		}},
		{name: "script lines", mutate: func(p *Policy) { p.Limits.MaxScriptLines = -1 }},
		{name: "sleep", mutate: func(p *Policy) { p.Limits.MaxSleepSeconds = -1 }},
		{name: "host timeout", mutate: func(p *Policy) { p.HostExec.MaxTimeoutSeconds = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPolicy()
			test.mutate(&policy)
			_, err := normalizeAndValidatePolicy(policy)
			require.ErrorContains(t, err, "limits must be positive")
		})
	}
}

func TestNormalizePolicyRejectsInvalidActions(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		mutate    func(*Policy)
	}{
		{
			name: "network", fieldName: "network.default_action",
			mutate: func(p *Policy) { p.Network.DefaultAction = "invalid" },
		},
		{
			name: "unparsable", fieldName: "actions.unparsable",
			mutate: func(p *Policy) { p.Actions.Unparsable = "invalid" },
		},
		{
			name: "unlisted", fieldName: "actions.unlisted_command",
			mutate: func(p *Policy) { p.Actions.UnlistedCommand = "invalid" },
		},
		{
			name: "unknown script", fieldName: "actions.unknown_script",
			mutate: func(p *Policy) { p.Actions.UnknownScript = "invalid" },
		},
		{
			name: "dependency", fieldName: "actions.dependency_change",
			mutate: func(p *Policy) { p.Actions.DependencyChange = "invalid" },
		},
		{
			name: "audit", fieldName: "actions.audit_failure",
			mutate: func(p *Policy) { p.Actions.AuditFailure = "invalid" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPolicy()
			test.mutate(&policy)
			_, err := normalizeAndValidatePolicy(policy)
			require.ErrorContains(t, err, test.fieldName)
			require.ErrorContains(t, err, "invalid permission action")
		})
	}
}

func TestNormalizePolicyRejectsFailOpenActions(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		mutate    func(*Policy)
	}{
		{
			name: "network", fieldName: "network.default_action",
			mutate: func(p *Policy) { p.Network.DefaultAction = tool.PermissionActionAllow },
		},
		{
			name: "unparsable", fieldName: "actions.unparsable",
			mutate: func(p *Policy) { p.Actions.Unparsable = tool.PermissionActionAllow },
		},
		{
			name: "unknown script", fieldName: "actions.unknown_script",
			mutate: func(p *Policy) { p.Actions.UnknownScript = tool.PermissionActionAllow },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPolicy()
			test.mutate(&policy)
			_, err := normalizeAndValidatePolicy(policy)
			require.ErrorContains(t, err, test.fieldName)
			require.ErrorContains(t, err, "must be deny or ask")
		})
	}
}

func TestNormalizePolicyRejectsVersionAndDomainPatterns(t *testing.T) {
	policy := DefaultPolicy()
	policy.Version = "v2"
	_, err := normalizeAndValidatePolicy(policy)
	require.ErrorContains(t, err, "unsupported tool safety policy version")

	invalidDomains := []string{
		"https://example.com",
		"example.com/path",
		`example.com\path`,
		"user@example.com",
		"example .com",
		"*.",
		".example.com",
		"example.com.",
	}
	for _, domain := range invalidDomains {
		t.Run(domain, func(t *testing.T) {
			policy := DefaultPolicy()
			policy.Network.AllowedDomains = []string{domain}
			_, err := normalizeAndValidatePolicy(policy)
			require.Error(t, err)
		})
	}

	policy = DefaultPolicy()
	policy.Network.AllowedDomains = []string{"*.example.com"}
	_, err = normalizeAndValidatePolicy(policy)
	require.NoError(t, err)
}

func TestLoadPolicyFileStrictFormats(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		ext     string
		content string
	}{
		{name: "empty YAML", ext: ".yaml", content: ""},
		{name: "malformed JSON", ext: ".json", content: "{"},
		{name: "unknown JSON field", ext: ".json", content: `{"unknown":true}`},
		{name: "trailing JSON", ext: ".json", content: `{"version":"v1"} {"version":"v1"}`},
		{name: "malformed YAML", ext: ".yaml", content: "version: ["},
		{name: "unknown YAML field", ext: ".yaml", content: "unknown: true\n"},
		{name: "multiple YAML documents", ext: ".yaml", content: "version: v1\n---\nversion: v1\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(test.name, " ", "_")+test.ext)
			require.NoError(t, os.WriteFile(path, []byte(test.content), 0o600))
			_, err := LoadPolicyFile(path)
			require.Error(t, err)
		})
	}

	_, err := LoadPolicyFile(filepath.Join(dir, "missing.yaml"))
	require.ErrorContains(t, err, "read tool safety policy")

	jsonInYAMLPath := filepath.Join(dir, "policy.conf")
	require.NoError(t, os.WriteFile(jsonInYAMLPath, []byte(`{
		"version":"v1",
		"policy_id":"json-detected",
		"actions":{"unparsable":"deny"}
	}`), 0o600))
	policy, err := LoadPolicyFile(jsonInYAMLPath)
	require.NoError(t, err)
	require.Equal(t, "json-detected", policy.PolicyID)
	require.Equal(t, tool.PermissionActionDeny, policy.Actions.Unparsable)

	require.Zero(t, firstNonSpace(nil))
	require.Zero(t, firstNonSpace([]byte(" \t\r\n")))
	require.Equal(t, byte('{'), firstNonSpace([]byte(" \n {")))
}
