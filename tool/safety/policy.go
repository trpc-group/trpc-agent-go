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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Policy controls safety guard behavior.
type Policy struct {
	AllowedCommands []string `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands  []string `json:"denied_commands" yaml:"denied_commands"`
	AllowedDomains  []string `json:"allowed_domains" yaml:"allowed_domains"`
	DeniedPaths     []string `json:"denied_paths" yaml:"denied_paths"`
	EnvAllowlist    []string `json:"env_allowlist" yaml:"env_allowlist"`

	MaxTimeoutSec    int `json:"max_timeout_sec" yaml:"max_timeout_sec"`
	MaxOutputBytes   int `json:"max_output_bytes" yaml:"max_output_bytes"`
	LongSleepSeconds int `json:"long_sleep_seconds" yaml:"long_sleep_seconds"`

	ParseErrorAction               Decision         `json:"parse_error_action" yaml:"parse_error_action"`
	UnknownToolAction              Decision         `json:"unknown_tool_action" yaml:"unknown_tool_action"`
	HostExecTTYAction              Decision         `json:"hostexec_tty_action" yaml:"hostexec_tty_action"`
	BackgroundAction               Decision         `json:"background_action" yaml:"background_action"`
	NonWhitelistedNetworkAction    Decision         `json:"non_whitelisted_network_action" yaml:"non_whitelisted_network_action"`
	DependencyInstallAction        Decision         `json:"dependency_install_action" yaml:"dependency_install_action"`
	ShellBypassAction              Decision         `json:"shell_bypass_action" yaml:"shell_bypass_action"`
	DisallowedEnvironmentAction    Decision         `json:"disallowed_environment_action" yaml:"disallowed_environment_action"`
	SensitivePathReadAction        Decision         `json:"sensitive_path_read_action" yaml:"sensitive_path_read_action"`
	ReviewShellPipelines           bool             `json:"review_shell_pipelines" yaml:"review_shell_pipelines"`
	DenyDangerousRecursiveDelete   bool             `json:"deny_dangerous_recursive_delete" yaml:"deny_dangerous_recursive_delete"`
	DenySecretLeakage              bool             `json:"deny_secret_leakage" yaml:"deny_secret_leakage"`
	RedactSensitiveEvidence        bool             `json:"redact_sensitive_evidence" yaml:"redact_sensitive_evidence"`
	RedactSensitivePaths           bool             `json:"redact_sensitive_paths" yaml:"redact_sensitive_paths"`
	FailClosedOnUnsupportedBackend bool             `json:"fail_closed_on_unsupported_backend" yaml:"fail_closed_on_unsupported_backend"`
	AuditFailureMode               AuditFailureMode `json:"audit_failure_mode" yaml:"audit_failure_mode"`
}

// DefaultPolicy returns conservative defaults suitable for examples and tests.
func DefaultPolicy() Policy {
	return Policy{
		AllowedCommands: []string{
			"go", "git", "echo", "cat", "pwd", "ls", "grep", "rg",
			"sed", "awk", "wc", "head", "tail", "true", "false",
			"curl", "wget", "sleep", "yes",
		},
		DeniedCommands: []string{
			"rm", "nc", "netcat", "ssh", "scp",
			"sftp", "sudo", "su", "apt", "apt-get", "yum", "dnf",
			"brew", "npm", "npx", "pip", "pip3", "python", "python3",
		},
		AllowedDomains: []string{
			"github.com", "proxy.golang.org", "sum.golang.org",
		},
		DeniedPaths: []string{
			"/", "/etc", "/usr", "/var", "/bin", "/sbin", "~/.ssh",
			"~/.aws", "~/.config/gcloud", ".env", "id_rsa",
			"id_ed25519", "credentials", "credential", "secrets",
		},
		EnvAllowlist: []string{
			"HOME", "TMPDIR", "GOMODCACHE", "GOCACHE",
			"GOPROXY", "GONOSUMDB", "GONOPROXY", "GOFLAGS",
		},
		MaxTimeoutSec:    300,
		MaxOutputBytes:   1 << 20,
		LongSleepSeconds: 60,

		ParseErrorAction:             DecisionAsk,
		UnknownToolAction:            DecisionAllow,
		HostExecTTYAction:            DecisionAsk,
		BackgroundAction:             DecisionAsk,
		NonWhitelistedNetworkAction:  DecisionDeny,
		DependencyInstallAction:      DecisionAsk,
		ShellBypassAction:            DecisionAsk,
		DisallowedEnvironmentAction:  DecisionAsk,
		SensitivePathReadAction:      DecisionDeny,
		ReviewShellPipelines:         true,
		DenyDangerousRecursiveDelete: true,
		DenySecretLeakage:            true,
		RedactSensitiveEvidence:      true,
		RedactSensitivePaths:         false,
		AuditFailureMode:             AuditBestEffort,
	}
}

// ProductionPolicy returns a stricter policy for deployments where every
// executable tool should be explicitly wired to the guard.
func ProductionPolicy() Policy {
	p := DefaultPolicy()
	p.UnknownToolAction = DecisionAsk
	p.FailClosedOnUnsupportedBackend = true
	p.AuditFailureMode = AuditFailClosed
	p.RedactSensitivePaths = true
	return p
}

// Normalize fills unset fields and validates decisions.
func (p Policy) Normalize() Policy {
	def := DefaultPolicy()
	if p.AllowedCommands == nil {
		p.AllowedCommands = def.AllowedCommands
	}
	if p.DeniedCommands == nil {
		p.DeniedCommands = def.DeniedCommands
	}
	if p.AllowedDomains == nil {
		p.AllowedDomains = def.AllowedDomains
	}
	if p.DeniedPaths == nil {
		p.DeniedPaths = def.DeniedPaths
	}
	if p.EnvAllowlist == nil {
		p.EnvAllowlist = def.EnvAllowlist
	}
	if p.MaxTimeoutSec == 0 {
		p.MaxTimeoutSec = def.MaxTimeoutSec
	}
	if p.MaxOutputBytes == 0 {
		p.MaxOutputBytes = def.MaxOutputBytes
	}
	if p.LongSleepSeconds == 0 {
		p.LongSleepSeconds = def.LongSleepSeconds
	}
	p.ParseErrorAction = normalizeDecision(p.ParseErrorAction, def.ParseErrorAction)
	p.UnknownToolAction = normalizeDecision(p.UnknownToolAction, def.UnknownToolAction)
	p.HostExecTTYAction = normalizeDecision(p.HostExecTTYAction, def.HostExecTTYAction)
	p.BackgroundAction = normalizeDecision(p.BackgroundAction, def.BackgroundAction)
	p.NonWhitelistedNetworkAction = normalizeDecision(
		p.NonWhitelistedNetworkAction, def.NonWhitelistedNetworkAction)
	p.DependencyInstallAction = normalizeDecision(
		p.DependencyInstallAction, def.DependencyInstallAction)
	p.ShellBypassAction = normalizeDecision(p.ShellBypassAction, def.ShellBypassAction)
	p.DisallowedEnvironmentAction = normalizeDecision(
		p.DisallowedEnvironmentAction, def.DisallowedEnvironmentAction)
	p.SensitivePathReadAction = normalizeDecision(
		p.SensitivePathReadAction, def.SensitivePathReadAction)
	p.AuditFailureMode = normalizeAuditFailureMode(p.AuditFailureMode, def.AuditFailureMode)
	return p
}

// LoadPolicy loads a JSON or YAML policy file.
func LoadPolicy(path string) (Policy, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- policy path is caller-configured, not model/tool input.
	if err != nil {
		return Policy{}, err
	}
	p := DefaultPolicy()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		err = json.Unmarshal(b, &p)
	case ".yaml", ".yml", "":
		err = yaml.Unmarshal(b, &p)
	default:
		return Policy{}, fmt.Errorf("unsupported policy extension %q", filepath.Ext(path))
	}
	if err != nil {
		return Policy{}, err
	}
	return p.Normalize(), nil
}

// LoadPolicyStrict loads a policy and rejects unknown fields and invalid limits.
func LoadPolicyStrict(path string) (Policy, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- policy path is caller-configured, not model/tool input.
	if err != nil {
		return Policy{}, err
	}
	p := DefaultPolicy()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		err = dec.Decode(&p)
	case ".yaml", ".yml", "":
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		err = dec.Decode(&p)
	default:
		return Policy{}, fmt.Errorf("unsupported policy extension %q", filepath.Ext(path))
	}
	if err != nil {
		return Policy{}, err
	}
	if err := validatePolicy(p); err != nil {
		return Policy{}, err
	}
	return p.Normalize(), nil
}

func validatePolicy(p Policy) error {
	if p.MaxTimeoutSec < 0 {
		return fmt.Errorf("max_timeout_sec must be non-negative")
	}
	if p.MaxOutputBytes < 0 {
		return fmt.Errorf("max_output_bytes must be non-negative")
	}
	if p.LongSleepSeconds < 0 {
		return fmt.Errorf("long_sleep_seconds must be non-negative")
	}
	for name, d := range map[string]Decision{
		"parse_error_action":             p.ParseErrorAction,
		"unknown_tool_action":            p.UnknownToolAction,
		"hostexec_tty_action":            p.HostExecTTYAction,
		"background_action":              p.BackgroundAction,
		"non_whitelisted_network_action": p.NonWhitelistedNetworkAction,
		"dependency_install_action":      p.DependencyInstallAction,
		"shell_bypass_action":            p.ShellBypassAction,
		"disallowed_environment_action":  p.DisallowedEnvironmentAction,
		"sensitive_path_read_action":     p.SensitivePathReadAction,
	} {
		if d == "" {
			continue
		}
		if normalizeDecision(d, "") == "" {
			return fmt.Errorf("%s has invalid decision %q", name, d)
		}
	}
	if p.AuditFailureMode != "" && normalizeAuditFailureMode(p.AuditFailureMode, "") == "" {
		return fmt.Errorf("audit_failure_mode has invalid value %q", p.AuditFailureMode)
	}
	return nil
}

func normalizeDecision(d, fallback Decision) Decision {
	switch d {
	case DecisionAllow, DecisionDeny, DecisionAsk:
		return d
	case "needs_human_review":
		return DecisionAsk
	case "":
		return fallback
	default:
		return fallback
	}
}

func normalizeAuditFailureMode(mode, fallback AuditFailureMode) AuditFailureMode {
	switch mode {
	case AuditBestEffort, AuditFailClosed:
		return mode
	case "":
		return fallback
	default:
		return fallback
	}
}
