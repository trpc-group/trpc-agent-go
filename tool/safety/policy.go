//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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

// Decision is the outcome of a safety scan.
type Decision string

const (
	// DecisionAllow lets the tool call execute.
	DecisionAllow Decision = "allow"
	// DecisionAsk requires an interactive approval before execution.
	DecisionAsk Decision = "ask"
	// DecisionNeedsHumanReview flags the call for offline human review.
	// Permission bridges map it to an ask outcome because the framework
	// cannot execute "later"; the distinct value is preserved in reports
	// and audit events so review queues can be built on top of it.
	DecisionNeedsHumanReview Decision = "needs_human_review"
	// DecisionDeny blocks the tool call.
	DecisionDeny Decision = "deny"
)

// severity orders decisions from most permissive to most restrictive.
func (d Decision) severity() int {
	switch d {
	case DecisionAllow:
		return 0
	case DecisionAsk:
		return 1
	case DecisionNeedsHumanReview:
		return 2
	case DecisionDeny:
		return 3
	default:
		return 3
	}
}

// stricter returns the more restrictive of the two decisions.
func stricter(a, b Decision) Decision {
	if b.severity() > a.severity() {
		return b
	}
	return a
}

// RiskLevel grades the severity of a finding or report.
type RiskLevel string

// Risk levels, from benign to critical.
const (
	RiskNone     RiskLevel = "none"
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

func (r RiskLevel) severity() int {
	switch r {
	case RiskNone:
		return 0
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskCritical:
		return 4
	default:
		return 4
	}
}

func maxRisk(a, b RiskLevel) RiskLevel {
	if b.severity() > a.severity() {
		return b
	}
	return a
}

// NetworkPolicy configures the network-egress rule.
type NetworkPolicy struct {
	// AllowedHosts lists hostnames (exact or "*.suffix" wildcard)
	// that network commands may reach. An empty list means every
	// egress attempt is reported.
	AllowedHosts []string `json:"allowed_hosts" yaml:"allowed_hosts"`
	// EgressCommands enumerates executables treated as network
	// clients. Defaults cover curl, wget, nc, ssh, scp, rsync, ftp
	// and friends; the list replaces the defaults when non-empty.
	EgressCommands []string `json:"egress_commands" yaml:"egress_commands"`
	// Decision applied when an egress command targets a host outside
	// AllowedHosts. Defaults to deny.
	Decision Decision `json:"decision" yaml:"decision"`
}

// LimitsPolicy bounds resource usage evaluated before execution.
type LimitsPolicy struct {
	// MaxTimeoutSec caps the timeout a tool call may request.
	// Zero disables the check.
	MaxTimeoutSec int `json:"max_timeout_sec" yaml:"max_timeout_sec"`
	// MaxOutputBytes is an advisory cap used for reporting when a
	// command is known to generate unbounded output.
	MaxOutputBytes int64 `json:"max_output_bytes" yaml:"max_output_bytes"`
	// MaxSleepSec flags sleep invocations longer than this bound.
	// Zero disables the check.
	MaxSleepSec int `json:"max_sleep_sec" yaml:"max_sleep_sec"`
	// MaxPipelineSegments flags pipelines with more segments than
	// this bound (mass parallelism / obfuscation). Zero disables.
	MaxPipelineSegments int `json:"max_pipeline_segments" yaml:"max_pipeline_segments"`
}

// EnvPolicy controls environment-variable handling.
type EnvPolicy struct {
	// AllowedNames lists environment variable names a tool call may
	// set. Empty means any name is accepted (values are still
	// scanned for secrets).
	AllowedNames []string `json:"allowed_names" yaml:"allowed_names"`
	// DeniedNames lists names that are always rejected (for example
	// LD_PRELOAD). Takes precedence over AllowedNames.
	DeniedNames []string `json:"denied_names" yaml:"denied_names"`
}

// HostExecPolicy tunes checks specific to host execution backends.
type HostExecPolicy struct {
	// AllowBackground permits background (long-lived) host sessions.
	AllowBackground bool `json:"allow_background" yaml:"allow_background"`
	// AllowPTY permits interactive PTY host sessions.
	AllowPTY bool `json:"allow_pty" yaml:"allow_pty"`
	// Decision applied when a disallowed session type is requested.
	// Defaults to ask so a human can approve legitimate uses.
	Decision Decision `json:"decision" yaml:"decision"`
}

// Policy is the operator-tunable configuration of the safety guard.
// The zero value is unusable; obtain a baseline from DefaultPolicy or
// LoadPolicy and adjust fields as needed.
type Policy struct {
	// Version identifies the policy schema. Currently always 1.
	Version int `json:"version" yaml:"version"`

	// AllowedCommands is the executable allowlist applied to every
	// pipeline segment (shellsafe strict-allow semantics). Empty
	// means any executable not otherwise denied may run.
	AllowedCommands []string `json:"allowed_commands" yaml:"allowed_commands"`
	// DeniedCommands is the executable denylist (basename or path).
	DeniedCommands []string `json:"denied_commands" yaml:"denied_commands"`

	// DeniedPaths lists path fragments that must not appear in
	// command arguments or code blocks (~/.ssh, .env, ...). Matching
	// is substring-based after normalisation, which is deliberately
	// conservative.
	DeniedPaths []string `json:"denied_paths" yaml:"denied_paths"`

	// DestructivePatterns extends the built-in destructive command
	// detection (rm -rf /, mkfs, dd of=/dev/...) with extra literal
	// fragments.
	DestructivePatterns []string `json:"destructive_patterns" yaml:"destructive_patterns"`

	// Network configures the egress rule.
	Network NetworkPolicy `json:"network" yaml:"network"`

	// Limits bounds requested resources.
	Limits LimitsPolicy `json:"limits" yaml:"limits"`

	// Env controls environment-variable checks.
	Env EnvPolicy `json:"env" yaml:"env"`

	// HostExec tunes host-execution session checks.
	HostExec HostExecPolicy `json:"host_exec" yaml:"host_exec"`

	// DependencyInstallDecision is applied when a package manager
	// install command is detected (go install, pip install, ...).
	// Defaults to ask.
	DependencyInstallDecision Decision `json:"dependency_install_decision" yaml:"dependency_install_decision"`

	// ParseErrorDecision is applied when shellsafe cannot parse the
	// command. Never allow: values other than deny, ask and
	// needs_human_review are rejected at load time. Defaults to deny.
	ParseErrorDecision Decision `json:"parse_error_decision" yaml:"parse_error_decision"`

	// RedactSecrets masks detected secrets in reports and audit
	// events. Defaults to true.
	RedactSecrets *bool `json:"redact_secrets" yaml:"redact_secrets"`
}

// DefaultPolicy returns the conservative built-in policy.
func DefaultPolicy() Policy {
	yes := true
	return Policy{
		Version: 1,
		DeniedPaths: []string{
			"~/.ssh", "/.ssh/", "id_rsa", "id_ed25519",
			".env", "/etc/shadow", "/etc/sudoers",
			".aws/credentials", ".kube/config", ".netrc",
			".git-credentials", ".npmrc", ".pypirc",
			"credentials.json", "secrets.json", "/root/.ssh",
		},
		Network: NetworkPolicy{
			Decision: DecisionDeny,
		},
		Limits: LimitsPolicy{
			MaxTimeoutSec:       600,
			MaxOutputBytes:      10 << 20, // 10 MiB
			MaxSleepSec:         120,
			MaxPipelineSegments: 8,
		},
		Env: EnvPolicy{
			DeniedNames: []string{
				"LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES",
				"GIT_SSH_COMMAND", "BASH_ENV", "ENV", "PROMPT_COMMAND",
			},
		},
		HostExec: HostExecPolicy{
			Decision: DecisionAsk,
		},
		DependencyInstallDecision: DecisionAsk,
		ParseErrorDecision:        DecisionDeny,
		RedactSecrets:             &yes,
	}
}

// LoadPolicy reads a YAML (.yaml/.yml) or JSON (.json) policy file
// and merges it over DefaultPolicy: list fields replace the defaults
// when present in the file, scalar fields replace when non-zero.
func LoadPolicy(path string) (Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("safety: read policy: %w", err)
	}
	return ParsePolicy(raw, strings.ToLower(filepath.Ext(path)))
}

// ParsePolicy decodes policy bytes. ext selects the decoder:
// ".json" uses JSON, everything else uses YAML (a superset of JSON,
// so extension-less JSON payloads still parse).
func ParsePolicy(raw []byte, ext string) (Policy, error) {
	loaded := Policy{}
	var err error
	if ext == ".json" {
		err = json.Unmarshal(raw, &loaded)
	} else {
		err = yaml.Unmarshal(raw, &loaded)
	}
	if err != nil {
		return Policy{}, fmt.Errorf("safety: decode policy: %w", err)
	}
	merged := mergePolicy(DefaultPolicy(), loaded)
	if err := merged.Validate(); err != nil {
		return Policy{}, err
	}
	return merged, nil
}

// mergePolicy overlays loaded onto base.
func mergePolicy(base, loaded Policy) Policy {
	out := base
	if loaded.Version != 0 {
		out.Version = loaded.Version
	}
	if loaded.AllowedCommands != nil {
		out.AllowedCommands = loaded.AllowedCommands
	}
	if loaded.DeniedCommands != nil {
		out.DeniedCommands = loaded.DeniedCommands
	}
	if loaded.DeniedPaths != nil {
		out.DeniedPaths = loaded.DeniedPaths
	}
	if loaded.DestructivePatterns != nil {
		out.DestructivePatterns = loaded.DestructivePatterns
	}
	if loaded.Network.AllowedHosts != nil {
		out.Network.AllowedHosts = loaded.Network.AllowedHosts
	}
	if loaded.Network.EgressCommands != nil {
		out.Network.EgressCommands = loaded.Network.EgressCommands
	}
	if loaded.Network.Decision != "" {
		out.Network.Decision = loaded.Network.Decision
	}
	if loaded.Limits.MaxTimeoutSec != 0 {
		out.Limits.MaxTimeoutSec = loaded.Limits.MaxTimeoutSec
	}
	if loaded.Limits.MaxOutputBytes != 0 {
		out.Limits.MaxOutputBytes = loaded.Limits.MaxOutputBytes
	}
	if loaded.Limits.MaxSleepSec != 0 {
		out.Limits.MaxSleepSec = loaded.Limits.MaxSleepSec
	}
	if loaded.Limits.MaxPipelineSegments != 0 {
		out.Limits.MaxPipelineSegments = loaded.Limits.MaxPipelineSegments
	}
	if loaded.Env.AllowedNames != nil {
		out.Env.AllowedNames = loaded.Env.AllowedNames
	}
	if loaded.Env.DeniedNames != nil {
		out.Env.DeniedNames = loaded.Env.DeniedNames
	}
	// Merge each HostExec field independently so a policy that only sets
	// allow_background/allow_pty (without a decision) is not silently dropped.
	if loaded.HostExec.AllowBackground {
		out.HostExec.AllowBackground = true
	}
	if loaded.HostExec.AllowPTY {
		out.HostExec.AllowPTY = true
	}
	if loaded.HostExec.Decision != "" {
		out.HostExec.Decision = loaded.HostExec.Decision
	}
	if loaded.DependencyInstallDecision != "" {
		out.DependencyInstallDecision = loaded.DependencyInstallDecision
	}
	if loaded.ParseErrorDecision != "" {
		out.ParseErrorDecision = loaded.ParseErrorDecision
	}
	if loaded.RedactSecrets != nil {
		out.RedactSecrets = loaded.RedactSecrets
	}
	return out
}

// Validate rejects policies that would weaken the fail-closed
// contract of the scanner.
func (p Policy) Validate() error {
	for _, d := range []Decision{
		p.Network.Decision,
		p.HostExec.Decision,
		p.DependencyInstallDecision,
		p.ParseErrorDecision,
	} {
		if d == "" {
			continue
		}
		switch d {
		case DecisionAllow, DecisionAsk, DecisionNeedsHumanReview, DecisionDeny:
		default:
			return fmt.Errorf("safety: unknown decision %q in policy", d)
		}
	}
	if p.ParseErrorDecision == DecisionAllow {
		return fmt.Errorf(
			"safety: parse_error_decision must not be %q: commands that "+
				"cannot be parsed conservatively must never run unreviewed",
			DecisionAllow,
		)
	}
	return nil
}

// redact reports whether secret redaction is enabled.
func (p Policy) redact() bool {
	return p.RedactSecrets == nil || *p.RedactSecrets
}
