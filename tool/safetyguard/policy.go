//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safetyguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPolicyVersion is the schema version stamped on reports and audit
// events emitted by a Guard built from a zero Version field.
const DefaultPolicyVersion = "safetyguard/v1"

// DecisionMode controls how aggregated findings map to a permission action.
type DecisionMode string

const (
	// DecisionModeFailClosed denies on any non-zero risk and asks on
	// medium when not denied. This is the default.
	DecisionModeFailClosed DecisionMode = "fail_closed"
	// DecisionModeAdvisory records findings but allows unless a finding
	// is explicitly routed to deny by RiskThresholdDeny.
	DecisionModeAdvisory DecisionMode = "advisory"
)

// ParseErrorAction is the action taken when internal/shellsafe cannot
// safely parse a command (it then never reached the allow/deny lists).
type ParseErrorAction string

const (
	// ParseErrorDeny rejects unparseable commands. Conservative default.
	ParseErrorDeny ParseErrorAction = "deny"
	// ParseErrorAsk surfaces unparseable commands for human approval.
	ParseErrorAsk ParseErrorAction = "ask"
)

// SafetyPolicy is the declarative configuration consumed by Guard. It can
// be loaded from YAML or JSON via LoadPolicy and is also constructible in
// code via DefaultSafetyPolicy plus option-style field mutation.
//
// All rule slices are optional; an empty SafetyPolicy produces a Guard
// that allows every call (no-op), preserving backward compatibility.
type SafetyPolicy struct {
	// Version is an opaque policy version stamped on reports and audit
	// events. Defaults to DefaultPolicyVersion when empty.
	Version string `yaml:"version" json:"version"`

	// Commands governs executable-name allow/deny and the structural
	// shellsafe parse.
	Commands CommandRules `yaml:"commands" json:"commands"`

	// ForbiddenPaths are path fragments/absolute paths that must not
	// appear in any command or argument (e.g. ~/.ssh, /etc/passwd).
	ForbiddenPaths []string `yaml:"forbidden_paths" json:"forbidden_paths"`

	// Network controls outbound-egress detection for curl/wget and
	// bare URLs found in arguments.
	Network NetworkRules `yaml:"network" json:"network"`

	// ResourceLimits caps timeouts and output sizes declared in tool
	// arguments.
	ResourceLimits ResourceLimits `yaml:"resource_limits" json:"resource_limits"`

	// Environment constrains the env map passed to shell tools.
	Environment EnvironmentRules `yaml:"environment" json:"environment"`

	// SensitiveInfo scans arguments for credential shapes before the
	// call reaches a tool that would echo them back to the model.
	SensitiveInfo SensitiveInfoRules `yaml:"sensitive_info" json:"sensitive_info"`

	// Decision tunes how findings aggregate into an action.
	Decision DecisionConfig `yaml:"decision" json:"decision"`

	// ToolCommandFields maps a tool name to the JSON field that carries
	// its shell command (default "command"). Used so the Guard can extract
	// the command for workspace_exec, hostexec's exec tool, the
	// claudecode bash tool and any custom shell tool without hard-coding
	// every name. Known defaults are pre-seeded.
	ToolCommandFields map[string]string `yaml:"tool_command_fields" json:"tool_command_fields"`

	// HostExecTools lists tool names that run on the host (hostexec
	// surface). Findings on these tools are escalated one risk level
	// because their blast radius is the host, not a workspace.
	HostExecTools []string `yaml:"host_exec_tools" json:"host_exec_tools"`
}

// CommandRules is the executable-name policy handed to internal/shellsafe.
type CommandRules struct {
	// Allowed restricts commands to these executables (basename-strict).
	Allowed []string `yaml:"allowed" json:"allowed"`
	// Denied rejects these executables (basename-permissive).
	Denied []string `yaml:"denied" json:"denied"`
	// DependencyChanges are executables that mutate the toolchain /
	// environment (go install, pip install, ...). They are flagged at
	// high risk and routed to ask unless explicitly allowed.
	DependencyChanges []string `yaml:"dependency_changes" json:"dependency_changes"`
	// PrivilegeEscalation are executables that elevate privileges
	// (sudo, su, doas). Flagged at critical on hostexec tools.
	PrivilegeEscalation []string `yaml:"privilege_escalation" json:"privilege_escalation"`
}

// NetworkRules controls outbound-egress detection.
type NetworkRules struct {
	// Enabled turns on URL / egress scanning. When false the Guard does
	// not inspect network commands or URLs (opt-in, backward compatible).
	Enabled bool `yaml:"enabled" json:"enabled"`
	// AllowedDomains is the egress allowlist. When non-empty, any host not
	// in the list is flagged at high risk. Empty means "no egress allowed"
	// when Enabled is true.
	AllowedDomains []string `yaml:"allowed_domains" json:"allowed_domains"`
	// NetworkTools are executables that open sockets (curl, wget, ssh,
	// scp, ...). Their presence is flagged even without an explicit URL.
	NetworkTools []string `yaml:"network_tools" json:"network_tools"`
}

// ResourceLimits caps resource usage declared in tool arguments.
type ResourceLimits struct {
	// MaxTimeoutSeconds rejects timeout_sec / timeout values above this.
	// Zero disables the check.
	MaxTimeoutSeconds int `yaml:"max_timeout_seconds" json:"max_timeout_seconds"`
	// MaxOutputBytes rejects output-size hints above this. Zero disables.
	MaxOutputBytes int `yaml:"max_output_bytes" json:"max_output_bytes"`
}

// EnvironmentRules constrains the env map passed to shell tools.
type EnvironmentRules struct {
	// AllowedVars is the env-key allowlist. When non-empty, any key not
	// in the list is flagged.
	AllowedVars []string `yaml:"allowed_vars" json:"allowed_vars"`
	// DeniedVars are always rejected (in addition to envscrub's
	// built-in blocklist).
	DeniedVars []string `yaml:"denied_vars" json:"denied_vars"`
}

// SensitiveInfoRules controls credential-shape scanning.
type SensitiveInfoRules struct {
	// Enabled turns on internal/redact-based scanning of arguments.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// DenyOnDetect routes a detected credential to deny instead of ask.
	DenyOnDetect bool `yaml:"deny_on_detect" json:"deny_on_detect"`
}

// DecisionConfig tunes how findings aggregate into an action.
type DecisionConfig struct {
	// Mode is the aggregation mode (fail_closed default).
	Mode DecisionMode `yaml:"mode" json:"mode"`
	// OnParseError is the action when shellsafe rejects a command
	// structurally (deny default).
	OnParseError ParseErrorAction `yaml:"on_parse_error" json:"on_parse_error"`
	// RiskThresholdAsk is the minimum risk level that triggers an ask
	// when not denied. Defaults to medium.
	RiskThresholdAsk RiskLevel `yaml:"risk_threshold_ask" json:"risk_threshold_ask"`
	// RiskThresholdDeny is the minimum risk level that triggers a deny.
	// Defaults to high.
	RiskThresholdDeny RiskLevel `yaml:"risk_threshold_deny" json:"risk_threshold_deny"`
}

// LoadPolicy reads a YAML or JSON policy file (selected by extension) and
// returns the validated SafetyPolicy. Unknown extensions default to YAML.
func LoadPolicy(path string) (SafetyPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SafetyPolicy{}, fmt.Errorf("safetyguard: read policy: %w", err)
	}
	return ParsePolicy(data, detectFormat(path))
}

// PolicyFormat selects the parser used by ParsePolicy.
type PolicyFormat int

const (
	// FormatYAML parses YAML.
	FormatYAML PolicyFormat = iota
	// FormatJSON parses JSON.
	FormatJSON
)

func detectFormat(path string) PolicyFormat {
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		return FormatJSON
	}
	return FormatYAML
}

// ParsePolicy decodes a policy document in the requested format.
func ParsePolicy(data []byte, format PolicyFormat) (SafetyPolicy, error) {
	var p SafetyPolicy
	switch format {
	case FormatJSON:
		if err := json.Unmarshal(data, &p); err != nil {
			return SafetyPolicy{}, fmt.Errorf("safetyguard: parse json: %w", err)
		}
	default:
		if err := yaml.Unmarshal(data, &p); err != nil {
			return SafetyPolicy{}, fmt.Errorf("safetyguard: parse yaml: %w", err)
		}
	}
	p = p.withDefaults()
	if err := p.Validate(); err != nil {
		return SafetyPolicy{}, err
	}
	return p, nil
}

// withDefaults fills derived fields without mutating the caller's slice
// headers and seeds the well-known tool command fields / host exec tools.
func (p SafetyPolicy) withDefaults() SafetyPolicy {
	if strings.TrimSpace(p.Version) == "" {
		p.Version = DefaultPolicyVersion
	}
	if p.Decision.Mode == "" {
		p.Decision.Mode = DecisionModeFailClosed
	}
	if p.Decision.OnParseError == "" {
		p.Decision.OnParseError = ParseErrorDeny
	}
	if p.Decision.RiskThresholdAsk == "" {
		p.Decision.RiskThresholdAsk = RiskLevelMedium
	}
	if p.Decision.RiskThresholdDeny == "" {
		p.Decision.RiskThresholdDeny = RiskLevelHigh
	}
	if len(p.Commands.DependencyChanges) == 0 {
		p.Commands.DependencyChanges = defaultDependencyChanges
	}
	if len(p.Commands.PrivilegeEscalation) == 0 {
		p.Commands.PrivilegeEscalation = defaultPrivilegeEscalation
	}
	if len(p.Network.NetworkTools) == 0 {
		p.Network.NetworkTools = defaultNetworkTools
	}
	if len(p.HostExecTools) == 0 {
		p.HostExecTools = defaultHostExecTools
	}
	if p.ToolCommandFields == nil {
		p.ToolCommandFields = map[string]string{}
	}
	for name, field := range defaultToolCommandFields {
		if _, ok := p.ToolCommandFields[name]; !ok {
			p.ToolCommandFields[name] = field
		}
	}
	return p
}

// Validate checks the policy for internal consistency. It does not reject
// an empty policy: that is a documented no-op.
func (p SafetyPolicy) Validate() error {
	switch p.Decision.Mode {
	case DecisionModeFailClosed, DecisionModeAdvisory:
	case "":
		return errors.New("safetyguard: decision.mode is empty after defaults")
	default:
		return fmt.Errorf("safetyguard: unknown decision mode %q", p.Decision.Mode)
	}
	switch p.Decision.OnParseError {
	case ParseErrorDeny, ParseErrorAsk:
	case "":
		return errors.New("safetyguard: decision.on_parse_error is empty after defaults")
	default:
		return fmt.Errorf("safetyguard: unknown parse-error action %q", p.Decision.OnParseError)
	}
	if p.Decision.RiskThresholdAsk != "" && !validRiskLevel(p.Decision.RiskThresholdAsk) {
		return fmt.Errorf("safetyguard: invalid risk_threshold_ask %q", p.Decision.RiskThresholdAsk)
	}
	if p.Decision.RiskThresholdDeny != "" && !validRiskLevel(p.Decision.RiskThresholdDeny) {
		return fmt.Errorf("safetyguard: invalid risk_threshold_deny %q", p.Decision.RiskThresholdDeny)
	}
	return nil
}

// Active reports whether the policy will reject or ask anything. A zero
// policy is inactive and the Guard allows every call.
func (p SafetyPolicy) Active() bool {
	return len(p.Commands.Allowed) > 0 ||
		len(p.Commands.Denied) > 0 ||
		len(p.Commands.DependencyChanges) > 0 ||
		len(p.Commands.PrivilegeEscalation) > 0 ||
		len(p.ForbiddenPaths) > 0 ||
		p.Network.Enabled ||
		p.ResourceLimits.MaxTimeoutSeconds > 0 ||
		p.ResourceLimits.MaxOutputBytes > 0 ||
		len(p.Environment.AllowedVars) > 0 ||
		len(p.Environment.DeniedVars) > 0 ||
		p.SensitiveInfo.Enabled
}

// commandField returns the JSON field that carries the shell command for
// the named tool, defaulting to "command".
func (p SafetyPolicy) commandField(toolName string) string {
	if f, ok := p.ToolCommandFields[toolName]; ok && f != "" {
		return f
	}
	return defaultCommandField
}

// isHostExecTool reports whether toolName is configured as a host-exec
// surface (higher blast radius).
func (p SafetyPolicy) isHostExecTool(toolName string) bool {
	for _, name := range p.HostExecTools {
		if name == toolName {
			return true
		}
	}
	return false
}

const defaultCommandField = "command"

var (
	defaultDependencyChanges = []string{
		"go", "pip", "pip3", "python", "python3", "pipx",
		"npm", "yarn", "pnpm", "node", "npx",
		"cargo", "rustc",
		"apt", "apt-get", "dpkg", "yum", "dnf", "rpm", "zypper",
		"brew", "port",
		"gem", "bundle",
		"mvn", "gradle",
		"docker", "podman",
	}
	defaultPrivilegeEscalation = []string{
		"sudo", "su", "doas", "pkexec", "runuser", "gosu",
	}
	defaultNetworkTools = []string{
		"curl", "wget", "ftp", "scp", "sftp", "rsync",
		"ssh", "telnet", "nc", "ncat", "netcat",
		"dig", "nslookup", "host",
	}
	// defaultHostExecTools are the tool names the framework ships that
	// execute on the host rather than inside a managed workspace.
	defaultHostExecTools = []string{
		"hostexec", "host_exec", "exec", "run_command",
	}
	// defaultToolCommandFields seeds the command-extraction map for the
	// shell-execution tools the framework ships today.
	defaultToolCommandFields = map[string]string{
		"workspace_exec":    "command",
		"workspace_command": "command",
		"hostexec":          "command",
		"host_exec":         "command",
		"exec":              "command",
		"run_command":       "command",
		"bash":              "command",
		"shell":             "command",
	}
)

// DefaultSafetyPolicy returns a conservative, ready-to-use policy that
// demonstrates every risk category without being empty. It is the
// programmatic equivalent of the tool_safety_policy.yaml sample.
func DefaultSafetyPolicy() SafetyPolicy {
	p := SafetyPolicy{
		Version: DefaultPolicyVersion,
		Commands: CommandRules{
			Denied: []string{
				"rm", "rmdir", "dd", "mkfs", "fdisk", "shred", "wipe",
				"chmod", "chown", "chattr",
				"kill", "pkill", "killall",
				"reboot", "shutdown", "poweroff", "halt", "init",
				"mkswap", "mount", "umount",
			},
		},
		ForbiddenPaths: []string{
			"~/.ssh", "~/.aws", "~/.config/gcloud", "~/.gnupg",
			"~/.bash_history", "~/.netrc", "~/.docker/config.json",
			"/etc/passwd", "/etc/shadow", "/etc/sudoers", "/etc/hosts",
		},
		Network: NetworkRules{
			Enabled:        true,
			AllowedDomains: []string{"localhost", "127.0.0.1", "::1"},
		},
		ResourceLimits: ResourceLimits{
			MaxTimeoutSeconds: 600,
			MaxOutputBytes:    1 << 20, // 1 MiB
		},
		Environment: EnvironmentRules{
			AllowedVars: []string{"LANG", "TZ", "LC_ALL", "HOME", "PATH"},
		},
		SensitiveInfo: SensitiveInfoRules{
			Enabled:      true,
			DenyOnDetect: false,
		},
		Decision: DecisionConfig{
			Mode:              DecisionModeFailClosed,
			OnParseError:      ParseErrorDeny,
			RiskThresholdAsk:  RiskLevelMedium,
			RiskThresholdDeny: RiskLevelHigh,
		},
	}
	return p.withDefaults()
}
