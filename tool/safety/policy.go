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
	"net"
	"strings"

	"gopkg.in/yaml.v3"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// PolicyFormatAuto detects JSON by its opening brace and otherwise uses YAML.
	PolicyFormatAuto = "auto"
	// PolicyFormatJSON selects strict JSON policy parsing.
	PolicyFormatJSON = "json"
	// PolicyFormatYAML selects strict YAML policy parsing.
	PolicyFormatYAML = "yaml"
)

// DefaultPolicy returns the built-in policy with every safety rule enabled.
func DefaultPolicy() Policy {
	return Policy{
		Version:       CurrentPolicyVersion,
		DefaultAction: tool.PermissionActionAllow,
	}
}

// ParsePolicy strictly decodes and validates a policy. Unknown fields, duplicate
// YAML keys, trailing documents, and invalid values are rejected.
func ParsePolicy(data []byte, format string) (Policy, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Policy{}, errors.New("safety: policy is empty")
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" || format == PolicyFormatAuto {
		trimmed := bytes.TrimSpace(data)
		if trimmed[0] == '{' {
			format = PolicyFormatJSON
		} else {
			format = PolicyFormatYAML
		}
	}
	var p Policy
	switch format {
	case PolicyFormatJSON:
		if err := validateJSONNoDuplicateKeys(data); err != nil {
			return Policy{}, fmt.Errorf("safety: validate JSON policy: %w", err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return Policy{}, fmt.Errorf("safety: decode JSON policy: %w", err)
		}
		if err := rejectTrailingJSON(dec); err != nil {
			return Policy{}, err
		}
	case PolicyFormatYAML, "yml":
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&p); err != nil {
			return Policy{}, fmt.Errorf("safety: decode YAML policy: %w", err)
		}
		var trailing any
		if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				return Policy{}, errors.New("safety: multiple YAML documents are not allowed")
			}
			return Policy{}, fmt.Errorf("safety: decode trailing YAML: %w", err)
		}
	default:
		return Policy{}, fmt.Errorf("safety: unsupported policy format %q", format)
	}
	if err := ValidatePolicy(p); err != nil {
		return Policy{}, err
	}
	return clonePolicy(p), nil
}

func validateJSONNoDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSONValue(dec); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value is not allowed")
		}
		return err
	}
	return nil
}

func walkJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := walkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func rejectTrailingJSON(dec *json.Decoder) error {
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("safety: trailing JSON value is not allowed")
		}
		return fmt.Errorf("safety: decode trailing JSON: %w", err)
	}
	return nil
}

// ValidatePolicy validates a programmatically constructed policy.
func ValidatePolicy(p Policy) error {
	if p.Version != CurrentPolicyVersion {
		return fmt.Errorf("safety: policy version must be %d", CurrentPolicyVersion)
	}
	if p.DefaultAction == "" {
		p.DefaultAction = tool.PermissionActionAllow
	}
	if err := validateAction("default_action", p.DefaultAction); err != nil {
		return err
	}
	if err := validateRules(p.Rules); err != nil {
		return err
	}
	for name, profile := range p.Profiles {
		if err := validateProfile(name, profile); err != nil {
			return err
		}
	}
	return nil
}

func validateRules(builtin BuiltinRules) error {
	rules := []struct {
		name string
		rule RulePolicy
	}{
		{"dangerous_command", builtin.DangerousCommand},
		{"sensitive_path", builtin.SensitivePath},
		{"network_access", builtin.NetworkAccess},
		{"shell_bypass", builtin.ShellBypass},
		{"host_execution", builtin.HostExecution},
		{"dependency_change", builtin.DependencyChange},
		{"resource_abuse", builtin.ResourceAbuse},
		{"secret_exposure", builtin.SecretExposure},
	}
	for _, item := range rules {
		if item.rule.Action != "" {
			if err := validateAction("rules."+item.name+".action", item.rule.Action); err != nil {
				return err
			}
		}
		if item.name == "secret_exposure" && item.rule.Enabled != nil && !*item.rule.Enabled {
			return errors.New("safety: secret_exposure cannot be disabled")
		}
		if item.name == "secret_exposure" && item.rule.Action == tool.PermissionActionAllow {
			return errors.New("safety: secret_exposure action cannot be allow")
		}
	}
	return nil
}

func validateProfile(name string, profile ToolProfile) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("safety: profile name cannot be empty")
	}
	for _, domain := range profile.AllowedDomains {
		if err := validateDomainPattern(domain); err != nil {
			return fmt.Errorf("safety: profile %q: %w", name, err)
		}
	}
	if hasBlank(profile.AllowedCommands) || hasBlank(profile.DeniedCommands) {
		return fmt.Errorf("safety: profile %q contains a blank command", name)
	}
	if hasBlank(profile.ForbiddenPaths) || hasBlank(profile.AllowedEnv) {
		return fmt.Errorf("safety: profile %q contains a blank path or environment name", name)
	}
	if profile.MaxTimeout < 0 || profile.MaxOutputBytes < 0 {
		return fmt.Errorf("safety: profile %q limits cannot be negative", name)
	}
	return nil
}

func validateAction(field string, action tool.PermissionAction) error {
	switch action {
	case tool.PermissionActionAllow, tool.PermissionActionAsk, tool.PermissionActionDeny:
		return nil
	default:
		return fmt.Errorf("safety: %s has invalid action %q", field, action)
	}
}

func validateDomainPattern(pattern string) error {
	p := strings.TrimSpace(pattern)
	if p == "" || p != strings.ToLower(p) {
		return fmt.Errorf("domain %q must be non-empty lower case", pattern)
	}
	if net.ParseIP(p) != nil {
		return nil
	}
	if strings.ContainsAny(p, "/:@?#") {
		return fmt.Errorf("domain %q must not contain a scheme, port, path, or userinfo", pattern)
	}
	host := p
	if strings.HasPrefix(host, "*.") {
		host = strings.TrimPrefix(host, "*.")
		if host == "" || strings.Contains(host, "*") {
			return fmt.Errorf("domain %q has an invalid wildcard", pattern)
		}
	} else if strings.Contains(host, "*") {
		return fmt.Errorf("domain %q only supports a leading *. wildcard", pattern)
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("domain %q is invalid", pattern)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("domain %q contains an invalid character", pattern)
			}
		}
	}
	return nil
}

func hasBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func clonePolicy(p Policy) Policy {
	out := p
	if out.DefaultAction == "" {
		out.DefaultAction = tool.PermissionActionAllow
	}
	out.Profiles = make(map[string]ToolProfile, len(p.Profiles))
	for name, profile := range p.Profiles {
		profile.AllowedDomains = append([]string(nil), profile.AllowedDomains...)
		profile.DeniedCommands = append([]string(nil), profile.DeniedCommands...)
		profile.AllowedCommands = append([]string(nil), profile.AllowedCommands...)
		profile.ForbiddenPaths = append([]string(nil), profile.ForbiddenPaths...)
		profile.AllowedEnv = append([]string(nil), profile.AllowedEnv...)
		out.Profiles[name] = profile
	}
	return out
}
