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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Policy holds all configurable safety rules loaded from a YAML or JSON file.
// Changing the policy file takes effect on the next LoadPolicy call without
// code changes, satisfying acceptance criterion #6.
type Policy struct {
	Version   string         `yaml:"version" json:"version"`
	Commands  CommandPolicy  `yaml:"commands" json:"commands"`
	Paths     PathPolicy     `yaml:"paths" json:"paths"`
	Network   NetworkPolicy  `yaml:"network" json:"network"`
	Resources ResourcePolicy `yaml:"resources" json:"resources"`
	Env       EnvPolicy      `yaml:"env" json:"env"`
	Secrets   SecretPolicy   `yaml:"secrets" json:"secrets"`
	HostExec  HostExecPolicy `yaml:"hostexec" json:"hostexec"`

	// compiled holds pre-compiled regexes built during LoadPolicy.
	compiled compiledPolicy
}

type compiledPolicy struct {
	secretPatterns  []*regexp.Regexp
	envDenyPatterns []*regexp.Regexp
}

// CommandPolicy controls which commands are allowed or denied.
type CommandPolicy struct {
	Allowed []string `yaml:"allowed" json:"allowed"`
	Denied  []string `yaml:"denied" json:"denied"`
	// DeniedInstallCmds lists package-manager invocations that trigger an ask
	// decision. Each entry is a two-word string like "pip install" or
	// "go install" where the first word is the executable and the second
	// is the sub-command.
	DeniedInstallCmds []string `yaml:"denied_install" json:"denied_install"`
}

// PathPolicy lists path patterns that should be denied.
// Each entry is a glob pattern (as understood by filepath.Match);
// the tilde prefix ~ is expanded to $HOME before matching.
type PathPolicy struct {
	Denied []string `yaml:"denied" json:"denied"`
}

// NetworkPolicy controls which domains may be contacted.
type NetworkPolicy struct {
	Whitelist []string `yaml:"whitelist" json:"whitelist"`
	Blacklist []string `yaml:"blacklist" json:"blacklist"`
}

// ResourcePolicy sets limits on tool execution resources.
type ResourcePolicy struct {
	MaxTimeoutSec int `yaml:"max_timeout_sec" json:"max_timeout_sec"`
	MaxOutputMB   int `yaml:"max_output_mb" json:"max_output_mb"`
	MaxConcurrent int `yaml:"max_concurrent" json:"max_concurrent"`
}

// SecretPolicy defines patterns for detecting secrets in commands and output.
type SecretPolicy struct {
	// Patterns is a list of regular expressions. Each pattern is compiled
	// during LoadPolicy; compilation errors are surfaced immediately.
	Patterns []string `yaml:"patterns" json:"patterns"`
}

// HostExecPolicy controls additional risks specific to host command execution.
type HostExecPolicy struct {
	PTYMaxDurationSec       int  `yaml:"pty_max_duration_sec" json:"pty_max_duration_sec"`
	DenyBackgroundProcesses bool `yaml:"deny_background_processes" json:"deny_background_processes"`
	DenyPrivilegeEscalation bool `yaml:"deny_privilege_escalation" json:"deny_privilege_escalation"`
}

// EnvPolicy controls which environment variables may be passed through.
type EnvPolicy struct {
	// AllowedKeys lists environment variable names that are permitted.
	// Supports wildcard suffix: "*_TOKEN" matches "GITHUB_TOKEN", "NPM_TOKEN".
	// When non-empty, only listed keys are allowed; others trigger deny.
	AllowedKeys []string `yaml:"allowed_keys" json:"allowed_keys"`
	// DeniedKeys lists environment variable names that are always blocked.
	// Supports wildcard suffix like AllowedKeys.
	DeniedKeys []string `yaml:"denied_keys" json:"denied_keys"`
	// DenyValues lists regex patterns that env values must not match.
	// Use to block secrets passed via environment variables.
	DenyValues []string `yaml:"deny_values" json:"deny_values"`
}

// LoadPolicy loads a Policy from a YAML or JSON file.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("safety: read policy %s: %w", path, err)
	}
	p := &Policy{}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, p); err != nil {
			return nil, fmt.Errorf("safety: parse policy %s: %w", path, err)
		}
	case ".json":
		if err := json.Unmarshal(data, p); err != nil {
			return nil, fmt.Errorf("safety: parse policy %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("safety: unsupported policy format %q (use .yaml, .yml, or .json)", ext)
	}
	p.applyDefaults()
	if err := p.compile(); err != nil {
		return nil, err
	}
	return p, nil
}

// LoadPolicyBytes parses a Policy from in-memory YAML or JSON bytes.
// Useful for testing without a temp file.
func LoadPolicyBytes(data []byte, format string) (*Policy, error) {
	p := &Policy{}
	switch format {
	case "yaml", "yml":
		if err := yaml.Unmarshal(data, p); err != nil {
			return nil, fmt.Errorf("safety: parse policy: %w", err)
		}
	case "json":
		if err := json.Unmarshal(data, p); err != nil {
			return nil, fmt.Errorf("safety: parse policy: %w", err)
		}
	default:
		return nil, fmt.Errorf("safety: unsupported format %q", format)
	}
	p.applyDefaults()
	if err := p.compile(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Policy) applyDefaults() {
	if p.Resources.MaxTimeoutSec == 0 {
		p.Resources.MaxTimeoutSec = 300
	}
	if p.Resources.MaxOutputMB == 0 {
		p.Resources.MaxOutputMB = 50
	}
	if p.Resources.MaxConcurrent == 0 {
		p.Resources.MaxConcurrent = 10
	}
	if len(p.Secrets.Patterns) == 0 {
		p.Secrets.Patterns = []string{
			`sk-[a-zA-Z0-9]{20,}`,
			`AKID[0-9a-zA-Z]{16,}`,
			`Bearer\s+[A-Za-z0-9\-._~+/]+=*`,
		}
	}
	if p.HostExec.PTYMaxDurationSec == 0 {
		p.HostExec.PTYMaxDurationSec = 600
	}
}

// compile pre-compiles all regex patterns so they are not re-compiled on every scan.
func (p *Policy) compile() error {
	for i, pat := range p.Secrets.Patterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			return fmt.Errorf("safety: secret pattern %d %q: %w", i, pat, err)
		}
		p.compiled.secretPatterns = append(p.compiled.secretPatterns, re)
	}
	for i, pat := range p.Env.DenyValues {
		re, err := regexp.Compile(pat)
		if err != nil {
			return fmt.Errorf("safety: env deny_values pattern %d %q: %w", i, pat, err)
		}
		p.compiled.envDenyPatterns = append(p.compiled.envDenyPatterns, re)
	}
	return nil
}

// SecretRegexps returns the pre-compiled secret detection patterns.
func (p *Policy) SecretRegexps() []*regexp.Regexp {
	return p.compiled.secretPatterns
}

// EnvDenyValueRegexps returns the pre-compiled env deny-value patterns.
func (p *Policy) EnvDenyValueRegexps() []*regexp.Regexp {
	return p.compiled.envDenyPatterns
}
