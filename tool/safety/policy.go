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
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	currentPolicyVersion     = 1
	defaultMaxTimeoutSeconds = 300
	defaultMaxInputBytes     = 1 << 20
	defaultMaxOutputBytes    = 1 << 20
	defaultMaxConcurrency    = 16
)

// Policy defines the configurable command, path, network, resource, and
// environment boundaries enforced by Scanner.
type Policy struct {
	Version                     int           `json:"version" yaml:"version"`
	AllowedCommands             []string      `json:"allowed_commands,omitempty" yaml:"allowed_commands,omitempty"`
	DeniedCommands              []string      `json:"denied_commands,omitempty" yaml:"denied_commands,omitempty"`
	ForbiddenPaths              []string      `json:"forbidden_paths,omitempty" yaml:"forbidden_paths,omitempty"`
	Network                     NetworkPolicy `json:"network,omitempty" yaml:"network,omitempty"`
	Limits                      Limits        `json:"limits,omitempty" yaml:"limits,omitempty"`
	AllowedEnvironmentVariables []string      `json:"allowed_environment_variables,omitempty" yaml:"allowed_environment_variables,omitempty"`
	Actions                     Actions       `json:"actions,omitempty" yaml:"actions,omitempty"`
}

// NetworkPolicy controls outbound targets found in URL arguments and known
// network-client command arguments.
type NetworkPolicy struct {
	AllowedDomains []string `json:"allowed_domains,omitempty" yaml:"allowed_domains,omitempty"`
	DenyByDefault  bool     `json:"deny_by_default" yaml:"deny_by_default"`
}

// Limits controls request time, retained output, and declared concurrency.
// A wrapper enforces MaxOutputBytes after execution; the other limits are
// evaluated before execution.
type Limits struct {
	MaxTimeoutSeconds int `json:"max_timeout_seconds" yaml:"max_timeout_seconds"`
	MaxInputBytes     int `json:"max_input_bytes" yaml:"max_input_bytes"`
	MaxOutputBytes    int `json:"max_output_bytes" yaml:"max_output_bytes"`
	MaxConcurrency    int `json:"max_concurrency" yaml:"max_concurrency"`
}

// Actions controls whether reviewable findings are denied or sent to a human.
// Unparsable must be ask or deny; a parser failure can never default to allow.
type Actions struct {
	Unparsable        Decision `json:"unparsable" yaml:"unparsable"`
	CommandNotAllowed Decision `json:"command_not_allowed" yaml:"command_not_allowed"`
	DependencyChange  Decision `json:"dependency_change" yaml:"dependency_change"`
	HostBackground    Decision `json:"host_background" yaml:"host_background"`
	HostTTY           Decision `json:"host_tty" yaml:"host_tty"`
}

// DefaultPolicy returns conservative defaults suitable as a starting point.
// Callers should still customize allowed domains, commands, paths, and
// environment variables for their deployment.
func DefaultPolicy() Policy {
	return Policy{
		Version: currentPolicyVersion,
		DeniedCommands: []string{
			"dd", "mkfs", "mount", "nc", "netcat", "rm", "scp", "ssh",
			"sudo", "su", "wget",
		},
		ForbiddenPaths: []string{
			"/etc", "/proc", "/root", "/sys", "~/.ssh", ".env",
			".aws/credentials", ".config/gcloud", ".netrc", ".npmrc",
		},
		Network: NetworkPolicy{DenyByDefault: true},
		Limits: Limits{
			MaxTimeoutSeconds: defaultMaxTimeoutSeconds,
			MaxInputBytes:     defaultMaxInputBytes,
			MaxOutputBytes:    defaultMaxOutputBytes,
			MaxConcurrency:    defaultMaxConcurrency,
		},
		AllowedEnvironmentVariables: []string{
			"CI", "GOCACHE", "GOMODCACHE", "HOME", "LANG", "PATH", "TMPDIR",
		},
		Actions: defaultActions(),
	}
}

// LoadPolicy loads a YAML or JSON policy file and rejects unknown fields. A
// file can change policy behavior without recompiling the application.
func LoadPolicy(path string) (Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return Policy{}, fmt.Errorf("open tool safety policy: %w", err)
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	policy := Policy{
		Network: NetworkPolicy{DenyByDefault: true},
	}
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("decode tool safety policy: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Policy{}, errors.New(
				"decode tool safety policy: multiple documents are not allowed",
			)
		}
		return Policy{}, fmt.Errorf("decode tool safety policy: %w", err)
	}
	return normalizePolicy(policy)
}

func normalizePolicy(policy Policy) (Policy, error) {
	if policy.Version != currentPolicyVersion {
		return Policy{}, fmt.Errorf(
			"unsupported tool safety policy version %d", policy.Version,
		)
	}
	if policy.Limits.MaxTimeoutSeconds < 0 ||
		policy.Limits.MaxInputBytes < 0 ||
		policy.Limits.MaxOutputBytes < 0 ||
		policy.Limits.MaxConcurrency < 0 {
		return Policy{}, errors.New("tool safety policy limits cannot be negative")
	}
	if policy.Limits.MaxTimeoutSeconds == 0 {
		policy.Limits.MaxTimeoutSeconds = defaultMaxTimeoutSeconds
	}
	if policy.Limits.MaxOutputBytes == 0 {
		policy.Limits.MaxOutputBytes = defaultMaxOutputBytes
	}
	if policy.Limits.MaxInputBytes == 0 {
		policy.Limits.MaxInputBytes = defaultMaxInputBytes
	}
	if policy.Limits.MaxConcurrency == 0 {
		policy.Limits.MaxConcurrency = defaultMaxConcurrency
	}

	policy.AllowedCommands = normalizedList(policy.AllowedCommands, false)
	policy.DeniedCommands = normalizedList(policy.DeniedCommands, false)
	policy.ForbiddenPaths = normalizedList(policy.ForbiddenPaths, false)
	policy.AllowedEnvironmentVariables = normalizedList(
		policy.AllowedEnvironmentVariables,
		false,
	)
	policy.Network.AllowedDomains = normalizedList(
		policy.Network.AllowedDomains,
		true,
	)
	for _, domain := range policy.Network.AllowedDomains {
		if strings.ContainsAny(domain, "/:@") {
			return Policy{}, fmt.Errorf(
				"network allowed domain %q must be a hostname", domain,
			)
		}
	}

	defaults := defaultActions()
	fillDecision(&policy.Actions.Unparsable, defaults.Unparsable)
	fillDecision(
		&policy.Actions.CommandNotAllowed,
		defaults.CommandNotAllowed,
	)
	fillDecision(
		&policy.Actions.DependencyChange,
		defaults.DependencyChange,
	)
	fillDecision(&policy.Actions.HostBackground, defaults.HostBackground)
	fillDecision(&policy.Actions.HostTTY, defaults.HostTTY)
	if policy.Actions.Unparsable == DecisionAllow {
		return Policy{}, errors.New(
			"actions.unparsable must be ask or deny",
		)
	}
	for name, action := range map[string]Decision{
		"unparsable":          policy.Actions.Unparsable,
		"command_not_allowed": policy.Actions.CommandNotAllowed,
		"dependency_change":   policy.Actions.DependencyChange,
		"host_background":     policy.Actions.HostBackground,
		"host_tty":            policy.Actions.HostTTY,
	} {
		if !validDecision(action) {
			return Policy{}, fmt.Errorf(
				"actions.%s has invalid decision %q", name, action,
			)
		}
	}
	return policy, nil
}

func defaultActions() Actions {
	return Actions{
		Unparsable:        DecisionAsk,
		CommandNotAllowed: DecisionAsk,
		DependencyChange:  DecisionAsk,
		HostBackground:    DecisionDeny,
		HostTTY:           DecisionAsk,
	}
}

func fillDecision(dst *Decision, fallback Decision) {
	if *dst == "" {
		*dst = fallback
	}
}

func validDecision(decision Decision) bool {
	switch decision {
	case DecisionAllow, DecisionDeny, DecisionAsk:
		return true
	default:
		return false
	}
}

func normalizedList(values []string, lower bool) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.TrimSuffix(strings.ToLower(value), ".")
			value = strings.TrimPrefix(value, "*.")
		}
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
