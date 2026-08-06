//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Policy defines the configurable safety rules and thresholds for tool execution.
type Policy struct {
	AllowedCommands  []string `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands   []string `json:"denied_commands" yaml:"denied_commands"`
	ForbiddenPaths   []string `json:"forbidden_paths" yaml:"forbidden_paths"`
	NetworkWhitelist []string `json:"network_whitelist" yaml:"network_whitelist"`
	MaxTimeout       string   `json:"max_timeout" yaml:"max_timeout"`
	MaxOutputSize    int64    `json:"max_output_size" yaml:"max_output_size"`
	AllowedEnvVars   []string `json:"allowed_env_vars" yaml:"allowed_env_vars"`
	AskRules         []string `json:"ask_rules" yaml:"ask_rules"`
}

// DefaultPolicy returns a baseline policy with conservative safety defaults.
func DefaultPolicy() *Policy {
	return &Policy{
		DeniedCommands: []string{
			"rm", "dd", "mkfs", "sudo", "su", "shutdown", "reboot", "init",
			"sh", "bash", "zsh", "eval", "exec",
		},
		ForbiddenPaths: []string{
			"~/.ssh", "/etc", "*.env", "*.pem", "*.key", "id_rsa",
		},
		NetworkWhitelist: []string{
			"github.com", "api.github.com", "golang.org", "pypi.org", "npmjs.org",
		},
		MaxTimeout:    "30s",
		MaxOutputSize: 1024 * 1024, // 1MB
		AllowedEnvVars: []string{
			"PATH", "HOME", "USER", "LANG", "LC_ALL", "SHELL", "TMPDIR",
		},
		AskRules: []string{
			"go install", "npm install", "pip install", "apt install", "yum install",
		},
	}
}

// LoadPolicyFile loads policy settings from a JSON or YAML file.
func LoadPolicyFile(filePath string) (*Policy, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read policy file failed: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".json":
		return LoadPolicyJSON(data)
	case ".yaml", ".yml":
		return LoadPolicyYAML(data)
	default:
		// Try JSON first; if it fails, try YAML.
		p, errJSON := LoadPolicyJSON(data)
		if errJSON == nil {
			return p, nil
		}
		pYAML, errYAML := LoadPolicyYAML(data)
		if errYAML == nil {
			return pYAML, nil
		}
		return nil, fmt.Errorf("unable to parse policy file as JSON (%v) or YAML (%v)", errJSON, errYAML)
	}
}

// LoadPolicyJSON parses JSON bytes into a Policy.
func LoadPolicyJSON(data []byte) (*Policy, error) {
	p := DefaultPolicy()
	if err := json.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("unmarshal JSON policy failed: %w", err)
	}
	return p, nil
}

// LoadPolicyYAML parses YAML bytes into a Policy.
func LoadPolicyYAML(data []byte) (*Policy, error) {
	p := DefaultPolicy()
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("unmarshal YAML policy failed: %w", err)
	}
	return p, nil
}
