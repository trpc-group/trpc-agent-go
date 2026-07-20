//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RuleSetting toggles a rule family and overrides its action.
//
// Action is the action contributed by findings from this rule, applied
// before the global risk threshold. A critical rule configured as allow or
// ask is rejected by Validate because critical findings must deny.
type RuleSetting struct {
	// Enabled controls whether the rule runs at all.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Action overrides the rule's default decision.
	Action Decision `yaml:"action" json:"action"`
}

// RulePolicy groups every rule family the scanner supports.
type RulePolicy struct {
	// DangerousCommands detects rm -rf, system overwrites, etc.
	DangerousCommands RuleSetting `yaml:"dangerous_commands" json:"dangerous_commands"`
	// Network detects non-whitelisted egress.
	Network RuleSetting `yaml:"network" json:"network"`
	// ShellBypass detects sh -c, eval, substitutions, redirections.
	ShellBypass RuleSetting `yaml:"shell_bypass" json:"shell_bypass"`
	// HostExec detects PTY long sessions, background, privilege, residual.
	HostExec RuleSetting `yaml:"hostexec" json:"hostexec"`
	// Dependencies detects package installation commands.
	Dependencies RuleSetting `yaml:"dependencies" json:"dependencies"`
	// ResourceAbuse detects long sleeps, output bombs, unbounded loops.
	ResourceAbuse RuleSetting `yaml:"resource_abuse" json:"resource_abuse"`
	// SecretLeak detects secrets in input/code/env.
	SecretLeak RuleSetting `yaml:"secret_leak" json:"secret_leak"`
}

// NetworkPolicy configures network egress allowlists.
type NetworkPolicy struct {
	// AllowedDomains lists exact hosts or *.example.com wildcards.
	AllowedDomains []string `yaml:"allowed_domains" json:"allowed_domains"`
	// Commands lists the commands treated as network commands.
	Commands []string `yaml:"commands" json:"commands"`
	// DenyAll disables all egress when true.
	DenyAll bool `yaml:"deny_all" json:"deny_all"`
}

// DecisionThreshold maps each risk level to a default decision. A critical
// finding always denies regardless of the threshold.
type DecisionThreshold struct {
	// Critical is the threshold for critical findings; must be deny.
	Critical Decision `yaml:"critical" json:"critical"`
	// High is the threshold for high-risk findings.
	High Decision `yaml:"high" json:"high"`
	// Medium is the threshold for medium-risk findings.
	Medium Decision `yaml:"medium" json:"medium"`
	// Low is the threshold for low-risk findings.
	Low Decision `yaml:"low" json:"low"`
}

// AuditPolicy configures JSONL audit output.
type AuditPolicy struct {
	// Path is the audit file path. Empty disables file audit.
	Path string `yaml:"path" json:"path"`
	// Required makes an audit write failure deny execution.
	Required bool `yaml:"required" json:"required"`
	// RedactSecrets enables redaction in audit evidence.
	RedactSecrets bool `yaml:"redact_secrets" json:"redact_secrets"`
}

// Policy is the loaded safety policy. The zero value is not usable; use
// DefaultPolicy or LoadPolicy.
type Policy struct {
	// Version is the schema version; must be 1.
	Version int `yaml:"version" json:"version"`
	// AllowedCommands is the executable allowlist for command.not_allowed.
	AllowedCommands []string `yaml:"allowed_commands" json:"allowed_commands"`
	// DeniedCommands is the executable denylist (in addition to built-ins).
	DeniedCommands []string `yaml:"denied_commands" json:"denied_commands"`
	// DeniedPaths is the exact-path denylist for path rules.
	DeniedPaths []string `yaml:"denied_paths" json:"denied_paths"`
	// DeniedPathGlobs is the glob denylist (doublestar patterns).
	DeniedPathGlobs []string `yaml:"denied_path_globs" json:"denied_path_globs"`
	// Network configures egress allowlists.
	Network NetworkPolicy `yaml:"network" json:"network"`
	// MaxTimeout is the maximum permitted timeout.
	MaxTimeout time.Duration `yaml:"max_timeout" json:"max_timeout"`
	// MaxOutputSize is the maximum permitted output in bytes.
	MaxOutputSize int64 `yaml:"max_output_size" json:"max_output_size"`
	// MaxSleepSeconds is the maximum permitted sleep duration in seconds.
	MaxSleepSeconds int64 `yaml:"max_sleep_seconds" json:"max_sleep_seconds"`
	// EnvWhitelist is the permitted environment variable names.
	EnvWhitelist []string `yaml:"env_whitelist" json:"env_whitelist"`
	// RequireIsolation denies profiles that do not declare isolation.
	RequireIsolation bool `yaml:"require_isolation" json:"require_isolation"`
	// Rules toggles and overrides each rule family.
	Rules RulePolicy `yaml:"rules" json:"rules"`
	// DecisionThreshold maps risk levels to default decisions.
	DecisionThreshold DecisionThreshold `yaml:"decision_threshold" json:"decision_threshold"`
	// Audit configures JSONL audit output.
	Audit AuditPolicy `yaml:"audit" json:"audit"`
}

// DefaultPolicy returns a conservative default policy that matches the
// behavior expected by the public test corpus.
func DefaultPolicy() Policy {
	return Policy{
		Version:         1,
		AllowedCommands: []string{"go", "git", "ls", "cat", "echo", "pwd", "grep", "find", "curl"},
		DeniedCommands:  []string{"rm", "sudo", "su", "doas", "chmod", "chown", "dd", "mkfs", "killall"},
		DeniedPaths: []string{
			"~/.ssh", "~/.aws", "~/.kube", ".env", "/etc", "/root",
			"~/.docker/config.json", "~/.netrc", ".git-credentials", ".npmrc", ".pypirc",
		},
		DeniedPathGlobs: []string{"~/.ssh/*", "**/.env*", "**/*.pem", "**/*.key"},
		Network: NetworkPolicy{
			AllowedDomains: []string{
				"github.com", "api.github.com",
				"proxy.golang.org", "sum.golang.org",
				"pypi.org", "files.pythonhosted.org",
				"registry.npmjs.org",
			},
			Commands: []string{"curl", "wget", "nc", "ssh", "scp", "sftp"},
			DenyAll:  false,
		},
		MaxTimeout:      30 * time.Second,
		MaxOutputSize:   1 << 20,
		MaxSleepSeconds: 300,
		EnvWhitelist:    []string{"PATH", "LANG", "LC_ALL", "GOPATH"},
		Rules: RulePolicy{
			DangerousCommands: RuleSetting{Enabled: true, Action: DecisionDeny},
			Network:           RuleSetting{Enabled: true, Action: DecisionDeny},
			ShellBypass:       RuleSetting{Enabled: true, Action: DecisionDeny},
			HostExec:          RuleSetting{Enabled: true, Action: DecisionDeny},
			Dependencies:      RuleSetting{Enabled: true, Action: DecisionAsk},
			ResourceAbuse:     RuleSetting{Enabled: true, Action: DecisionDeny},
			SecretLeak:        RuleSetting{Enabled: true, Action: DecisionDeny},
		},
		DecisionThreshold: DecisionThreshold{
			Critical: DecisionDeny,
			High:     DecisionDeny,
			Medium:   DecisionAsk,
			Low:      DecisionAllow,
		},
		Audit: AuditPolicy{
			Path:          "tool_safety_audit.jsonl",
			Required:      true,
			RedactSecrets: true,
		},
	}
}

// LoadPolicy reads a YAML or JSON policy from path. YAML syntax accepts JSON
// input, so both encodings work with the same decoder.
func LoadPolicy(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("load policy %q: %w", path, err)
	}
	return LoadPolicyFromBytes(data)
}

// LoadPolicyFromBytes parses policy bytes. The decoder accepts YAML or JSON
// (YAML is a superset of JSON). Unknown top-level and nested fields are
// rejected so a typo cannot silently widen the policy. Fields omitted from
// the input retain their DefaultPolicy values, allowing partial overrides.
// "needs_human_review" is normalized to DecisionAsk wherever it appears.
func LoadPolicyFromBytes(data []byte) (Policy, error) {
	policy := DefaultPolicy()
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	if err := normalizePolicyDecisions(&policy); err != nil {
		return Policy{}, err
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// normalizePolicyDecisions converts the "needs_human_review" alias to
// DecisionAsk in every decision-bearing field and validates the resulting
// values. It mutates p in place.
func normalizePolicyDecisions(p *Policy) error {
	var errs []string
	normalize := func(name string, d Decision) Decision {
		out, err := normalizeDecision(d)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			return d
		}
		return out
	}
	p.DecisionThreshold.Critical = normalize("decision_threshold.critical", p.DecisionThreshold.Critical)
	p.DecisionThreshold.High = normalize("decision_threshold.high", p.DecisionThreshold.High)
	p.DecisionThreshold.Medium = normalize("decision_threshold.medium", p.DecisionThreshold.Medium)
	p.DecisionThreshold.Low = normalize("decision_threshold.low", p.DecisionThreshold.Low)
	p.Rules.DangerousCommands.Action = normalize("rules.dangerous_commands.action", p.Rules.DangerousCommands.Action)
	p.Rules.Network.Action = normalize("rules.network.action", p.Rules.Network.Action)
	p.Rules.ShellBypass.Action = normalize("rules.shell_bypass.action", p.Rules.ShellBypass.Action)
	p.Rules.HostExec.Action = normalize("rules.hostexec.action", p.Rules.HostExec.Action)
	p.Rules.Dependencies.Action = normalize("rules.dependencies.action", p.Rules.Dependencies.Action)
	p.Rules.ResourceAbuse.Action = normalize("rules.resource_abuse.action", p.Rules.ResourceAbuse.Action)
	p.Rules.SecretLeak.Action = normalize("rules.secret_leak.action", p.Rules.SecretLeak.Action)
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Validate enforces the cross-field invariants the scanner relies on.
func (p Policy) Validate() error {
	if p.Version != 1 {
		return fmt.Errorf("policy version must be 1, got %d", p.Version)
	}
	if p.MaxTimeout < 0 {
		return errors.New("max_timeout must be non-negative")
	}
	if p.MaxOutputSize < 0 {
		return errors.New("max_output_size must be non-negative")
	}
	if p.MaxSleepSeconds < 0 {
		return errors.New("max_sleep_seconds must be non-negative")
	}
	for _, d := range []struct {
		name string
		v    Decision
	}{
		{"decision_threshold.critical", p.DecisionThreshold.Critical},
		{"decision_threshold.high", p.DecisionThreshold.High},
		{"decision_threshold.medium", p.DecisionThreshold.Medium},
		{"decision_threshold.low", p.DecisionThreshold.Low},
		{"rules.dangerous_commands.action", p.Rules.DangerousCommands.Action},
		{"rules.network.action", p.Rules.Network.Action},
		{"rules.shell_bypass.action", p.Rules.ShellBypass.Action},
		{"rules.hostexec.action", p.Rules.HostExec.Action},
		{"rules.dependencies.action", p.Rules.Dependencies.Action},
		{"rules.resource_abuse.action", p.Rules.ResourceAbuse.Action},
		{"rules.secret_leak.action", p.Rules.SecretLeak.Action},
	} {
		if err := validateDecision(d.name, d.v); err != nil {
			return err
		}
	}
	if p.DecisionThreshold.Critical == DecisionAllow ||
		p.DecisionThreshold.Critical == DecisionAsk {
		return errors.New("decision_threshold.critical must be deny")
	}
	if p.Rules.DangerousCommands.Enabled &&
		p.Rules.DangerousCommands.Action == DecisionAllow {
		return errors.New("rules.dangerous_commands.action cannot be allow")
	}
	if p.Rules.SecretLeak.Enabled &&
		p.Rules.SecretLeak.Action == DecisionAllow {
		return errors.New("rules.secret_leak.action cannot be allow")
	}
	for _, dom := range p.Network.AllowedDomains {
		if err := validateDomain(dom); err != nil {
			return fmt.Errorf("network.allowed_domains: %w", err)
		}
	}
	return nil
}

func validateDecision(name string, d Decision) error {
	switch d {
	case DecisionAllow, DecisionDeny, DecisionAsk:
		return nil
	case DecisionNeedsHumanReview:
		return fmt.Errorf("%s: %q is reserved for input only; use %q", name, d, DecisionAsk)
	case "":
		return fmt.Errorf("%s is empty", name)
	}
	return fmt.Errorf("%s: unknown decision %q", name, d)
}

func validateDomain(dom string) error {
	dom = strings.TrimSpace(dom)
	if dom == "" {
		return errors.New("empty domain")
	}
	if strings.Contains(dom, " ") {
		return fmt.Errorf("domain %q contains spaces", dom)
	}
	if strings.Count(dom, "*") > 1 {
		return fmt.Errorf("domain %q has too many wildcards", dom)
	}
	if strings.HasPrefix(dom, "*.") {
		rest := dom[len("*."):]
		if rest == "" || strings.Contains(rest, "*") {
			return fmt.Errorf("domain %q has invalid wildcard placement", dom)
		}
	} else if strings.Contains(dom, "*") {
		return fmt.Errorf("domain %q must use *.example.com form for wildcards", dom)
	}
	return nil
}

// normalizeDecision converts the needs_human_review alias to ask and rejects
// unknown values. It is used by the loader and by callers that build a
// policy in code.
func normalizeDecision(d Decision) (Decision, error) {
	switch d {
	case DecisionNeedsHumanReview:
		return DecisionAsk, nil
	case DecisionAllow, DecisionDeny, DecisionAsk:
		return d, nil
	case "":
		return "", errors.New("empty decision")
	}
	return "", fmt.Errorf("unknown decision %q", d)
}

// thresholdFor returns the threshold decision for a risk level.
func (p Policy) thresholdFor(r RiskLevel) Decision {
	switch r {
	case RiskCritical:
		return p.DecisionThreshold.Critical
	case RiskHigh:
		return p.DecisionThreshold.High
	case RiskMedium:
		return p.DecisionThreshold.Medium
	case RiskLow:
		return p.DecisionThreshold.Low
	}
	return DecisionDeny
}
