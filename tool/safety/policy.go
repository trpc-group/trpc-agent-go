// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"errors"
	"fmt"
	"strings"
)

// Policy configures scanner decisions. YAML and JSON use the same field names.
type Policy struct {
	Version               string                        `json:"version" yaml:"version"`
	DefaultAction         Decision                      `json:"default_action" yaml:"default_action"`
	ParseErrorAction      Decision                      `json:"parse_error_action" yaml:"parse_error_action"`
	AllowedCommands       []string                      `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands        []string                      `json:"denied_commands" yaml:"denied_commands"`
	ForbiddenPaths        []string                      `json:"forbidden_paths" yaml:"forbidden_paths"`
	AllowedNetworkDomains []string                      `json:"allowed_network_domains" yaml:"allowed_network_domains"`
	DeniedNetworkDomains  []string                      `json:"denied_network_domains" yaml:"denied_network_domains"`
	DependencyCommands    []DependencyCommandPolicy     `json:"dependency_commands" yaml:"dependency_commands"`
	EnvAllowlist          []string                      `json:"env_allowlist" yaml:"env_allowlist"`
	ResourceLimits        ResourceLimits                `json:"resource_limits" yaml:"resource_limits"`
	BackendRules          BackendRules                  `json:"backend_rules" yaml:"backend_rules"`
	Audit                 AuditConfig                   `json:"audit" yaml:"audit"`
	Redaction             RedactionConfig               `json:"redaction" yaml:"redaction"`
	Rules                 map[string]RulePolicyOverride `json:"rules" yaml:"rules"`
}

// DependencyCommandPolicy describes package-manager subcommands.
type DependencyCommandPolicy struct {
	Command     string   `json:"command" yaml:"command"`
	Subcommands []string `json:"subcommands" yaml:"subcommands"`
	Action      Decision `json:"action" yaml:"action"`
}

// ResourceLimits controls resource-abuse findings.
type ResourceLimits struct {
	MaxTimeoutMS       int64 `json:"max_timeout_ms" yaml:"max_timeout_ms"`
	MaxOutputBytes     int64 `json:"max_output_bytes" yaml:"max_output_bytes"`
	MaxCommandBytes    int   `json:"max_command_bytes" yaml:"max_command_bytes"`
	MaxSegments        int   `json:"max_segments" yaml:"max_segments"`
	MaxSleepSeconds    int   `json:"max_sleep_seconds" yaml:"max_sleep_seconds"`
	MaxParallelismHint int   `json:"max_parallelism_hint" yaml:"max_parallelism_hint"`
}

// BackendRules contains backend-specific defaults.
type BackendRules struct {
	WorkspaceExec WorkspaceExecRules `json:"workspaceexec" yaml:"workspaceexec"`
	HostExec      HostExecRules      `json:"hostexec" yaml:"hostexec"`
	CodeExec      CodeExecRules      `json:"codeexec" yaml:"codeexec"`
}

// WorkspaceExecRules configures workspace execution risks.
type WorkspaceExecRules struct {
	RequireWorkspaceRelativeCwd bool     `json:"require_workspace_relative_cwd" yaml:"require_workspace_relative_cwd"`
	DenyTTY                     bool     `json:"deny_tty" yaml:"deny_tty"`
	BackgroundAction            Decision `json:"background_action" yaml:"background_action"`
}

// HostExecRules configures host execution risks.
type HostExecRules struct {
	DefaultAction    Decision `json:"default_action" yaml:"default_action"`
	DenyTTY          bool     `json:"deny_tty" yaml:"deny_tty"`
	BackgroundAction Decision `json:"background_action" yaml:"background_action"`
	MaxTimeoutMS     int64    `json:"max_timeout_ms" yaml:"max_timeout_ms"`
}

// CodeExecRules configures code execution risks.
type CodeExecRules struct {
	AllowedLanguages []string `json:"allowed_languages" yaml:"allowed_languages"`
	BashAction       Decision `json:"bash_action" yaml:"bash_action"`
}

// AuditConfig configures audit output.
type AuditConfig struct {
	Enabled    bool   `json:"enabled" yaml:"enabled"`
	Path       string `json:"path" yaml:"path"`
	FailClosed bool   `json:"fail_closed" yaml:"fail_closed"`
}

// RedactionConfig configures sensitive-value redaction.
type RedactionConfig struct {
	Enabled       *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Replacement   string   `json:"replacement" yaml:"replacement"`
	ExtraPatterns []string `json:"extra_patterns" yaml:"extra_patterns"`
}

// RulePolicyOverride customizes a known rule.
type RulePolicyOverride struct {
	Action    Decision  `json:"action" yaml:"action"`
	RiskLevel RiskLevel `json:"risk_level" yaml:"risk_level"`
}

// DefaultPolicy returns a conservative policy suitable for local examples.
func DefaultPolicy() Policy {
	return Policy{
		Version:          "1",
		DefaultAction:    DecisionAsk,
		ParseErrorAction: DecisionDeny,
		AllowedCommands: []string{
			"go", "git", "grep", "rg", "sed", "awk", "cat", "ls",
			"pwd", "echo", "test", "true", "false", "wc", "find",
			"curl", "wget", "nc", "netcat", "ssh", "scp",
			"npm", "pip", "pip3", "apt", "apt-get", "sleep", "yes",
		},
		DeniedCommands: []string{
			"rm", "rmdir", "sudo", "su", "doas", "chmod", "chown",
			"mkfs", "dd",
		},
		ForbiddenPaths: []string{
			".env", "**/.env", ".env.*", "**/.env.*",
			"~/.ssh/**", ".ssh/**", "**/.ssh/**",
			"**/*credential*", "**/*credentials*", "**/*secret*",
			"**/*token*", "**/*private*key*", "/etc/**", "/var/**",
			"/usr/**", "/bin/**", "/sbin/**", "/System/**",
		},
		AllowedNetworkDomains: []string{"proxy.example.test"},
		DependencyCommands: []DependencyCommandPolicy{
			{Command: "go", Subcommands: []string{"install", "get"}, Action: DecisionAsk},
			{Command: "npm", Subcommands: []string{"install", "i", "add"}, Action: DecisionAsk},
			{Command: "pip", Subcommands: []string{"install"}, Action: DecisionAsk},
			{Command: "pip3", Subcommands: []string{"install"}, Action: DecisionAsk},
			{Command: "apt", Subcommands: []string{"install", "remove", "upgrade"}, Action: DecisionDeny},
			{Command: "apt-get", Subcommands: []string{"install", "remove", "upgrade"}, Action: DecisionDeny},
		},
		EnvAllowlist: []string{
			"PATH", "HOME", "TMPDIR", "GOCACHE", "GOMODCACHE",
			"GOPATH", "GOFLAGS", "GOPROXY", "NO_COLOR",
		},
		ResourceLimits: ResourceLimits{
			MaxTimeoutMS:       int64(5 * 60 * 1000),
			MaxOutputBytes:     4 * 1024 * 1024,
			MaxCommandBytes:    16 * 1024,
			MaxSegments:        32,
			MaxSleepSeconds:    60,
			MaxParallelismHint: 32,
		},
		BackendRules: BackendRules{
			WorkspaceExec: WorkspaceExecRules{
				RequireWorkspaceRelativeCwd: true,
				DenyTTY:                     false,
				BackgroundAction:            DecisionAsk,
			},
			HostExec: HostExecRules{
				DefaultAction:    DecisionAsk,
				DenyTTY:          false,
				BackgroundAction: DecisionAsk,
				MaxTimeoutMS:     int64(5 * 60 * 1000),
			},
			CodeExec: CodeExecRules{
				AllowedLanguages: []string{"bash", "sh", "python", "python3", "go"},
				BashAction:       DecisionAsk,
			},
		},
		Audit: AuditConfig{
			Enabled:    false,
			FailClosed: true,
		},
		Redaction: RedactionConfig{
			Enabled:     boolPointer(true),
			Replacement: "[REDACTED]",
		},
		Rules: map[string]RulePolicyOverride{},
	}
}

func (p Policy) normalized() (Policy, error) {
	def := DefaultPolicy()
	if p.Version == "" {
		p.Version = def.Version
	}
	if p.DefaultAction == "" {
		p.DefaultAction = def.DefaultAction
	}
	if p.ParseErrorAction == "" {
		p.ParseErrorAction = def.ParseErrorAction
	}
	if len(p.AllowedCommands) == 0 {
		p.AllowedCommands = append([]string(nil), def.AllowedCommands...)
	}
	if len(p.DeniedCommands) == 0 {
		p.DeniedCommands = append([]string(nil), def.DeniedCommands...)
	}
	if len(p.ForbiddenPaths) == 0 {
		p.ForbiddenPaths = append([]string(nil), def.ForbiddenPaths...)
	}
	if len(p.DependencyCommands) == 0 {
		p.DependencyCommands = append([]DependencyCommandPolicy(nil), def.DependencyCommands...)
	}
	if len(p.EnvAllowlist) == 0 {
		p.EnvAllowlist = append([]string(nil), def.EnvAllowlist...)
	}
	if p.ResourceLimits == (ResourceLimits{}) {
		p.ResourceLimits = def.ResourceLimits
	}
	p.BackendRules.WorkspaceExec = normalizeWorkspaceExecRules(
		p.BackendRules.WorkspaceExec,
		def.BackendRules.WorkspaceExec,
	)
	p.BackendRules.HostExec = normalizeHostExecRules(
		p.BackendRules.HostExec,
		def.BackendRules.HostExec,
	)
	if len(p.BackendRules.CodeExec.AllowedLanguages) == 0 {
		p.BackendRules.CodeExec.AllowedLanguages = append([]string(nil), def.BackendRules.CodeExec.AllowedLanguages...)
	}
	if p.BackendRules.CodeExec.BashAction == "" {
		p.BackendRules.CodeExec.BashAction = def.BackendRules.CodeExec.BashAction
	}
	if p.Redaction.Replacement == "" {
		p.Redaction.Replacement = def.Redaction.Replacement
	}
	if p.Redaction.Enabled == nil {
		p.Redaction.Enabled = boolPointer(true)
	}
	if p.Rules == nil {
		p.Rules = map[string]RulePolicyOverride{}
	}
	if err := p.validate(); err != nil {
		return Policy{}, err
	}
	p.AllowedCommands = cleanStrings(p.AllowedCommands)
	p.DeniedCommands = cleanStrings(p.DeniedCommands)
	p.ForbiddenPaths = cleanStrings(p.ForbiddenPaths)
	p.AllowedNetworkDomains = cleanStrings(p.AllowedNetworkDomains)
	p.DeniedNetworkDomains = cleanStrings(p.DeniedNetworkDomains)
	p.EnvAllowlist = cleanStrings(p.EnvAllowlist)
	return p, nil
}

func boolPointer(v bool) *bool {
	return &v
}

func (p Policy) validate() error {
	for name, action := range map[string]Decision{
		"default_action":     p.DefaultAction,
		"parse_error_action": p.ParseErrorAction,
		"backend_rules.workspaceexec.background_action": p.BackendRules.WorkspaceExec.BackgroundAction,
		"backend_rules.hostexec.default_action":         p.BackendRules.HostExec.DefaultAction,
		"backend_rules.hostexec.background_action":      p.BackendRules.HostExec.BackgroundAction,
		"backend_rules.codeexec.bash_action":            p.BackendRules.CodeExec.BashAction,
	} {
		if !validDecision(action) {
			return fmt.Errorf("%s: invalid decision %q", name, action)
		}
	}
	for _, dc := range p.DependencyCommands {
		if strings.TrimSpace(dc.Command) == "" {
			return errors.New("dependency command cannot be empty")
		}
		if dc.Action != "" && !validDecision(dc.Action) {
			return fmt.Errorf("dependency command %q: invalid action %q", dc.Command, dc.Action)
		}
	}
	for id, override := range p.Rules {
		if strings.TrimSpace(id) == "" {
			return errors.New("rule override id cannot be empty")
		}
		if override.Action != "" && !validDecision(override.Action) {
			return fmt.Errorf("rule %q: invalid action %q", id, override.Action)
		}
		if override.RiskLevel != "" && !validRisk(override.RiskLevel) {
			return fmt.Errorf("rule %q: invalid risk level %q", id, override.RiskLevel)
		}
	}
	if p.ResourceLimits.MaxTimeoutMS < 0 ||
		p.ResourceLimits.MaxOutputBytes < 0 ||
		p.ResourceLimits.MaxCommandBytes < 0 ||
		p.ResourceLimits.MaxSegments < 0 ||
		p.ResourceLimits.MaxSleepSeconds < 0 ||
		p.ResourceLimits.MaxParallelismHint < 0 {
		return errors.New("resource limits cannot be negative")
	}
	return nil
}

func normalizeWorkspaceExecRules(got, def WorkspaceExecRules) WorkspaceExecRules {
	if got == (WorkspaceExecRules{}) {
		return def
	}
	if !got.RequireWorkspaceRelativeCwd {
		got.RequireWorkspaceRelativeCwd = def.RequireWorkspaceRelativeCwd
	}
	if got.BackgroundAction == "" {
		got.BackgroundAction = def.BackgroundAction
	}
	return got
}

func normalizeHostExecRules(got, def HostExecRules) HostExecRules {
	if got.DefaultAction == "" {
		got.DefaultAction = def.DefaultAction
	}
	if got.BackgroundAction == "" {
		got.BackgroundAction = def.BackgroundAction
	}
	if got.MaxTimeoutMS == 0 {
		got.MaxTimeoutMS = def.MaxTimeoutMS
	}
	return got
}

func validDecision(d Decision) bool {
	switch d {
	case DecisionAllow, DecisionDeny, DecisionAsk, DecisionNeedsHumanReview:
		return true
	default:
		return false
	}
}

func validRisk(r RiskLevel) bool {
	switch r {
	case RiskNone, RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, v := range in {
		s := strings.TrimSpace(v)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
