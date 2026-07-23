//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "context"

// Rule detects a specific category of risk in a scan request.
// Implementations must be safe for concurrent use.
type Rule interface {
	// ID returns the unique identifier for this rule.
	ID() string
	// Name returns a human-readable name.
	Name() string
	// Check inspects req and returns a Risk if the rule fires,
	// or nil if the request passes this rule.
	Check(ctx context.Context, req *ScanRequest) *Risk
}

// Policy is the configurable safety policy loaded from a YAML or
// JSON file.
type Policy struct {
	// Version is the policy schema version.
	Version string `yaml:"version" json:"version"`
	// DefaultVerdict is used when no rule fires.
	DefaultVerdict Verdict `yaml:"default_verdict" json:"default_verdict"`
	// Commands controls allowed and denied executable names.
	Commands CommandPolicy `yaml:"commands" json:"commands"`
	// ForbiddenPaths are path patterns that must never be accessed.
	ForbiddenPaths []string `yaml:"forbidden_paths" json:"forbidden_paths"`
	// NetworkWhitelist is the set of allowed hostnames for egress.
	NetworkWhitelist []string `yaml:"network_whitelist" json:"network_whitelist"`
	// ResourceLimits constrains execution resources.
	ResourceLimits ResourceLimits `yaml:"resource_limits" json:"resource_limits"`
	// DependencyPolicy controls package installation.
	DependencyPolicy DependencyPolicy `yaml:"dependency_policy" json:"dependency_policy"`
	// SensitivePatterns are regex patterns that match secrets.
	SensitivePatterns []string `yaml:"sensitive_patterns" json:"sensitive_patterns"`
	// BackendPolicies holds per-backend overrides.
	BackendPolicies BackendPolicies `yaml:"backend_policies" json:"backend_policies"`
}

// CommandPolicy holds the allow/deny lists for executable names.
type CommandPolicy struct {
	// Allowed is the set of permitted executable names.
	Allowed []string `yaml:"allowed" json:"allowed"`
	// Denied is the set of forbidden executable names.
	Denied []string `yaml:"denied" json:"denied"`
}

// ResourceLimits constrains execution-time resources.
type ResourceLimits struct {
	// AllowedSleepSeconds is the maximum allowed sleep duration.
	// A value of 0 disables the sleep-duration check.
	AllowedSleepSeconds int `yaml:"allowed_sleep_seconds" json:"allowed_sleep_seconds"`
}

// DependencyPolicy controls package manager usage.
type DependencyPolicy struct {
	// AllowedManagers is the set of permitted package managers.
	AllowedManagers []string `yaml:"allowed_managers" json:"allowed_managers"`
	// DeniedPackages is the set of forbidden package names.
	DeniedPackages []string `yaml:"denied_packages" json:"denied_packages"`
}

// BackendPolicies holds per-backend overrides.
type BackendPolicies struct {
	// WorkspaceExec configures the workspaceexec backend.
	WorkspaceExec BackendPolicy `yaml:"workspaceexec" json:"workspaceexec"`
	// HostExec configures the hostexec backend.
	HostExec BackendPolicy `yaml:"hostexec" json:"hostexec"`
}

// BackendPolicy is the per-backend safety configuration.
type BackendPolicy struct {
	// AllowBackground controls whether background processes are permitted.
	AllowBackground bool `yaml:"allow_background" json:"allow_background"`
	// RequireHumanReview controls whether all commands need review.
	RequireHumanReview bool `yaml:"require_human_review" json:"require_human_review"`
}

// DefaultPolicy returns a policy with sensible defaults that fail
// closed for the most dangerous categories (destruction, credential
// access, non-whitelisted egress) while allowing common safe commands.
func DefaultPolicy() *Policy {
	return &Policy{
		Version:        "1.0",
		DefaultVerdict: VerdictAsk,
		Commands: CommandPolicy{
			Allowed: []string{
				"ls", "cat", "grep", "find", "git", "go", "python",
				"echo", "pwd", "head", "tail", "wc", "sort", "uniq",
				"diff", "make", "test", "mkdir", "cp", "mv", "touch",
			},
			Denied: []string{
				"rm", "dd", "mkfs", "shutdown", "reboot",
				"halt", "poweroff", "killall", "pkill",
			},
		},
		ForbiddenPaths: []string{
			"~/.ssh",
			"~/.aws",
			"~/.gnupg",
			"/etc/passwd",
			"/etc/shadow",
			"*.env",
			"*credentials*",
			"*secret*",
		},
		NetworkWhitelist: []string{
			"github.com",
			"api.github.com",
			"proxy.golang.org",
			"pypi.org",
			"registry.npmjs.org",
		},
		ResourceLimits: ResourceLimits{
			AllowedSleepSeconds: 60,
		},
		DependencyPolicy: DependencyPolicy{
			AllowedManagers: []string{"go", "npm", "pip"},
			DeniedPackages:  []string{},
		},
		SensitivePatterns: []string{
			`sk-[a-zA-Z0-9]{32,}`,
			`ghp_[a-zA-Z0-9]{36}`,
			`-----BEGIN.*PRIVATE KEY-----`,
			`(?i)password\s*=\s*['"][^'"]+['"]`,
			`(?i)api[_-]?key\s*=\s*['"][^'"]+['"]`,
			`(?i)token\s*=\s*['"][^'"]+['"]`,
		},
		BackendPolicies: BackendPolicies{
			WorkspaceExec: BackendPolicy{
				AllowBackground:    false,
				RequireHumanReview: false,
			},
			HostExec: BackendPolicy{
				AllowBackground:    false,
				RequireHumanReview: true,
			},
		},
	}
}
