// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPolicy returns the conservative default execution policy.
func DefaultPolicy() Policy {
	return Policy{
		DeniedCommands: []string{
			"dd", "mkfs", "mount", "umount", "shutdown", "reboot",
			"halt", "poweroff", "sudo", "su", "doas",
		},
		DeniedPaths: []string{
			"/", "/boot", "/dev", "/etc", "/proc", "/root", "/sys",
			"~/.ssh", ".ssh", ".env", ".npmrc", ".pypirc",
			"id_rsa", "id_ed25519", "credentials", "secrets",
		},
		EnvAllowlist: []string{
			"PATH", "HOME", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL",
			"CGO_ENABLED", "GOCACHE", "GOMODCACHE", "GOPATH",
		},
		ReviewCommands: []string{
			"go install", "npm install", "npm ci", "pip install",
			"pip3 install", "apt install", "apt-get install",
			"brew install", "cargo install",
		},
		MaxTimeoutSeconds: 300,
		MaxOutputBytes:    4 * 1024 * 1024,
		ParseErrorAction:  DecisionDeny,
		PipelineAction:    DecisionNeedsHumanReview,
	}
}

// LoadPolicy loads a strict JSON or YAML policy file and overlays its explicit
// values on DefaultPolicy.
func LoadPolicy(path string) (Policy, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read safety policy: %w", err)
	}
	var raw rawPolicy
	if strings.EqualFold(filepath.Ext(path), ".json") {
		err = decodeJSONPolicy(contents, &raw)
	} else {
		err = decodeYAMLPolicy(contents, &raw)
	}
	if err != nil {
		return Policy{}, fmt.Errorf("decode safety policy: %w", err)
	}
	policy := DefaultPolicy()
	raw.overlay(&policy)
	if err := validatePolicy(policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

type rawPolicy struct {
	AllowedCommands   *[]string `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands    *[]string `json:"denied_commands" yaml:"denied_commands"`
	DeniedPaths       *[]string `json:"denied_paths" yaml:"denied_paths"`
	NetworkAllowlist  *[]string `json:"network_allowlist" yaml:"network_allowlist"`
	EnvAllowlist      *[]string `json:"env_allowlist" yaml:"env_allowlist"`
	ReviewCommands    *[]string `json:"review_commands" yaml:"review_commands"`
	MaxTimeoutSeconds *int      `json:"max_timeout_seconds" yaml:"max_timeout_seconds"`
	MaxOutputBytes    *int64    `json:"max_output_bytes" yaml:"max_output_bytes"`
	ParseErrorAction  *Decision `json:"parse_error_action" yaml:"parse_error_action"`
	PipelineAction    *Decision `json:"pipeline_action" yaml:"pipeline_action"`
}

func decodeJSONPolicy(contents []byte, raw *rawPolicy) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(raw); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func decodeYAMLPolicy(contents []byte, raw *rawPolicy) error {
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(raw); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing YAML document")
		}
		return err
	}
	return nil
}

func (raw rawPolicy) overlay(policy *Policy) {
	if raw.AllowedCommands != nil {
		policy.AllowedCommands = cloneStrings(*raw.AllowedCommands)
	}
	if raw.DeniedCommands != nil {
		policy.DeniedCommands = cloneStrings(*raw.DeniedCommands)
	}
	if raw.DeniedPaths != nil {
		policy.DeniedPaths = cloneStrings(*raw.DeniedPaths)
	}
	if raw.NetworkAllowlist != nil {
		policy.NetworkAllowlist = cloneStrings(*raw.NetworkAllowlist)
	}
	if raw.EnvAllowlist != nil {
		policy.EnvAllowlist = cloneStrings(*raw.EnvAllowlist)
	}
	if raw.ReviewCommands != nil {
		policy.ReviewCommands = cloneStrings(*raw.ReviewCommands)
	}
	if raw.MaxTimeoutSeconds != nil {
		policy.MaxTimeoutSeconds = *raw.MaxTimeoutSeconds
	}
	if raw.MaxOutputBytes != nil {
		policy.MaxOutputBytes = *raw.MaxOutputBytes
	}
	if raw.ParseErrorAction != nil {
		policy.ParseErrorAction = *raw.ParseErrorAction
	}
	if raw.PipelineAction != nil {
		policy.PipelineAction = *raw.PipelineAction
	}
}

func validatePolicy(policy Policy) error {
	if policy.MaxTimeoutSeconds < 0 {
		return fmt.Errorf("max_timeout_seconds must not be negative")
	}
	if policy.MaxOutputBytes < 0 {
		return fmt.Errorf("max_output_bytes must not be negative")
	}
	for _, deniedPath := range policy.DeniedPaths {
		if strings.ContainsAny(deniedPath, "*?[]") {
			return fmt.Errorf("denied_paths must not contain wildcard characters: %q", deniedPath)
		}
	}
	if policy.ParseErrorAction != "" && !validDecision(policy.ParseErrorAction) {
		return fmt.Errorf("parse_error_action is not a valid decision: %q", policy.ParseErrorAction)
	}
	if policy.PipelineAction != "" && !validDecision(policy.PipelineAction) {
		return fmt.Errorf("pipeline_action is not a valid decision: %q", policy.PipelineAction)
	}
	return nil
}

func validDecision(decision Decision) bool {
	return decision == DecisionAllow || decision == DecisionDeny ||
		decision == DecisionAsk || decision == DecisionNeedsHumanReview
}

func clonePolicy(policy Policy) Policy {
	policy.AllowedCommands = cloneStrings(policy.AllowedCommands)
	policy.DeniedCommands = cloneStrings(policy.DeniedCommands)
	policy.DeniedPaths = cloneStrings(policy.DeniedPaths)
	policy.NetworkAllowlist = cloneStrings(policy.NetworkAllowlist)
	policy.EnvAllowlist = cloneStrings(policy.EnvAllowlist)
	policy.ReviewCommands = cloneStrings(policy.ReviewCommands)
	return policy
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
