//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

//






package safety

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultPolicy returns a reasonable default safety policy.
// It is used when no policy file is configured.
func DefaultPolicy() *Policy {
	return &Policy{
		Version:          "1.0",
		AllowedCommands:  []string{"echo", "ls", "cat", "go", "python3", "node"},
		DeniedCommands:   []string{},
		ForbiddenPaths: []string{
			"/etc/passwd", "/etc/shadow", "~/.ssh",
			".env", ".env.local", "credentials.json",
			"/proc/", "/sys/",
		},
		AllowlistedHosts: []string{},
		MaxTimeoutSec:    300,
		MaxOutputBytes:   10 * 1024 * 1024, // 10MB
		EnvAllowlist: []string{
			"PATH", "HOME", "USER", "LANG",
			"GOFLAGS", "GOPATH", "GOROOT",
			"PYTHONPATH", "NODE_PATH",
		},
		Rules: []Rule{
			{
				ID:          "dangerous_cmd_001",
				Category:    "dangerous_commands",
				Description: "Detect rm -rf on sensitive directories",
				Patterns:    []string{`rm\s+.*-r.*/`, `rm\s+.*-rf`},
				RiskLevel:   RiskCritical,
				Action:      DecisionDeny,
			},
			{
				ID:          "secrets_001",
				Category:    "sensitive_info",
				Description: "Detect credential file access",
				Patterns:    []string{`~/.ssh`, `\.env`, `credentials`, `id_rsa`, `\.pem`},
				RiskLevel:   RiskCritical,
				Action:      DecisionDeny,
			},
			{
				ID:          "network_egress_001",
				Category:    "network_egress",
				Description: "Detect curl/wget to external hosts",
				Patterns:    []string{`curl\s+`, `wget\s+`, `nc\s+`, `ssh\s+`},
				RiskLevel:   RiskHigh,
				Action:      DecisionAsk,
			},
			{
				ID:          "shell_bypass_001",
				Category:    "shell_bypass",
				Description: "Detect shell wrapper/subshell bypass",
				Patterns:    []string{`sh\s+-c`, `bash\s+-c`, `eval\s+`, `\$\(`, "`"},
				RiskLevel:   RiskCritical,
				Action:      DecisionDeny,
			},
			{
				ID:          "hostexec_risk_001",
				Category:    "host_execution",
				Description: "Detect background/privilege escalation commands",
				Patterns:    []string{`sudo\s+`, `su\s+`, `nohup\s+`, `&\s*$`, `disown`},
				RiskLevel:   RiskHigh,
				Action:      DecisionDeny,
			},
			{
				ID:          "dependency_install_001",
				Category:    "dependency_changes",
				Description: "Detect package installer invocations",
				Patterns:    []string{`pip\s+install`, `npm\s+install`, `go\s+install`, `apt\s+install`, `yum\s+install`},
				RiskLevel:   RiskMedium,
				Action:      DecisionAsk,
			},
			{
				ID:          "resource_abuse_001",
				Category:    "resource_abuse",
				Description: "Detect long sleeps, infinite loops",
				Patterns:    []string{`sleep\s+\d{3,}`, `while\s+true`, `:\(\)\s*{`},
				RiskLevel:   RiskMedium,
				Action:      DecisionDeny,
			},
			{
				ID:          "sensitive_leak_001",
				Category:    "sensitive_leak",
				Description: "Detect API key patterns in output",
				Patterns:    []string{`[A-Za-z0-9_-]{20,}`, `sk-[A-Za-z0-9]{32,}`},
				RiskLevel:   RiskHigh,
				Action:      DecisionAsk,
			},
		},
	}
}

// LoadPolicy reads a safety policy from a YAML file.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file: %w", err)
	}

	var policy Policy
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("parse policy yaml: %w", err)
	}

	if policy.Version == "" {
		policy.Version = "1.0"
	}
	return &policy, nil
}
