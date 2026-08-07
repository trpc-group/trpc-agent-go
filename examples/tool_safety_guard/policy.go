//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Policy defines security rules for tool execution.
type Policy struct {
	AllowedCommands    []string `yaml:"allowed_commands"`
	DeniedCommands     []string `yaml:"denied_commands"`
	DeniedPaths        []string `yaml:"denied_paths"`
	AllowedDomains     []string `yaml:"allowed_domains"`
	MaxTimeoutSec      int      `yaml:"max_timeout_sec"`
	MaxOutputSizeBytes int64    `yaml:"max_output_size_bytes"`
}

// LoadPolicy loads safety policy from a YAML file.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
