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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// PolicyFile is the configurable safety policy.
type PolicyFile struct {
	// Version is the policy schema version.
	Version string `yaml:"version" json:"version"`
	// DefaultAction is the decision when no rule matches. Use DecisionDeny
	// for fail-closed behaviour.
	DefaultAction Decision `yaml:"default_action" json:"default_action"`
	// AllowedCommands lists commands that are always allowed.
	AllowedCommands []string `yaml:"allowed_commands" json:"allowed_commands"`
	// DeniedCommands lists commands that are always denied.
	DeniedCommands []string `yaml:"denied_commands" json:"denied_commands"`
	// DeniedPaths lists filesystem paths that must not be accessed.
	DeniedPaths []string `yaml:"denied_paths" json:"denied_paths"`
	// NetworkAllowlist lists permitted network endpoints. An empty list
	// means all network access is denied.
	NetworkAllowlist []string `yaml:"network_allowlist" json:"network_allowlist"`
	// MaxTimeoutSec is the maximum allowed command execution timeout in seconds.
	MaxTimeoutSec int `yaml:"max_timeout_sec" json:"max_timeout_sec"`
	// MaxOutputBytes is the maximum allowed command output size in bytes.
	MaxOutputBytes int `yaml:"max_output_bytes" json:"max_output_bytes"`
	// AllowedEnvVars lists environment variable names that may be set.
	AllowedEnvVars []string `yaml:"allowed_env_vars" json:"allowed_env_vars"`
	// DeniedEnvVars lists environment variable names that must not be set.
	DeniedEnvVars []string `yaml:"denied_env_vars" json:"denied_env_vars"`
	// AskForReviewTools lists tool names that require human review before
	// execution.
	AskForReviewTools []string `yaml:"ask_for_review_tools" json:"ask_for_review_tools"`
}

// DefaultPolicy returns a sensible default safety policy.
//
// The default is fail-closed: DefaultAction is DecisionDeny, and common
// destructive commands, sensitive paths, and dangerous environment variables
// are denied.
func DefaultPolicy() PolicyFile {
	return PolicyFile{
		Version:       "v1",
		DefaultAction: DecisionDeny,
		DeniedCommands: []string{
			"rm", "rmdir", "mkfs", "dd", "format", "fdisk",
			"mkfs.ext4", "mkfs.xfs",
		},
		DeniedPaths: []string{
			"~/.ssh", "~/.gnupg", "~/.aws", "~/.kube",
			"~/.config/gcloud", "~/.env", "~/.credentials",
			"/etc/shadow", "/etc/passwd", "/etc/ssh",
		},
		NetworkAllowlist: []string{},
		MaxTimeoutSec:    300,
		MaxOutputBytes:   1048576,
		DeniedEnvVars: []string{
			"HOME", "PATH", "LD_PRELOAD", "LD_LIBRARY_PATH",
			"BASH_ENV", "PYTHONPATH",
		},
	}
}

// LoadPolicyFile loads a PolicyFile from the given file path.
//
// The file format is auto-detected by extension: .yaml and .yml are parsed as
// YAML; .json is parsed as JSON. The loaded values are overlaid onto
// DefaultPolicy so that missing fields retain safe defaults (fail-closed).
func LoadPolicyFile(path string) (PolicyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PolicyFile{}, fmt.Errorf("read policy file %s: %w", path, err)
	}
	return LoadPolicyFromBytes(data)
}

// LoadPolicyFromBytes loads a PolicyFile from byte content.
//
// The format is auto-detected: if the content parses as valid JSON it is
// treated as JSON, otherwise YAML. The loaded values are overlaid onto
// DefaultPolicy so that missing fields retain safe defaults (fail-closed).
func LoadPolicyFromBytes(data []byte) (PolicyFile, error) {
	base := DefaultPolicy()

	extPolicy := &policyFileRaw{}
	// Try JSON first; if it fails, fall back to YAML.
	if err := json.Unmarshal(data, extPolicy); err == nil {
		overlayPolicy(&base, extPolicy)
		return base, nil
	}

	if err := yaml.Unmarshal(data, extPolicy); err != nil {
		return PolicyFile{}, fmt.Errorf("parse policy: not valid JSON or YAML: %w", err)
	}
	overlayPolicy(&base, extPolicy)
	return base, nil
}

// policyFileRaw mirrors PolicyFile but uses pointers for all fields so that
// we can distinguish "missing from file" (nil) from "zero value in file".
type policyFileRaw struct {
	Version           *string   `yaml:"version" json:"version"`
	DefaultAction     *Decision `yaml:"default_action" json:"default_action"`
	AllowedCommands   []string  `yaml:"allowed_commands" json:"allowed_commands"`
	DeniedCommands    []string  `yaml:"denied_commands" json:"denied_commands"`
	DeniedPaths       []string  `yaml:"denied_paths" json:"denied_paths"`
	NetworkAllowlist  []string  `yaml:"network_allowlist" json:"network_allowlist"`
	MaxTimeoutSec     *int      `yaml:"max_timeout_sec" json:"max_timeout_sec"`
	MaxOutputBytes    *int      `yaml:"max_output_bytes" json:"max_output_bytes"`
	AllowedEnvVars    []string  `yaml:"allowed_env_vars" json:"allowed_env_vars"`
	DeniedEnvVars     []string  `yaml:"denied_env_vars" json:"denied_env_vars"`
	AskForReviewTools []string  `yaml:"ask_for_review_tools" json:"ask_for_review_tools"`
}

// overlayPolicy copies non-zero fields from raw onto base, preserving base
// defaults for fields that were not specified in the policy file.
func overlayPolicy(base *PolicyFile, raw *policyFileRaw) {
	if raw.Version != nil {
		base.Version = *raw.Version
	}
	if raw.DefaultAction != nil {
		base.DefaultAction = *raw.DefaultAction
	}
	if raw.AllowedCommands != nil {
		base.AllowedCommands = raw.AllowedCommands
	}
	if raw.DeniedCommands != nil {
		base.DeniedCommands = raw.DeniedCommands
	}
	if raw.DeniedPaths != nil {
		base.DeniedPaths = raw.DeniedPaths
	}
	if raw.NetworkAllowlist != nil {
		base.NetworkAllowlist = raw.NetworkAllowlist
	}
	if raw.MaxTimeoutSec != nil {
		base.MaxTimeoutSec = *raw.MaxTimeoutSec
	}
	if raw.MaxOutputBytes != nil {
		base.MaxOutputBytes = *raw.MaxOutputBytes
	}
	if raw.AllowedEnvVars != nil {
		base.AllowedEnvVars = raw.AllowedEnvVars
	}
	if raw.DeniedEnvVars != nil {
		base.DeniedEnvVars = raw.DeniedEnvVars
	}
	if raw.AskForReviewTools != nil {
		base.AskForReviewTools = raw.AskForReviewTools
	}
}

// isYAMLExt returns true if the file extension indicates a YAML file.
func isYAMLExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// isJSONExt returns true if the file extension indicates a JSON file.
func isJSONExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".json"
}
