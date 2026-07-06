//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package toolsafety provides a configurable command-content safety
// scanner that sits on top of shellsafe and adds parameter-level
// risk analysis. shellsafe validates command structure and executable
// names; this package inspects what the command actually does — which
// files it accesses, which hosts it contacts, whether it installs
// dependencies — and produces structured scan reports and audit events.
package toolsafety

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SafetyPolicy is the top-level configuration loaded from a YAML or
// JSON file. Modifying the policy file does not require recompilation.
type SafetyPolicy struct {
	Version string `yaml:"version" json:"version"`

	// DeniedCommands lists command basenames or full paths that are
	// always blocked regardless of arguments (content-level deny,
	// separate from shellsafe's command-name policy).
	DeniedCommands []string `yaml:"denied_commands" json:"denied_commands"`

	// AllowedCommands is an allowlist of command basenames. When
	// non-empty, any command not in this list triggers a finding.
	AllowedCommands []string `yaml:"allowed_commands" json:"allowed_commands"`

	// DeniedPathPatterns are regex patterns matched against the
	// command arguments. A match indicates the command tries to
	// access a sensitive path.
	DeniedPathPatterns []string `yaml:"denied_path_patterns" json:"denied_path_patterns"`

	// AllowedDomains is the domain whitelist for network tools.
	// Network connections to domains not in this list trigger R2-NET.
	AllowedDomains []string `yaml:"allowed_domains" json:"allowed_domains"`

	// BlockedNetworkTools lists command names that are blocked from
	// making any network connection (e.g. curl, wget, nc).
	BlockedNetworkTools []string `yaml:"blocked_network_tools" json:"blocked_network_tools"`

	// MaxTimeoutSec is the maximum allowed timeout in seconds.
	// Commands requesting longer timeouts trigger R6-RES.
	MaxTimeoutSec int `yaml:"max_timeout_sec" json:"max_timeout_sec"`

	// MaxOutputBytes is the maximum allowed output size.
	MaxOutputBytes int64 `yaml:"max_output_bytes" json:"max_output_bytes"`

	// AutoDenyRiskLevels lists the risk levels that trigger an
	// automatic deny decision. Typical values: critical, high.
	AutoDenyRiskLevels []string `yaml:"auto_deny_risk_levels" json:"auto_deny_risk_levels"`

	// SensitivePatterns are regex patterns applied to command output
	// to detect leaked secrets (API keys, tokens, private keys).
	SensitivePatterns []SensitivePattern `yaml:"sensitive_patterns" json:"sensitive_patterns"`

	// BackendOverrides specifies per-backend policy adjustments.
	// Keys: "hostexec", "workspaceexec", "codeexec".
	BackendOverrides map[string]BackendPolicy `yaml:"backend_overrides" json:"backend_overrides"`
}

// SensitivePattern defines a regex for detecting leaked secrets.
type SensitivePattern struct {
	Name    string `yaml:"name" json:"name"`
	Pattern string `yaml:"pattern" json:"pattern"`
}

// BackendPolicy allows per-backend overrides of safety settings.
type BackendPolicy struct {
	AutoDenyRiskLevels []string `yaml:"auto_deny_risk_levels" json:"auto_deny_risk_levels"`
	MaxTimeoutSec      int      `yaml:"max_timeout_sec" json:"max_timeout_sec"`
	MaxOutputBytes     int64    `yaml:"max_output_bytes" json:"max_output_bytes"`
}

// EffectiveAutoDeny returns the auto-deny risk levels for the given
// backend, falling back to the global setting.
func (p *SafetyPolicy) EffectiveAutoDeny(backend string) []string {
	if p == nil {
		return nil
	}
	if ov, ok := p.BackendOverrides[backend]; ok && len(ov.AutoDenyRiskLevels) > 0 {
		return ov.AutoDenyRiskLevels
	}
	return p.AutoDenyRiskLevels
}

// EffectiveMaxTimeout returns the max timeout for the given backend,
// falling back to the global setting.
func (p *SafetyPolicy) EffectiveMaxTimeout(backend string) int {
	if p == nil {
		return 0
	}
	if ov, ok := p.BackendOverrides[backend]; ok && ov.MaxTimeoutSec > 0 {
		return ov.MaxTimeoutSec
	}
	return p.MaxTimeoutSec
}

// EffectiveMaxOutput returns the max output size for the given backend.
func (p *SafetyPolicy) EffectiveMaxOutput(backend string) int64 {
	if p == nil {
		return 0
	}
	if ov, ok := p.BackendOverrides[backend]; ok && ov.MaxOutputBytes > 0 {
		return ov.MaxOutputBytes
	}
	return p.MaxOutputBytes
}

// IsAutoDeny reports whether the given risk level should trigger an
// automatic deny for the given backend.
func (p *SafetyPolicy) IsAutoDeny(backend string, level RiskLevel) bool {
	for _, l := range p.EffectiveAutoDeny(backend) {
		if strings.EqualFold(l, string(level)) {
			return true
		}
	}
	return false
}

// LoadPolicyFromFile reads and parses a policy file in YAML or JSON
// format. The format is detected from the file extension (.yaml, .yml,
// .json). Returns an error if the file cannot be read or parsed.
func LoadPolicyFromFile(path string) (*SafetyPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file %s: %w", path, err)
	}
	return parsePolicy(data, path)
}

func parsePolicy(data []byte, path string) (*SafetyPolicy, error) {
	var policy SafetyPolicy
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".json") {
		if err := json.Unmarshal(data, &policy); err != nil {
			return nil, fmt.Errorf("parse policy JSON: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &policy); err != nil {
			return nil, fmt.Errorf("parse policy YAML: %w", err)
		}
	}
	if policy.Version == "" {
		policy.Version = "1.0"
	}
	// Apply sensible defaults when fields are zero.
	if len(policy.AutoDenyRiskLevels) == 0 {
		policy.AutoDenyRiskLevels = []string{"critical", "high"}
	}
	return &policy, nil
}
