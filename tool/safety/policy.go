// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// Policy is the declarative safety policy loaded from a YAML file.
// It defines the allow/deny lists, resource limits, and environment
// constraints that the Safety Guard applies to every tool call.
//
// The policy is designed to be hot-reloadable: an operator can edit
// the YAML file and call [Policy.Reload] without restarting the
// process. All fields carry explicit yaml tags so the on-disk key
// names are stable and do not depend on Go field naming conventions.
type Policy struct {
	// AllowedCommands is the explicit allow-list of executable names
	// (or paths) that may be invoked by a tool. When non-empty, any
	// command whose argv[0] is not in this list is denied.
	AllowedCommands []string `yaml:"allowed_commands"`

	// DeniedCommands is the explicit deny-list of executable names
	// (or paths). A match here causes an unconditional deny, overriding
	// the allow-list.
	DeniedCommands []string `yaml:"denied_commands"`

	// ForbiddenPaths is a list of file system path patterns that tools
	// must not read, write, or execute. Patterns use Go regexp syntax
	// and are matched against the absolute path.
	ForbiddenPaths []string `yaml:"forbidden_paths"`

	// NetworkWhitelist is a list of allowed exact hosts, explicit
	// "*.example.com" subdomain wildcards, host:port entries, or CIDRs.
	// A detected network target outside this list, or a missing list, follows
	// NetworkFailureDecision. Similar-looking domains never match by prefix.
	NetworkWhitelist []string `yaml:"network_whitelist"`

	// NetworkFailureDecision controls the static-scan decision for a detected
	// network client whose destination is unknown, not whitelisted, or whose
	// whitelist is unconfigured. Only "ask" opts into review; every other
	// value, including the zero value, fails closed as "deny".
	NetworkFailureDecision Decision `yaml:"network_failure_decision"`

	// MaxTimeoutMS is the maximum wall-clock time (in milliseconds) a
	// tool call may take before the guard considers it suspicious.
	// A value of 0 means no timeout enforcement.
	MaxTimeoutMS int64 `yaml:"max_timeout_ms"`

	// MaxOutputBytes is the maximum number of bytes a tool may produce
	// on stdout/stderr before the output is truncated or flagged.
	// A value of 0 means no output limit.
	MaxOutputBytes int64 `yaml:"max_output_bytes"`

	// EnvWhitelist is the list of environment variable names that a
	// tool is allowed to access. When non-empty, access to any
	// variable not in this list is denied.
	EnvWhitelist []string `yaml:"env_whitelist"`

	// mu guards the fields above during hot-reload. Callers that read
	// the policy concurrently with a Reload must hold the read lock.
	mu sync.RWMutex `yaml:"-"`
}

// LoadPolicy reads and parses a YAML policy file at path, returning
// a *Policy ready for use. The returned Policy is safe for concurrent
// reads once loaded.
func LoadPolicy(path string) (*Policy, error) {
	return loadPolicyFromFile(path)
}

// Reload re-reads the YAML file at path and updates the receiver in
// place. This allows operators to change the policy at runtime without
// restarting the process or re-wiring consumers that already hold a
// *Policy reference.
//
// If the new file fails to parse, the existing policy is left
// unchanged so the guard continues to enforce the last known-good
// rules.
func (p *Policy) Reload(path string) error {
	np, err := loadPolicyFromFile(path)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.AllowedCommands = np.AllowedCommands
	p.DeniedCommands = np.DeniedCommands
	p.ForbiddenPaths = np.ForbiddenPaths
	p.NetworkWhitelist = np.NetworkWhitelist
	p.NetworkFailureDecision = np.NetworkFailureDecision
	p.MaxTimeoutMS = np.MaxTimeoutMS
	p.MaxOutputBytes = np.MaxOutputBytes
	p.EnvWhitelist = np.EnvWhitelist
	return nil
}

// loadPolicyFromFile is the shared implementation for LoadPolicy and
// Reload. It reads the file, validates that it is non-empty, and
// unmarshals it into a new Policy.
func loadPolicyFromFile(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("safety: read policy %q: %w", path, err)
	}
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("safety: parse policy %q: %w", path, err)
	}
	return &p, nil
}
