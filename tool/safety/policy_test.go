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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type invalidPolicyCase struct {
	name    string
	file    string
	content string
}

func TestDefaultPolicy_ReturnsIndependentCopies(t *testing.T) {
	first := DefaultPolicy()
	second := DefaultPolicy()
	require.NotEmpty(t, first.allowedCommands)
	first.allowedCommands[0] = "changed"
	first.deniedPaths[0] = "changed"
	require.NotEqual(t, first.allowedCommands[0], second.allowedCommands[0])
	require.NotEqual(t, first.deniedPaths[0], second.deniedPaths[0])
}

func TestLoadPolicy_YAMLAndJSONAreEquivalent(t *testing.T) {
	yamlPath := writePolicyFile(t, "policy.yaml", `
version: 1
commands:
  allowed: [go, date]
  denied: [rm]
paths:
  denied: [/root]
network:
  allowed_domains: [api.github.com, "*.trusted.example"]
limits:
  max_timeout: 20s
  max_output_bytes: 2048
  max_sleep: 2s
  max_concurrency: 3
environment:
  allowed: [LANG]
actions:
  parse_error: deny
  unknown_language: needs_human_review
  pipeline: ask
  dependency_install: ask
  host_pty: ask
  host_background: deny
`)
	jsonPath := writePolicyFile(t, "policy.json", `{
  "version": 1,
  "commands": {"allowed": ["go", "date"], "denied": ["rm"]},
  "paths": {"denied": ["/root"]},
  "network": {"allowed_domains": ["api.github.com", "*.trusted.example"]},
  "limits": {
    "max_timeout": "20s",
    "max_output_bytes": 2048,
    "max_sleep": "2s",
    "max_concurrency": 3
  },
  "environment": {"allowed": ["LANG"]},
  "actions": {
    "parse_error": "deny",
    "unknown_language": "needs_human_review",
    "pipeline": "ask",
    "dependency_install": "ask",
    "host_pty": "ask",
    "host_background": "deny"
  }
}`)

	want, err := LoadPolicy(yamlPath)
	require.NoError(t, err)
	got, err := LoadPolicy(jsonPath)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, 20*time.Second, got.maxTimeout)
	require.Equal(t, int64(2048), got.maxOutputBytes)
	require.Equal(t, []string{"api.github.com", "*.trusted.example"}, got.allowedDomains)
}

func TestLoadPolicy_ExplicitEmptyLists(t *testing.T) {
	path := writePolicyFile(t, "policy.yaml", `
version: 1
commands:
  allowed: []
  denied: []
paths:
  denied: []
network:
  allowed_domains: []
environment:
  allowed: []
`)
	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	require.True(t, policy.denyAllCommands)
	require.Empty(t, policy.allowedCommands)
	require.Empty(t, policy.deniedCommands)
	require.Empty(t, policy.deniedPaths)
	require.Empty(t, policy.allowedDomains)
	require.Empty(t, policy.allowedEnv)
	require.Equal(t, 30*time.Second, policy.maxTimeout)
}

func TestLoadPolicy_OmittedListsUseDefaults(t *testing.T) {
	path := writePolicyFile(t, "policy.json", `{"version": 1}`)
	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	require.Equal(t, defaultAllowedCommands, policy.allowedCommands)
	require.Equal(t, defaultDeniedCommands, policy.deniedCommands)
	require.Equal(t, defaultDeniedPaths, policy.deniedPaths)
	require.Equal(t, defaultAllowedEnv, policy.allowedEnv)
	require.Empty(t, policy.allowedDomains)
}

func TestLoadPolicy_StrictDecode(t *testing.T) {
	tests := append(strictYAMLPolicyCases(), strictJSONPolicyCases()...)
	requirePolicyLoadErrors(t, tests)
}

func TestLoadPolicy_Validation(t *testing.T) {
	tests := append(validationPolicyCases(), validationDomainCases()...)
	requirePolicyLoadErrors(t, tests)
}

func strictYAMLPolicyCases() []invalidPolicyCase {
	return []invalidPolicyCase{
		invalidPolicy("yaml unknown field", "policy.yaml", "version: 1\nunknown: true\n"),
		invalidPolicy("yaml duplicate key", "policy.yaml", "version: 1\nversion: 1\n"),
		invalidPolicy("yaml trailing document", "policy.yaml", "version: 1\n---\nversion: 1\n"),
		invalidPolicy("yaml null", "policy.yaml", "version: 1\ncommands: null\n"),
		invalidPolicy("yaml fractional version", "policy.yaml", "version: 1.5\n"),
		invalidPolicy("yaml boolean string list item", "policy.yaml", "version: 1\ncommands:\n  allowed: [true]\n"),
		invalidPolicy("yaml fractional integer limit", "policy.yaml", "version: 1\nlimits:\n  max_output_bytes: 1.5\n"),
		invalidPolicy("yaml octal-looking integer limit", "policy.yaml", "version: 1\nlimits:\n  max_output_bytes: 010\n"),
		invalidPolicy("yaml negative octal-looking integer limit", "policy.yaml", "version: 1\nlimits:\n  max_output_bytes: -010\n"),
	}
}

func strictJSONPolicyCases() []invalidPolicyCase {
	return []invalidPolicyCase{
		invalidJSONPolicy("json unknown field", `{"version":1,"unknown":true}`),
		invalidJSONPolicy("json duplicate key", `{"version":1,"version":1}`),
		invalidJSONPolicy("json nested duplicate key", `{"version":1,"commands":{"allowed":[],"allowed":[]}}`),
		invalidJSONPolicy("json trailing value", `{"version":1} {"version":1}`),
		invalidJSONPolicy("json null", `{"version":1,"commands":null}`),
		invalidJSONPolicy("json case variant root field", `{"version":1,"Version":2}`),
		invalidJSONPolicy("json case variant nested field", `{"version":1,"commands":{"allowed":["go"],"Allowed":["date"]}}`),
	}
}

func validationPolicyCases() []invalidPolicyCase {
	return []invalidPolicyCase{
		invalidJSONPolicy("missing version", `{}`),
		invalidJSONPolicy("unsupported version", `{"version":2}`),
		invalidJSONPolicy("duplicate list item", `{"version":1,"commands":{"allowed":["go","go"]}}`),
		invalidJSONPolicy("allow deny conflict", `{"version":1,"commands":{"allowed":["go"],"denied":["GO"]}}`),
		invalidJSONPolicy("empty list item", `{"version":1,"paths":{"denied":[" "]}}`),
		invalidJSONPolicy("basename allow deny conflict", `{"version":1,"commands":{"allowed":["/usr/bin/curl"],"denied":["curl"]}}`),
		invalidJSONPolicy("invalid env key", `{"version":1,"environment":{"allowed":["BAD-KEY"]}}`),
		invalidJSONPolicy("invalid duration", `{"version":1,"limits":{"max_timeout":"soon"}}`),
		invalidJSONPolicy("zero output limit", `{"version":1,"limits":{"max_output_bytes":0}}`),
		invalidJSONPolicy("negative concurrency", `{"version":1,"limits":{"max_concurrency":-1}}`),
		invalidJSONPolicy("allow action", `{"version":1,"actions":{"pipeline":"allow"}}`),
		invalidJSONPolicy("unknown action", `{"version":1,"actions":{"pipeline":"review"}}`),
	}
}

func validationDomainCases() []invalidPolicyCase {
	return []invalidPolicyCase{
		invalidDomainPolicy("invalid domain", "https://example.com"),
		invalidDomainPolicy("IP literal domain", "127.0.0.1"),
		invalidDomainPolicy("wildcard IP literal domain", "*.127.0.0.1"),
		invalidDomainPolicy("legacy shortened IP literal domain", "127.1"),
		invalidDomainPolicy("legacy padded IP literal domain", "127.000.000.001"),
		invalidDomainPolicy("legacy hexadecimal IP literal domain", "0x7f.0.0.1"),
		invalidDomainPolicy("legacy mixed hexadecimal final component", "127.0.0.0x1"),
		invalidDomainPolicy("legacy all hexadecimal components", "0x7f.0x0.0x0.0x1"),
		invalidDomainPolicy("legacy padded decimal and hexadecimal components", "09.0.0.0x1"),
		invalidDomainPolicy("wildcard legacy IP literal domain", "*.127.1"),
		invalidDomainPolicy("wildcard mixed hexadecimal IP literal domain", "*.127.0.0.0x1"),
	}
}

func invalidPolicy(name, file, content string) invalidPolicyCase {
	return invalidPolicyCase{name: name, file: file, content: content}
}

func invalidJSONPolicy(name, content string) invalidPolicyCase {
	return invalidPolicy(name, "policy.json", content)
}

func invalidDomainPolicy(name, domain string) invalidPolicyCase {
	content := fmt.Sprintf(`{"version":1,"network":{"allowed_domains":[%q]}}`, domain)
	return invalidJSONPolicy(name, content)
}

func requirePolicyLoadErrors(t *testing.T, tests []invalidPolicyCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadPolicy(writePolicyFile(t, test.file, test.content))
			require.Error(t, err)
		})
	}
}

func TestLoadPolicy_LinuxBareAllowCommandsAreCaseSensitive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific allow matching semantics")
	}
	path := writePolicyFile(t, "policy.json", `{
  "version": 1,
  "commands": {"allowed": ["go", "GO"], "denied": []}
}`)
	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	require.Equal(t, []string{"go", "GO"}, policy.allowedCommands)
}

func TestLoadPolicy_RejectsUnsupportedExtension(t *testing.T) {
	_, err := LoadPolicy(writePolicyFile(t, "policy.toml", "version = 1"))
	require.ErrorContains(t, err, "unsupported policy extension")
}

func writePolicyFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
