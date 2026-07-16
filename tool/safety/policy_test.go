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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultPolicy_FailClosed verifies that the default policy is fail-closed.
func TestDefaultPolicy_FailClosed(t *testing.T) {
	policy := DefaultPolicy()

	assert.Equal(t, DecisionDeny, policy.DefaultAction, "default action should be deny")
	assert.Equal(t, "v1", policy.Version)
	assert.NotEmpty(t, policy.DeniedCommands, "should have denied commands")
	assert.NotEmpty(t, policy.DeniedPaths, "should have denied paths")
	assert.NotEmpty(t, policy.DeniedEnvVars, "should have denied env vars")
	assert.Empty(t, policy.NetworkAllowlist, "network allowlist should be empty (deny all)")
	assert.Equal(t, 300, policy.MaxTimeoutSec)
	assert.Equal(t, 1048576, policy.MaxOutputBytes)
}

// TestLoadPolicyFromBytes_YAML verifies loading a policy from YAML content.
func TestLoadPolicyFromBytes_YAML(t *testing.T) {
	yamlData := []byte(`
version: v2
default_action: allow
allowed_commands:
  - go
  - git
denied_commands:
  - rm
network_allowlist:
  - api.example.com
max_timeout_sec: 600
max_output_bytes: 2097152
allowed_env_vars:
  - HOME
  - GOPATH
denied_env_vars:
  - LD_PRELOAD
ask_for_review_tools:
  - dangerous_tool
`)

	policy, err := LoadPolicyFromBytes(yamlData)
	require.NoError(t, err)

	assert.Equal(t, "v2", policy.Version)
	assert.Equal(t, DecisionAllow, policy.DefaultAction)
	assert.Equal(t, []string{"go", "git"}, policy.AllowedCommands)
	assert.Equal(t, []string{"rm"}, policy.DeniedCommands)
	assert.Equal(t, []string{"api.example.com"}, policy.NetworkAllowlist)
	assert.Equal(t, 600, policy.MaxTimeoutSec)
	assert.Equal(t, 2097152, policy.MaxOutputBytes)
	assert.Equal(t, []string{"HOME", "GOPATH"}, policy.AllowedEnvVars)
	assert.Equal(t, []string{"LD_PRELOAD"}, policy.DeniedEnvVars)
	assert.Equal(t, []string{"dangerous_tool"}, policy.AskForReviewTools)
}

// TestLoadPolicyFromBytes_JSON verifies loading a policy from JSON content.
func TestLoadPolicyFromBytes_JSON(t *testing.T) {
	jsonData, err := json.Marshal(map[string]any{
		"version":              "v2",
		"default_action":       "allow",
		"allowed_commands":     []string{"go", "git"},
		"denied_commands":      []string{"rm"},
		"network_allowlist":    []string{"api.example.com"},
		"max_timeout_sec":      600,
		"max_output_bytes":     2097152,
		"allowed_env_vars":     []string{"HOME", "GOPATH"},
		"denied_env_vars":      []string{"LD_PRELOAD"},
		"ask_for_review_tools": []string{"dangerous_tool"},
	})
	require.NoError(t, err)

	policy, err := LoadPolicyFromBytes(jsonData)
	require.NoError(t, err)

	assert.Equal(t, "v2", policy.Version)
	assert.Equal(t, DecisionAllow, policy.DefaultAction)
	assert.Equal(t, []string{"go", "git"}, policy.AllowedCommands)
	assert.Equal(t, []string{"rm"}, policy.DeniedCommands)
	assert.Equal(t, []string{"api.example.com"}, policy.NetworkAllowlist)
	assert.Equal(t, 600, policy.MaxTimeoutSec)
	assert.Equal(t, 2097152, policy.MaxOutputBytes)
}

// TestLoadPolicyFile_NotFound verifies that loading a non-existent file returns an error.
func TestLoadPolicyFile_NotFound(t *testing.T) {
	_, err := LoadPolicyFile(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read policy file")
}

// TestLoadPolicyFile_Overlay verifies that missing fields in a policy file
// get their values from DefaultPolicy (fail-closed overlay).
func TestLoadPolicyFile_Overlay(t *testing.T) {
	// Only specify version and default_action; other fields should come from defaults.
	yamlData := []byte(`
version: v2
default_action: allow
`)
	policy, err := LoadPolicyFromBytes(yamlData)
	require.NoError(t, err)

	assert.Equal(t, "v2", policy.Version)
	assert.Equal(t, DecisionAllow, policy.DefaultAction)

	// These should come from DefaultPolicy since they're not in the YAML.
	defaultP := DefaultPolicy()
	assert.Equal(t, defaultP.DeniedCommands, policy.DeniedCommands)
	assert.Equal(t, defaultP.DeniedPaths, policy.DeniedPaths)
	assert.Equal(t, defaultP.DeniedEnvVars, policy.DeniedEnvVars)
	assert.Equal(t, defaultP.MaxTimeoutSec, policy.MaxTimeoutSec)
	assert.Equal(t, defaultP.MaxOutputBytes, policy.MaxOutputBytes)
}

// TestLoadPolicyFile_YAMLFile verifies loading policy from a YAML file on disk.
func TestLoadPolicyFile_YAMLFile(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "policy.yaml")

	yamlData := []byte(`
version: v1
default_action: deny
network_allowlist:
  - api.trusted.com
max_timeout_sec: 120
`)
	err := os.WriteFile(yamlPath, yamlData, 0o644)
	require.NoError(t, err)

	policy, err := LoadPolicyFile(yamlPath)
	require.NoError(t, err)

	assert.Equal(t, "v1", policy.Version)
	assert.Equal(t, DecisionDeny, policy.DefaultAction)
	assert.Equal(t, []string{"api.trusted.com"}, policy.NetworkAllowlist)
	assert.Equal(t, 120, policy.MaxTimeoutSec)
}

// TestLoadPolicyFile_JSONFile verifies loading policy from a JSON file on disk.
func TestLoadPolicyFile_JSONFile(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "policy.json")

	jsonData, err := json.Marshal(map[string]any{
		"version":           "v1",
		"default_action":    "deny",
		"network_allowlist": []string{"api.trusted.com"},
		"max_timeout_sec":   120,
	})
	require.NoError(t, err)

	err = os.WriteFile(jsonPath, jsonData, 0o644)
	require.NoError(t, err)

	policy, err := LoadPolicyFile(jsonPath)
	require.NoError(t, err)

	assert.Equal(t, "v1", policy.Version)
	assert.Equal(t, DecisionDeny, policy.DefaultAction)
	assert.Equal(t, []string{"api.trusted.com"}, policy.NetworkAllowlist)
}

// TestLoadPolicyFromBytes_InvalidContent verifies that invalid content returns an error.
func TestLoadPolicyFromBytes_InvalidContent(t *testing.T) {
	_, err := LoadPolicyFromBytes([]byte("{{invalid yaml: ["))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse policy")
}

// TestLoadPolicyFromBytes_EmptyContent verifies that empty content falls back to defaults.
func TestLoadPolicyFromBytes_EmptyContent(t *testing.T) {
	policy, err := LoadPolicyFromBytes([]byte("{}"))
	require.NoError(t, err)

	// All fields should be from DefaultPolicy since nothing was specified.
	defaultP := DefaultPolicy()
	assert.Equal(t, defaultP.DefaultAction, policy.DefaultAction)
	assert.Equal(t, defaultP.Version, policy.Version)
}

// TestIsYAMLExt verifies YAML extension detection.
func TestIsYAMLExt(t *testing.T) {
	assert.True(t, isYAMLExt("policy.yaml"))
	assert.True(t, isYAMLExt("policy.YAML"))
	assert.True(t, isYAMLExt("policy.yml"))
	assert.True(t, isYAMLExt("policy.YML"))
	assert.False(t, isYAMLExt("policy.json"))
	assert.False(t, isYAMLExt("policy.txt"))
}

// TestIsJSONExt verifies JSON extension detection.
func TestIsJSONExt(t *testing.T) {
	assert.True(t, isJSONExt("policy.json"))
	assert.True(t, isJSONExt("policy.JSON"))
	assert.False(t, isJSONExt("policy.yaml"))
	assert.False(t, isJSONExt("policy.txt"))
}

// TestDefaultPolicy_DeniedCommands verifies expected commands in deny list.
func TestDefaultPolicy_DeniedCommands(t *testing.T) {
	policy := DefaultPolicy()
	assert.Contains(t, policy.DeniedCommands, "rm")
	assert.Contains(t, policy.DeniedCommands, "dd")
	assert.Contains(t, policy.DeniedCommands, "mkfs")
}

// TestDefaultPolicy_DeniedPaths verifies expected paths in deny list.
func TestDefaultPolicy_DeniedPaths(t *testing.T) {
	policy := DefaultPolicy()
	assert.Contains(t, policy.DeniedPaths, "~/.ssh")
	assert.Contains(t, policy.DeniedPaths, "/etc/shadow")
	assert.Contains(t, policy.DeniedPaths, "/etc/passwd")
}
