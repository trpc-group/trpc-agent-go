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
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DefaultPolicy returns conservative defaults suitable for agent tool calls.
func DefaultPolicy() Policy {
	return Policy{
		AllowedCommands: []string{
			"go", "git", "ls", "pwd", "cat", "sed", "awk", "grep",
			"rg", "find", "echo", "test", "wc", "head", "tail",
			"curl", "wget", "sleep",
		},
		DeniedCommands: []string{
			"rm", "nc", "netcat", "ssh", "scp", "sftp",
		},
		ForbiddenPaths: []string{
			"~/.ssh", ".env", ".npmrc", ".pypirc", ".netrc",
			"/etc/passwd", "/etc/shadow", "/var/run/docker.sock",
			"/proc/self/environ", "/run/secrets",
			"~/.git-credentials", "~/.docker/config.json",
			"~/.kube/config",
			"id_rsa", "id_ed25519", "credentials", "credential",
			"application_default_credentials.json",
		},
		NetworkAllowlist:    []string{"github.com", "proxy.golang.org", "sum.golang.org"},
		EnvAllowlist:        []string{"PATH", "HOME", "TMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH"},
		MaxTimeoutSec:       300,
		MaxOutputBytes:      4 << 20,
		MaxConcurrency:      128,
		ParseFailureAction:  DecisionAsk,
		UnknownToolAction:   DecisionAsk,
		DependencyAction:    DecisionAsk,
		PipelineAction:      DecisionAsk,
		HostPTYAction:       DecisionAsk,
		BackgroundAction:    DecisionAsk,
		DisallowedEnvAction: DecisionAsk,
	}
}

// LoadPolicyFile loads a YAML or JSON safety policy from path.
func LoadPolicyFile(path string) (Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf(
			"load safety policy file %q: %w",
			path,
			err,
		)
	}
	p := DefaultPolicy()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return Policy{}, fmt.Errorf("load safety policy json: %w", err)
		}
		if err := ensureJSONEOF(dec); err != nil {
			return Policy{}, fmt.Errorf("load safety policy json: %w", err)
		}
	default:
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&p); err != nil {
			return Policy{}, fmt.Errorf("load safety policy yaml: %w", err)
		}
		if err := ensureYAMLEOF(dec); err != nil {
			return Policy{}, fmt.Errorf("load safety policy yaml: %w", err)
		}
	}
	p.normalize()
	if err := p.Validate(); err != nil {
		return Policy{}, fmt.Errorf("validate safety policy: %w", err)
	}
	return p, nil
}

// Validate checks policy values that would otherwise silently weaken scanning.
func (p Policy) Validate() error {
	actions := []struct {
		name   string
		action Decision
	}{
		{"parse_failure_action", p.ParseFailureAction},
		{"unknown_tool_action", p.UnknownToolAction},
		{"dependency_action", p.DependencyAction},
		{"pipeline_action", p.PipelineAction},
		{"host_pty_action", p.HostPTYAction},
		{"background_action", p.BackgroundAction},
		{"disallowed_env_action", p.DisallowedEnvAction},
	}
	for _, item := range actions {
		if !validDecision(item.action) {
			return fmt.Errorf("%s has invalid decision %q", item.name, item.action)
		}
	}
	if p.MaxTimeoutSec <= 0 {
		return fmt.Errorf("max_timeout_sec must be positive")
	}
	if p.MaxOutputBytes <= 0 {
		return fmt.Errorf("max_output_bytes must be positive")
	}
	if p.MaxConcurrency <= 0 {
		return fmt.Errorf("max_concurrency must be positive")
	}
	for _, name := range p.EnvAllowlist {
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("env_allowlist contains invalid name %q", name)
		}
	}
	for _, host := range p.NetworkAllowlist {
		if err := validateHost(host); err != nil {
			return fmt.Errorf("network_allowlist: %w", err)
		}
	}
	return nil
}

func (p *Policy) normalize() {
	p.AllowedCommands = cleanStrings(p.AllowedCommands)
	p.DeniedCommands = cleanStrings(p.DeniedCommands)
	p.ForbiddenPaths = cleanStrings(p.ForbiddenPaths)
	p.NetworkAllowlist = cleanStrings(p.NetworkAllowlist)
	p.EnvAllowlist = cleanStrings(p.EnvAllowlist)
	if p.ParseFailureAction == "" {
		p.ParseFailureAction = DecisionAsk
	}
	if p.UnknownToolAction == "" {
		p.UnknownToolAction = DecisionAsk
	}
	if p.DependencyAction == "" {
		p.DependencyAction = DecisionAsk
	}
	if p.PipelineAction == "" {
		p.PipelineAction = DecisionAsk
	}
	if p.HostPTYAction == "" {
		p.HostPTYAction = DecisionAsk
	}
	if p.BackgroundAction == "" {
		p.BackgroundAction = DecisionAsk
	}
	if p.DisallowedEnvAction == "" {
		p.DisallowedEnvAction = DecisionAsk
	}
}

func validDecision(d Decision) bool {
	switch d {
	case DecisionAllow, DecisionDeny, DecisionAsk:
		return true
	default:
		return false
	}
}

func validateHost(raw string) error {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" {
		return fmt.Errorf("contains an empty host")
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if strings.ContainsAny(host, "/:@*") || len(host) > 253 {
		return fmt.Errorf("contains invalid host %q", raw)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 ||
			strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("contains invalid host %q", raw)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("contains invalid host %q", raw)
			}
		}
	}
	return nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func ensureYAMLEOF(dec *yaml.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple YAML documents")
		}
		return err
	}
	return nil
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
