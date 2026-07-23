//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultMaxTimeoutSeconds = 300
	defaultMaxOutputBytes    = 1 << 20
	defaultMaxCommandBytes   = 64 << 10
	defaultMaxScriptBytes    = 1 << 20
	defaultMaxSessionBytes   = 64 << 10
	defaultMaxScriptLines    = 5000
	defaultMaxSleepSeconds   = 30
)

// Policy is the versioned, serializable configuration for Guard.
type Policy struct {
	Version     string            `json:"version" yaml:"version"`
	PolicyID    string            `json:"policy_id" yaml:"policy_id"`
	Commands    CommandPolicy     `json:"commands" yaml:"commands"`
	Paths       PathPolicy        `json:"paths" yaml:"paths"`
	Network     NetworkPolicy     `json:"network" yaml:"network"`
	Environment EnvironmentPolicy `json:"environment" yaml:"environment"`
	Limits      LimitsPolicy      `json:"limits" yaml:"limits"`
	HostExec    HostExecPolicy    `json:"hostexec" yaml:"hostexec"`
	Actions     ActionPolicy      `json:"actions" yaml:"actions"`
}

// CommandPolicy controls executable names after conservative shell parsing.
type CommandPolicy struct {
	Allowed []string `json:"allowed" yaml:"allowed"`
	Denied  []string `json:"denied" yaml:"denied"`
	Review  []string `json:"review" yaml:"review"`
}

// PathPolicy controls paths that execution requests must not access.
type PathPolicy struct {
	Denied []string `json:"denied" yaml:"denied"`
}

// NetworkPolicy controls external network command destinations.
type NetworkPolicy struct {
	Commands       []string              `json:"commands" yaml:"commands"`
	AllowedDomains []string              `json:"allowed_domains" yaml:"allowed_domains"`
	DefaultAction  tool.PermissionAction `json:"default_action" yaml:"default_action"`
}

// EnvironmentPolicy controls environment keys passed to execution backends.
type EnvironmentPolicy struct {
	AllowedVariables []string `json:"allowed_variables" yaml:"allowed_variables"`
	DeniedVariables  []string `json:"denied_variables" yaml:"denied_variables"`
}

// LimitsPolicy controls request-side resource declarations and input sizes.
type LimitsPolicy struct {
	MaxTimeoutSeconds    int   `json:"max_timeout_seconds" yaml:"max_timeout_seconds"`
	MaxOutputBytes       int64 `json:"max_output_bytes" yaml:"max_output_bytes"`
	MaxCommandBytes      int   `json:"max_command_bytes" yaml:"max_command_bytes"`
	MaxScriptBytes       int   `json:"max_script_bytes" yaml:"max_script_bytes"`
	MaxSessionInputBytes int   `json:"max_session_input_bytes" yaml:"max_session_input_bytes"`
	MaxScriptLines       int   `json:"max_script_lines" yaml:"max_script_lines"`
	MaxSleepSeconds      int   `json:"max_sleep_seconds" yaml:"max_sleep_seconds"`
}

// HostExecPolicy applies stricter review rules to direct host execution.
type HostExecPolicy struct {
	AllowBackground   bool `json:"allow_background" yaml:"allow_background"`
	AllowPTY          bool `json:"allow_pty" yaml:"allow_pty"`
	MaxTimeoutSeconds int  `json:"max_timeout_seconds" yaml:"max_timeout_seconds"`
}

// ActionPolicy controls fail-closed behavior for requests that cannot be
// classified conclusively. AuditFailure is retained as an observable policy
// field; audit failures never silently turn an unsafe request into allow.
type ActionPolicy struct {
	Unparsable       tool.PermissionAction `json:"unparsable" yaml:"unparsable"`
	UnlistedCommand  tool.PermissionAction `json:"unlisted_command" yaml:"unlisted_command"`
	UnknownScript    tool.PermissionAction `json:"unknown_script" yaml:"unknown_script"`
	DependencyChange tool.PermissionAction `json:"dependency_change" yaml:"dependency_change"`
	AuditFailure     tool.PermissionAction `json:"audit_failure" yaml:"audit_failure"`
}

// DefaultPolicy returns conservative defaults suitable for an execution
// guard. Operators should explicitly populate commands.allowed and
// network.allowed_domains for their deployment.
func DefaultPolicy() Policy {
	return Policy{
		Version:  "v1",
		PolicyID: "default",
		Paths: PathPolicy{Denied: []string{
			"~/.ssh", ".ssh", ".env", "credentials", ".aws/credentials",
			".config/gcloud/application_default_credentials.json",
		}},
		Network: NetworkPolicy{
			Commands:      []string{"curl", "wget", "nc", "netcat", "ssh", "scp", "sftp"},
			DefaultAction: tool.PermissionActionDeny,
		},
		Environment: EnvironmentPolicy{DeniedVariables: []string{
			"LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES",
			"DYLD_LIBRARY_PATH", "DYLD_FORCE_FLAT_NAMESPACE", "BASH_ENV",
			"ENV", "PROMPT_COMMAND", "PYTHONSTARTUP", "NODE_OPTIONS",
		}},
		Limits: LimitsPolicy{
			MaxTimeoutSeconds:    defaultMaxTimeoutSeconds,
			MaxOutputBytes:       defaultMaxOutputBytes,
			MaxCommandBytes:      defaultMaxCommandBytes,
			MaxScriptBytes:       defaultMaxScriptBytes,
			MaxSessionInputBytes: defaultMaxSessionBytes,
			MaxScriptLines:       defaultMaxScriptLines,
			MaxSleepSeconds:      defaultMaxSleepSeconds,
		},
		HostExec: HostExecPolicy{MaxTimeoutSeconds: 120},
		Actions: ActionPolicy{
			Unparsable:       tool.PermissionActionAsk,
			UnlistedCommand:  tool.PermissionActionAsk,
			UnknownScript:    tool.PermissionActionAsk,
			DependencyChange: tool.PermissionActionAsk,
			AuditFailure:     tool.PermissionActionAllow,
		},
	}
}

// LoadPolicyFile loads a strict YAML or JSON policy. Unknown fields, trailing
// documents, invalid actions, and malformed values are rejected at startup.
func LoadPolicyFile(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read tool safety policy: %w", err)
	}
	p := DefaultPolicy()
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" || firstNonSpace(data) == '{' {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return Policy{}, fmt.Errorf("decode tool safety JSON: %w", err)
		}
		if err := requireJSONEOF(dec); err != nil {
			return Policy{}, err
		}
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&p); err != nil {
			return Policy{}, fmt.Errorf("decode tool safety YAML: %w", err)
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			if err == nil {
				return Policy{}, errors.New("decode tool safety YAML: multiple documents are not allowed")
			}
			return Policy{}, fmt.Errorf("decode tool safety YAML: %w", err)
		}
	}
	return normalizeAndValidatePolicy(p)
}

func firstNonSpace(data []byte) byte {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b
		}
	}
	return 0
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode tool safety JSON: trailing JSON value is not allowed")
		}
		return fmt.Errorf("decode tool safety JSON: %w", err)
	}
	return nil
}

func normalizeAndValidatePolicy(p Policy) (Policy, error) {
	defaults := DefaultPolicy()
	if strings.TrimSpace(p.Version) == "" {
		p.Version = defaults.Version
	}
	if p.Version != "v1" {
		return Policy{}, fmt.Errorf("unsupported tool safety policy version %q", p.Version)
	}
	if strings.TrimSpace(p.PolicyID) == "" {
		p.PolicyID = defaults.PolicyID
	}
	p.PolicyID = strings.TrimSpace(p.PolicyID)
	applyPolicyDefaults(&p, defaults)
	normalizePolicyLists(&p)
	if err := validatePolicy(p); err != nil {
		return Policy{}, err
	}
	return p, nil
}

func applyPolicyDefaults(p *Policy, defaults Policy) {
	if p.Paths.Denied == nil {
		p.Paths.Denied = defaults.Paths.Denied
	}
	if p.Network.Commands == nil {
		p.Network.Commands = defaults.Network.Commands
	}
	if p.Environment.DeniedVariables == nil {
		p.Environment.DeniedVariables = defaults.Environment.DeniedVariables
	}
	if p.Limits.MaxTimeoutSeconds == 0 {
		p.Limits.MaxTimeoutSeconds = defaults.Limits.MaxTimeoutSeconds
	}
	if p.Limits.MaxOutputBytes == 0 {
		p.Limits.MaxOutputBytes = defaults.Limits.MaxOutputBytes
	}
	if p.Limits.MaxCommandBytes == 0 {
		p.Limits.MaxCommandBytes = defaults.Limits.MaxCommandBytes
	}
	if p.Limits.MaxScriptBytes == 0 {
		p.Limits.MaxScriptBytes = defaults.Limits.MaxScriptBytes
	}
	if p.Limits.MaxSessionInputBytes == 0 {
		p.Limits.MaxSessionInputBytes = defaults.Limits.MaxSessionInputBytes
	}
	if p.Limits.MaxScriptLines == 0 {
		p.Limits.MaxScriptLines = defaults.Limits.MaxScriptLines
	}
	if p.Limits.MaxSleepSeconds == 0 {
		p.Limits.MaxSleepSeconds = defaults.Limits.MaxSleepSeconds
	}
	if p.HostExec.MaxTimeoutSeconds == 0 {
		p.HostExec.MaxTimeoutSeconds = defaults.HostExec.MaxTimeoutSeconds
	}
	if p.Network.DefaultAction == "" {
		p.Network.DefaultAction = defaults.Network.DefaultAction
	}
	if p.Actions.Unparsable == "" {
		p.Actions.Unparsable = defaults.Actions.Unparsable
	}
	if p.Actions.UnlistedCommand == "" {
		p.Actions.UnlistedCommand = defaults.Actions.UnlistedCommand
	}
	if p.Actions.UnknownScript == "" {
		p.Actions.UnknownScript = defaults.Actions.UnknownScript
	}
	if p.Actions.DependencyChange == "" {
		p.Actions.DependencyChange = defaults.Actions.DependencyChange
	}
	if p.Actions.AuditFailure == "" {
		p.Actions.AuditFailure = defaults.Actions.AuditFailure
	}
}

func normalizePolicyLists(p *Policy) {

	p.Commands.Allowed = cleanPolicyList(p.Commands.Allowed)
	p.Commands.Denied = cleanPolicyList(p.Commands.Denied)
	p.Commands.Review = cleanPolicyList(p.Commands.Review)
	p.Paths.Denied = cleanPolicyList(p.Paths.Denied)
	p.Network.Commands = cleanPolicyList(p.Network.Commands)
	p.Network.AllowedDomains = cleanPolicyList(p.Network.AllowedDomains)
	p.Environment.AllowedVariables = cleanPolicyList(p.Environment.AllowedVariables)
	p.Environment.DeniedVariables = cleanPolicyList(p.Environment.DeniedVariables)

}

func validatePolicy(p Policy) error {
	if p.Limits.MaxTimeoutSeconds <= 0 || p.Limits.MaxOutputBytes <= 0 ||
		p.Limits.MaxCommandBytes <= 0 || p.Limits.MaxScriptBytes <= 0 ||
		p.Limits.MaxSessionInputBytes <= 0 || p.Limits.MaxScriptLines <= 0 ||
		p.Limits.MaxSleepSeconds <= 0 || p.HostExec.MaxTimeoutSeconds <= 0 {
		return errors.New("tool safety limits must be positive")
	}
	actions := []struct {
		name       string
		action     tool.PermissionAction
		failClosed bool
	}{
		{name: "network.default_action", action: p.Network.DefaultAction, failClosed: true},
		{name: "actions.unparsable", action: p.Actions.Unparsable, failClosed: true},
		{name: "actions.unlisted_command", action: p.Actions.UnlistedCommand},
		{name: "actions.unknown_script", action: p.Actions.UnknownScript, failClosed: true},
		{name: "actions.dependency_change", action: p.Actions.DependencyChange},
		{name: "actions.audit_failure", action: p.Actions.AuditFailure},
	}
	for _, entry := range actions {
		if err := validateAction(entry.action); err != nil {
			return fmt.Errorf("%s: %w", entry.name, err)
		}
		if entry.failClosed && entry.action == tool.PermissionActionAllow {
			return fmt.Errorf("%s must be deny or ask", entry.name)
		}
	}
	for _, domain := range p.Network.AllowedDomains {
		if err := validateDomainPattern(domain); err != nil {
			return err
		}
	}
	return nil
}

func validateAction(action tool.PermissionAction) error {
	switch action {
	case tool.PermissionActionAllow, tool.PermissionActionDeny, tool.PermissionActionAsk:
		return nil
	default:
		return fmt.Errorf("invalid permission action %q", action)
	}
}

func validateDomainPattern(domain string) error {
	d := strings.TrimSpace(domain)
	if strings.Contains(d, "://") || strings.ContainsAny(d, "/\\@ ") {
		return fmt.Errorf("allowed domain %q must be a host or *.host pattern", domain)
	}
	if strings.HasPrefix(d, "*.") {
		d = strings.TrimPrefix(d, "*.")
	}
	if d == "" || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return fmt.Errorf("invalid allowed domain %q", domain)
	}
	return nil
}

func cleanPolicyList(values []string) []string {
	if values == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}
