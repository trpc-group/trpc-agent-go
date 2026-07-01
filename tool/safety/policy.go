//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// Action is a safety decision applied before a tool executes.
type Action string

const (
	// ActionAllow lets the tool call proceed.
	ActionAllow Action = "allow"
	// ActionDeny blocks the tool call.
	ActionDeny Action = "deny"
	// ActionAsk routes the tool call to human / model approval. The policy
	// keyword "needs_human_review" is normalized to this value.
	ActionAsk Action = "ask"
)

// RiskLevel ranks the severity of a finding. It is defined here because the
// policy's rule overrides reference it; the rule engine and report reuse the
// same type.
type RiskLevel string

const (
	// RiskNone means no risk was detected.
	RiskNone RiskLevel = "none"
	// RiskLow is an informational finding.
	RiskLow RiskLevel = "low"
	// RiskMedium is a finding that warrants human review.
	RiskMedium RiskLevel = "medium"
	// RiskHigh is a finding that should be blocked by default.
	RiskHigh RiskLevel = "high"
	// RiskCritical is an unconditionally dangerous finding.
	RiskCritical RiskLevel = "critical"
)

// Backend identifiers used by the report and the backend mapping. These are
// the canonical backend names; the concrete tool names that map to each are
// configured under Policy.Backends so a renamed tool (hostexec/codeexec allow
// WithName) does not silently bypass the guard.
const (
	// BackendWorkspace is the workspace_exec backend (sandboxed workspace).
	BackendWorkspace = "workspace_exec"
	// BackendHost is the host shell backend (hostexec exec_command).
	BackendHost = "host"
	// BackendCode is the code executor backend (codeexec execute_code).
	BackendCode = "code"
)

// CommandPolicy holds the executable-name allow/deny lists handed to
// internal/shellsafe. shellsafe owns argv[0] allow/deny plus its built-in
// shell-wrapper deny set; the safety rules only add argument-level checks.
type CommandPolicy struct {
	Allowed []string `yaml:"allowed" json:"allowed"`
	Denied  []string `yaml:"denied" json:"denied"`
}

// NetworkPolicy configures outbound-network detection.
type NetworkPolicy struct {
	DownloadCommands []string `yaml:"download_commands" json:"download_commands"`
	AllowedDomains   []string `yaml:"allowed_domains" json:"allowed_domains"`
	OnNonWhitelisted Action   `yaml:"on_non_whitelisted" json:"on_non_whitelisted"`
}

// ResourcePolicy configures resource-abuse limits. Static detection here is
// best-effort; the real enforcement is the runtime timeout / output cap in
// workspaceexec and the sandbox.
type ResourcePolicy struct {
	MaxTimeoutSec        int  `yaml:"max_timeout_sec" json:"max_timeout_sec"`
	MaxOutputBytes       int  `yaml:"max_output_bytes" json:"max_output_bytes"`
	MaxSleepSec          int  `yaml:"max_sleep_sec" json:"max_sleep_sec"`
	DenyBackgroundOnHost bool `yaml:"deny_background_on_host" json:"deny_background_on_host"`
	DenyPTYOnHost        bool `yaml:"deny_pty_on_host" json:"deny_pty_on_host"`
}

// EnvPolicy configures environment-variable handling.
type EnvPolicy struct {
	AllowedKeys []string `yaml:"allowed_keys" json:"allowed_keys"`
}

// SecretPolicy lists regular expressions whose matches are redacted from
// command strings, evidence, env values, reports and audit events.
type SecretPolicy struct {
	Patterns []string `yaml:"patterns" json:"patterns"`
}

// Subcommand matches a command plus an argument prefix, e.g. {cmd: go,
// args_prefix: [install]} matches "go install ...".
type Subcommand struct {
	Cmd        string   `yaml:"cmd" json:"cmd"`
	ArgsPrefix []string `yaml:"args_prefix" json:"args_prefix"`
}

// Override replaces the action and/or risk level produced for a rule id. A
// blank Action leaves the rule's action unchanged (risk-only override).
type Override struct {
	Action    Action    `yaml:"action" json:"action"`
	RiskLevel RiskLevel `yaml:"risk_level" json:"risk_level"`
}

// Policy is the file-driven safety configuration. Editing the YAML/JSON file
// changes allow/deny lists, forbidden paths, the network whitelist and limits
// without code changes.
type Policy struct {
	Version           int                 `yaml:"version" json:"version"`
	UnparsableAction  Action              `yaml:"unparsable_action" json:"unparsable_action"`
	DefaultAction     Action              `yaml:"default_action" json:"default_action"`
	Backends          map[string][]string `yaml:"backends" json:"backends"`
	Commands          CommandPolicy       `yaml:"commands" json:"commands"`
	DeniedSubcommands []Subcommand        `yaml:"denied_subcommands" json:"denied_subcommands"`
	ForbiddenPaths    []string            `yaml:"forbidden_paths" json:"forbidden_paths"`
	Network           NetworkPolicy       `yaml:"network" json:"network"`
	Resources         ResourcePolicy      `yaml:"resources" json:"resources"`
	Env               EnvPolicy           `yaml:"env" json:"env"`
	Secrets           SecretPolicy        `yaml:"secrets" json:"secrets"`
	RuleOverrides     map[string]Override `yaml:"rule_overrides" json:"rule_overrides"`

	// compiled holds the precompiled, read-only matchers. It is populated by
	// compile and is safe for concurrent reads.
	compiled compiledPolicy `yaml:"-" json:"-"`
}

// compiledPolicy holds the precompiled matchers derived from a Policy. All
// fields are read-only after compile and safe for concurrent use.
type compiledPolicy struct {
	forbiddenGlobs []string
	secretRes      []*regexp.Regexp
	allowedDomains []domainMatcher
	shellPolicy    shellsafe.Policy
	backendIndex   map[string]string // tool name -> backend identifier
}

// domainMatcher matches a host against one allowed_domains entry. A "*."
// prefix marks a wildcard that also matches the bare base domain.
type domainMatcher struct {
	base     string
	suffix   string
	wildcard bool
}

// DefaultPolicy returns the built-in safe defaults: unparsable commands are
// denied, anything not matched by a rule is allowed, non-whitelisted network
// access is denied, and the backend map points at the real tool names.
func DefaultPolicy() Policy {
	return Policy{
		Version:          1,
		UnparsableAction: ActionDeny,
		DefaultAction:    ActionAllow,
		Network:          NetworkPolicy{OnNonWhitelisted: ActionDeny},
		Backends: map[string][]string{
			BackendWorkspace: {"workspace_exec"},
			BackendHost:      {"exec_command"},
			BackendCode:      {"execute_code"},
		},
	}
}

// LoadPolicy reads a YAML or JSON policy file, layers it over DefaultPolicy and
// compiles it. A bad regex or glob in the file surfaces as an error here, at
// startup, rather than at request time.
func LoadPolicy(path string) (*Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy %q: %w", path, err)
	}
	p := DefaultPolicy()
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("parse yaml policy %q: %w", path, err)
		}
	case ".json":
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("parse json policy %q: %w", path, err)
		}
	default:
		return nil, fmt.Errorf(
			"unsupported policy extension %q (want .yaml, .yml or .json)", ext)
	}
	if err := p.compile(); err != nil {
		return nil, err
	}
	return &p, nil
}

// compile validates and precompiles the policy. It normalizes the action
// keywords (including needs_human_review -> ask), compiles every regex and
// glob, and builds the domain and backend indexes.
func (p *Policy) compile() error {
	var c compiledPolicy
	var err error
	if p.UnparsableAction, err = resolveAction(p.UnparsableAction, ActionDeny); err != nil {
		return fmt.Errorf("unparsable_action: %w", err)
	}
	if p.DefaultAction, err = resolveAction(p.DefaultAction, ActionAllow); err != nil {
		return fmt.Errorf("default_action: %w", err)
	}
	if p.Network.OnNonWhitelisted, err = resolveAction(
		p.Network.OnNonWhitelisted, ActionDeny); err != nil {
		return fmt.Errorf("network.on_non_whitelisted: %w", err)
	}
	for id, ov := range p.RuleOverrides {
		changed := false
		if strings.TrimSpace(string(ov.Action)) != "" {
			ca, ok := canonicalAction(ov.Action)
			if !ok {
				return fmt.Errorf("rule_overrides[%s]: unknown action %q", id, ov.Action)
			}
			ov.Action = ca
			changed = true
		}
		if strings.TrimSpace(string(ov.RiskLevel)) != "" {
			cr, ok := canonicalRisk(ov.RiskLevel)
			if !ok {
				return fmt.Errorf("rule_overrides[%s]: unknown risk_level %q", id, ov.RiskLevel)
			}
			ov.RiskLevel = cr
			changed = true
		}
		if changed {
			p.RuleOverrides[id] = ov
		}
	}
	if c.secretRes, err = compileRegexps(p.Secrets.Patterns, "secrets.patterns"); err != nil {
		return err
	}
	c.forbiddenGlobs = expandForbiddenPaths(p.ForbiddenPaths)
	for _, g := range c.forbiddenGlobs {
		if !doublestar.ValidatePattern(g) {
			return fmt.Errorf("forbidden_paths: invalid glob %q", g)
		}
	}
	c.allowedDomains = compileDomains(p.Network.AllowedDomains)
	c.shellPolicy = shellsafe.PolicyFromLists(p.Commands.Allowed, p.Commands.Denied)
	if c.backendIndex, err = buildBackendIndex(p.Backends); err != nil {
		return err
	}
	p.compiled = c
	return nil
}

// clone returns a deep copy of the policy's mutable fields. WithPolicy uses it
// so that compile() (which rewrites RuleOverrides in place) and subsequent
// concurrent checks operate on the guard's own maps and slices: caller
// mutations after NewGuard cannot change live policy behavior or race with a
// check. The compiled field is intentionally reset; the caller recompiles.
func (p *Policy) clone() Policy {
	cp := *p
	cp.compiled = compiledPolicy{}
	cp.Backends = cloneStringSliceMap(p.Backends)
	cp.Commands.Allowed = cloneStrings(p.Commands.Allowed)
	cp.Commands.Denied = cloneStrings(p.Commands.Denied)
	cp.ForbiddenPaths = cloneStrings(p.ForbiddenPaths)
	cp.Network.DownloadCommands = cloneStrings(p.Network.DownloadCommands)
	cp.Network.AllowedDomains = cloneStrings(p.Network.AllowedDomains)
	cp.Env.AllowedKeys = cloneStrings(p.Env.AllowedKeys)
	cp.Secrets.Patterns = cloneStrings(p.Secrets.Patterns)
	if p.DeniedSubcommands != nil {
		subs := make([]Subcommand, len(p.DeniedSubcommands))
		for i, s := range p.DeniedSubcommands {
			s.ArgsPrefix = cloneStrings(s.ArgsPrefix)
			subs[i] = s
		}
		cp.DeniedSubcommands = subs
	}
	if p.RuleOverrides != nil {
		ov := make(map[string]Override, len(p.RuleOverrides))
		for k, v := range p.RuleOverrides {
			ov[k] = v
		}
		cp.RuleOverrides = ov
	}
	return cp
}

// cloneStrings returns a copy of s, preserving nil.
func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// cloneStringSliceMap returns a deep copy of m, preserving nil.
func cloneStringSliceMap(m map[string][]string) map[string][]string {
	if m == nil {
		return nil
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = cloneStrings(v)
	}
	return out
}

// backendFor returns the backend identifier configured for toolName, or an
// empty string when the tool is not an exec backend (and is therefore allowed
// without scanning).
func (p *Policy) backendFor(toolName string) string {
	return p.compiled.backendIndex[toolName]
}

// shellPolicy exposes the compiled shellsafe policy to the rule engine.
func (p *Policy) shellPolicy() shellsafe.Policy {
	return p.compiled.shellPolicy
}

// forbiddenMatch reports whether candidate (an argv word or cwd) hits any
// forbidden path. Non-glob entries also match as a directory prefix, so
// "~/.ssh" matches "~/.ssh/id_rsa". It returns the matched pattern for use as
// finding evidence.
func (p *Policy) forbiddenMatch(candidate string) (string, bool) {
	cand := filepath.ToSlash(strings.TrimSpace(candidate))
	if cand == "" {
		return "", false
	}
	for _, g := range p.compiled.forbiddenGlobs {
		if globHit(g, cand) {
			return g, true
		}
	}
	return "", false
}

// domainAllowed reports whether host is in the network allow list. host should
// already be free of any port (pass url.URL.Hostname()).
func (p *Policy) domainAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, m := range p.compiled.allowedDomains {
		if m.wildcard {
			if host == m.base || strings.HasSuffix(host, m.suffix) {
				return true
			}
			continue
		}
		if host == m.base {
			return true
		}
	}
	return false
}

// canonicalAction maps an action keyword (case-insensitive, trimmed) to its
// canonical form. needs_human_review is an alias for ask.
func canonicalAction(a Action) (Action, bool) {
	switch strings.ToLower(strings.TrimSpace(string(a))) {
	case "allow":
		return ActionAllow, true
	case "deny":
		return ActionDeny, true
	case "ask", "needs_human_review":
		return ActionAsk, true
	default:
		return "", false
	}
}

// canonicalRisk maps a risk-level keyword (case-insensitive, trimmed) to its
// canonical form. An unknown keyword is rejected so a typo like "medum" cannot
// silently map to no action and let the default action permit a finding.
func canonicalRisk(r RiskLevel) (RiskLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(string(r))) {
	case "none":
		return RiskNone, true
	case "low":
		return RiskLow, true
	case "medium":
		return RiskMedium, true
	case "high":
		return RiskHigh, true
	case "critical":
		return RiskCritical, true
	default:
		return "", false
	}
}

// resolveAction returns the canonical action, falling back when blank and
// erroring on an unknown keyword.
func resolveAction(a, fallback Action) (Action, error) {
	if strings.TrimSpace(string(a)) == "" {
		return fallback, nil
	}
	ca, ok := canonicalAction(a)
	if !ok {
		return "", fmt.Errorf("unknown action %q", a)
	}
	return ca, nil
}

// compileRegexps compiles each pattern, attributing a parse error to field.
func compileRegexps(patterns []string, field string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, pat := range patterns {
		if strings.TrimSpace(pat) == "" {
			continue
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("%s: compile %q: %w", field, pat, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// expandForbiddenPaths normalizes patterns to forward slashes and, for entries
// rooted at "~", adds a $HOME-expanded variant so both the literal "~/.ssh"
// and an absolute "/home/user/.ssh" form are caught.
func expandForbiddenPaths(patterns []string) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		norm := filepath.ToSlash(p)
		out = append(out, norm)
		if home != "" && (norm == "~" || strings.HasPrefix(norm, "~/")) {
			expanded := filepath.ToSlash(filepath.Join(home, strings.TrimPrefix(norm, "~")))
			out = append(out, expanded)
		}
	}
	return out
}

// compileDomains parses allowed_domains entries into matchers. A "*." prefix
// marks a wildcard that also accepts the bare base domain.
func compileDomains(domains []string) []domainMatcher {
	var out []domainMatcher
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if strings.HasPrefix(d, "*.") {
			base := d[2:]
			out = append(out, domainMatcher{base: base, suffix: "." + base, wildcard: true})
			continue
		}
		out = append(out, domainMatcher{base: d})
	}
	return out
}

// buildBackendIndex inverts the backend->tools map into a tool->backend index.
// It rejects unknown backend names (a typo like "hostexec" would otherwise
// silently disable backend-specific checks) and duplicate tool mappings (the
// same tool under two backends would otherwise be resolved by map iteration
// order). Both surface at compile time, not at request time.
func buildBackendIndex(backends map[string][]string) (map[string]string, error) {
	idx := make(map[string]string)
	for backend, tools := range backends {
		switch backend {
		case BackendWorkspace, BackendHost, BackendCode:
		default:
			return nil, fmt.Errorf("backends: unknown backend %q (want %q, %q or %q)",
				backend, BackendWorkspace, BackendHost, BackendCode)
		}
		for _, t := range tools {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if prev, ok := idx[t]; ok && prev != backend {
				return nil, fmt.Errorf(
					"backends: tool %q is mapped to both %q and %q", t, prev, backend)
			}
			idx[t] = backend
		}
	}
	return idx, nil
}

// globHit reports whether candidate matches pattern. A pattern without glob
// metacharacters matches the exact path or any path under it; a glob pattern
// is matched with doublestar.
func globHit(pattern, candidate string) bool {
	if pattern == candidate {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[{") {
		trimmed := strings.TrimRight(pattern, "/")
		if trimmed == "" {
			// Root ("/") only matches exactly (handled by the equality check
			// above); treating it as a prefix would flag every absolute path.
			// Deleting the root itself is caught by the dangerous-command rule.
			return false
		}
		return strings.HasPrefix(candidate, trimmed+"/")
	}
	ok, err := doublestar.Match(pattern, candidate)
	return err == nil && ok
}
