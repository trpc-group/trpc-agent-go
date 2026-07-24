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
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultMaxTimeoutSec  = 300
	defaultMaxOutputBytes = 1 << 20
	defaultMaxCommandSize = 16 * 1024
	defaultMaxScriptSize  = 1 << 20
)

// AuditFailureMode controls whether audit write failures can fail safe calls.
type AuditFailureMode string

// Audit failure modes.
const (
	AuditFailureModeBestEffort AuditFailureMode = "best_effort"
	AuditFailureModeStrict     AuditFailureMode = "strict"
)

// Policy configures the safety scanner.
type Policy struct {
	AllowedCommands         []string         `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands          []string         `json:"denied_commands" yaml:"denied_commands"`
	DeniedPaths             []string         `json:"denied_paths" yaml:"denied_paths"`
	DisableDefaultDenies    bool             `json:"disable_default_denies" yaml:"disable_default_denies"`
	NetworkAllowlist        []string         `json:"network_allowlist" yaml:"network_allowlist"`
	MaxTimeoutSec           int              `json:"max_timeout_sec" yaml:"max_timeout_sec"`
	MaxOutputBytes          int64            `json:"max_output_bytes" yaml:"max_output_bytes"`
	MaxCommandBytes         int              `json:"max_command_bytes" yaml:"max_command_bytes"`
	MaxScriptBytes          int              `json:"max_script_bytes" yaml:"max_script_bytes"`
	EnvAllowlist            []string         `json:"env_allowlist" yaml:"env_allowlist"`
	DependencyInstallAction Decision         `json:"dependency_install_action" yaml:"dependency_install_action"`
	UnparsableShellAction   Decision         `json:"unparsable_shell_action" yaml:"unparsable_shell_action"`
	HostUnparsableAction    Decision         `json:"host_unparsable_shell_action" yaml:"host_unparsable_shell_action"`
	SecretAction            Decision         `json:"secret_action" yaml:"secret_action"`
	AuditFailureMode        AuditFailureMode `json:"audit_failure_mode" yaml:"audit_failure_mode"`
}

// DefaultPolicy returns a conservative, backward-compatible policy. It is only
// applied when callers explicitly enable the safety scanner.
func DefaultPolicy() Policy {
	return Policy{
		DeniedCommands: []string{
			"rm", "rmdir", "del", "erase", "format",
			"sudo", "su", "doas", "nc", "netcat", "ssh", "scp",
		},
		DeniedPaths: []string{
			"~/.ssh", ".ssh/id_rsa", ".ssh/id_ed25519",
			".env", ".env.local", "/etc/passwd", "/etc/shadow",
			"credentials", "credential", "secret", "secrets",
		},
		MaxTimeoutSec:           defaultMaxTimeoutSec,
		MaxOutputBytes:          defaultMaxOutputBytes,
		MaxCommandBytes:         defaultMaxCommandSize,
		MaxScriptBytes:          defaultMaxScriptSize,
		DependencyInstallAction: DecisionAsk,
		UnparsableShellAction:   DecisionAsk,
		HostUnparsableAction:    DecisionDeny,
		SecretAction:            DecisionDeny,
		AuditFailureMode:        AuditFailureModeBestEffort,
	}
}

// WithDefaults fills zero fields with default policy values.
func (p Policy) WithDefaults() Policy {
	d := DefaultPolicy()
	if p.AllowedCommands != nil {
		d.AllowedCommands = cleanStringList(p.AllowedCommands)
	}
	if p.DisableDefaultDenies {
		d.DeniedCommands = cleanStringList(p.DeniedCommands)
		d.DeniedPaths = cleanStringList(p.DeniedPaths)
	} else {
		if p.DeniedCommands != nil && len(cleanStringList(p.DeniedCommands)) > 0 {
			d.DeniedCommands = cleanStringList(p.DeniedCommands)
		}
		if p.DeniedPaths != nil && len(cleanStringList(p.DeniedPaths)) > 0 {
			d.DeniedPaths = cleanStringList(p.DeniedPaths)
		}
	}
	d.DisableDefaultDenies = p.DisableDefaultDenies
	if p.NetworkAllowlist != nil {
		d.NetworkAllowlist = cleanStringList(p.NetworkAllowlist)
	}
	if p.MaxTimeoutSec != 0 {
		d.MaxTimeoutSec = p.MaxTimeoutSec
	}
	if p.MaxOutputBytes != 0 {
		d.MaxOutputBytes = p.MaxOutputBytes
	}
	if p.MaxCommandBytes != 0 {
		d.MaxCommandBytes = p.MaxCommandBytes
	}
	if p.MaxScriptBytes != 0 {
		d.MaxScriptBytes = p.MaxScriptBytes
	}
	if p.EnvAllowlist != nil {
		d.EnvAllowlist = cleanStringList(p.EnvAllowlist)
	}
	if p.DependencyInstallAction != "" {
		d.DependencyInstallAction = p.DependencyInstallAction
	}
	if p.UnparsableShellAction != "" {
		d.UnparsableShellAction = p.UnparsableShellAction
	}
	if p.HostUnparsableAction != "" {
		d.HostUnparsableAction = p.HostUnparsableAction
	}
	if p.SecretAction != "" {
		d.SecretAction = p.SecretAction
	}
	if p.AuditFailureMode != "" {
		d.AuditFailureMode = p.AuditFailureMode
	}
	return d
}

// Validate rejects invalid policy values.
func (p Policy) Validate() error {
	decisions := map[string]Decision{
		"dependency_install_action":    p.DependencyInstallAction,
		"unparsable_shell_action":      p.UnparsableShellAction,
		"host_unparsable_shell_action": p.HostUnparsableAction,
		"secret_action":                p.SecretAction,
	}
	for name, decision := range decisions {
		if !decision.Valid() {
			return fmt.Errorf("%s: invalid decision %q", name, decision)
		}
	}
	switch p.AuditFailureMode {
	case AuditFailureModeBestEffort, AuditFailureModeStrict:
	default:
		return fmt.Errorf("audit_failure_mode: invalid value %q", p.AuditFailureMode)
	}
	if p.MaxTimeoutSec < 0 {
		return fmt.Errorf("max_timeout_sec must be >= 0")
	}
	if p.MaxOutputBytes < 0 {
		return fmt.Errorf("max_output_bytes must be >= 0")
	}
	if p.MaxCommandBytes < 0 {
		return fmt.Errorf("max_command_bytes must be >= 0")
	}
	if p.MaxScriptBytes < 0 {
		return fmt.Errorf("max_script_bytes must be >= 0")
	}
	if p.DisableDefaultDenies {
		return nil
	}
	if len(p.DeniedCommands) == 0 {
		return fmt.Errorf("denied_commands cannot be empty unless disable_default_denies is true")
	}
	if len(p.DeniedPaths) == 0 {
		return fmt.Errorf("denied_paths cannot be empty unless disable_default_denies is true")
	}
	return nil
}

// LoadPolicyJSON loads a strict JSON policy.
func LoadPolicyJSON(r io.Reader) (Policy, error) {
	var p Policy
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Policy{}, err
	}
	if err := requireJSONEOF(dec); err != nil {
		return Policy{}, err
	}
	p = p.WithDefaults()
	return p, p.Validate()
}

// LoadPolicyYAML loads a strict YAML policy.
func LoadPolicyYAML(r io.Reader) (Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return Policy{}, err
	}
	if err := requireYAMLEOF(dec); err != nil {
		return Policy{}, err
	}
	p = p.WithDefaults()
	return p, p.Validate()
}

// LoadPolicyFile loads a JSON or YAML policy file by extension.
func LoadPolicyFile(path string) (Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return Policy{}, err
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return LoadPolicyJSON(f)
	case ".yaml", ".yml":
		return LoadPolicyYAML(f)
	default:
		return Policy{}, fmt.Errorf("unsupported policy file extension %q", filepath.Ext(path))
	}
}

func cleanStringList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	switch {
	case err == io.EOF:
		return nil
	case err == nil:
		return fmt.Errorf("policy JSON must contain exactly one document")
	default:
		return fmt.Errorf("policy JSON must contain exactly one document: %w", err)
	}
}

func requireYAMLEOF(dec *yaml.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	switch {
	case err == io.EOF:
		return nil
	case err == nil:
		return fmt.Errorf("policy YAML must contain exactly one document")
	default:
		return fmt.Errorf("policy YAML must contain exactly one document: %w", err)
	}
}
