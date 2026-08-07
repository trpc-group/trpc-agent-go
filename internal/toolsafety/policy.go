// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SafetyPolicy is the configurable policy for tool execution safety checks.
type SafetyPolicy struct {
	Version           string          `yaml:"version" json:"version"`
	AllowedCommands   []string        `yaml:"allowed_commands" json:"allowed_commands"`
	DeniedCommands    []string        `yaml:"denied_commands" json:"denied_commands"`
	DangerousPatterns []PatternRule   `yaml:"dangerous_patterns" json:"dangerous_patterns"`
	NetworkPolicy     *NetworkPolicy  `yaml:"network_policy" json:"network_policy,omitempty"`
	PathPolicy        *PathPolicy     `yaml:"path_policy" json:"path_policy,omitempty"`
	ResourcePolicy    *ResourcePolicy `yaml:"resource_policy" json:"resource_policy,omitempty"`
	SensitivePatterns []string        `yaml:"sensitive_patterns" json:"sensitive_patterns,omitempty"`
	DecisionPolicy    *DecisionPolicy `yaml:"decision_policy" json:"decision_policy,omitempty"`
	AuditPolicy       *AuditPolicy    `yaml:"audit_policy" json:"audit_policy,omitempty"`
}

// PatternRule defines a regex-based dangerous pattern check.
type PatternRule struct {
	Pattern     string    `yaml:"pattern" json:"pattern"`
	RiskLevel   RiskLevel `yaml:"risk_level" json:"risk_level"`
	Description string    `yaml:"description" json:"description,omitempty"`
	RuleID      RuleID    `yaml:"rule_id" json:"rule_id,omitempty"`
}

// NetworkPolicy controls network egress checking rules.
type NetworkPolicy struct {
	AllowedDomains []string `yaml:"allowed_domains" json:"allowed_domains"`
	BlockedDomains []string `yaml:"blocked_domains" json:"blocked_domains"`
	DefaultAction  string   `yaml:"default_action" json:"default_action"`
}

// PathPolicy controls filesystem path checking rules.
type PathPolicy struct {
	DeniedPaths    []string `yaml:"denied_paths" json:"denied_paths"`
	AllowedPaths   []string `yaml:"allowed_paths" json:"allowed_paths"`
	SensitivePaths []string `yaml:"sensitive_paths" json:"sensitive_paths"`
}

// ResourcePolicy controls resource usage limits.
type ResourcePolicy struct {
	MaxTimeoutS    int   `yaml:"max_timeout_s" json:"max_timeout_s"`
	MaxOutputBytes int64 `yaml:"max_output_bytes" json:"max_output_bytes"`
	MaxSleepS      int   `yaml:"max_sleep_s" json:"max_sleep_s"`
	MaxProcessCnt  int   `yaml:"max_process_count" json:"max_process_count"`
}

// DecisionPolicy controls how scan findings are converted to execution decisions.
type DecisionPolicy struct {
	DefaultOnParseFailure string `yaml:"default_on_parse_failure" json:"default_on_parse_failure"`
	DefaultOnUnknownRisk  string `yaml:"default_on_unknown_risk" json:"default_on_unknown_risk"`
	AskOnRiskLevel        string `yaml:"ask_on_risk_level" json:"ask_on_risk_level"`
}

// AuditPolicy controls audit event logging.
type AuditPolicy struct {
	Enabled         bool     `yaml:"enabled" json:"enabled"`
	OutputPath      string   `yaml:"output_path" json:"output_path"`
	SensitiveFields []string `yaml:"sensitive_fields" json:"sensitive_fields,omitempty"`
}

// DefaultPolicy returns a policy with sensible defaults.
func DefaultPolicy() *SafetyPolicy {
	return &SafetyPolicy{
		Version: "1.0",
		DeniedCommands: []string{
			"rm", "dd", "mkfs", "shred", "chmod", "chown",
			"curl", "wget", "nc", "ncat",
		},
		DangerousPatterns: []PatternRule{
			{Pattern: `rm\s+(-rf?\s+)?/?$`, RiskLevel: RiskLevelCritical, Description: "Destructive recursive delete on root / system directory", RuleID: RuleDestructivePath},
			{Pattern: `pip\s+install\s+`, RiskLevel: RiskLevelHigh, Description: "Dependency install via pip", RuleID: RuleDependencyInstall},
			{Pattern: `npm\s+install\s+`, RiskLevel: RiskLevelHigh, Description: "Dependency install via npm", RuleID: RuleDependencyInstall},
			{Pattern: `go\s+install\s+`, RiskLevel: RiskLevelHigh, Description: "Dependency install via go", RuleID: RuleDependencyInstall},
		},
		NetworkPolicy: &NetworkPolicy{
			AllowedDomains: []string{},
			BlockedDomains: []string{},
			DefaultAction:  "deny",
		},
		PathPolicy: &PathPolicy{
			DeniedPaths:    []string{"/etc/**", "/var/**"},
			SensitivePaths: []string{"**/.env", "**/.ssh/**", "**/*.pem", "**/credentials"},
			AllowedPaths:   []string{"/tmp/**", "/workspace/**"},
		},
		ResourcePolicy: &ResourcePolicy{
			MaxTimeoutS:    300,
			MaxOutputBytes: 10 << 20, // 10 MB
			MaxSleepS:      60,
		},
		DecisionPolicy: &DecisionPolicy{
			DefaultOnParseFailure: "deny",
			DefaultOnUnknownRisk:  "ask",
			AskOnRiskLevel:        "high",
		},
		AuditPolicy: &AuditPolicy{
			Enabled:    true,
			OutputPath: "",
		},
	}
}

// LoadPolicy loads a SafetyPolicy from a YAML or JSON file.
// When path is empty, it returns the default policy.
func LoadPolicy(path string) (*SafetyPolicy, error) {
	if path == "" {
		return DefaultPolicy(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("toolsafety: read policy file: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		return loadYAML(data)
	case ".json":
		return loadJSON(data)
	default:
		return nil, fmt.Errorf("toolsafety: unsupported policy format %q (use .yaml, .yml, or .json)", ext)
	}
}

func loadYAML(data []byte) (*SafetyPolicy, error) {
	// Decode with gopkg.in/yaml.v3 (already in root go.mod).
	var policy SafetyPolicy
	if err := yamlUnmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("toolsafety: parse yaml policy: %w", err)
	}
	return &policy, nil
}

func loadJSON(data []byte) (*SafetyPolicy, error) {
	var policy SafetyPolicy
	if err := jsonUnmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("toolsafety: parse json policy: %w", err)
	}
	return &policy, nil
}
