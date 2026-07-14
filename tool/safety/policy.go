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

	"gopkg.in/yaml.v3"
)

// EnvPolicyPath is the environment variable that points at a policy file.
// When set and no explicit path is given, LoadPolicyFromEnv reads it.
const EnvPolicyPath = "TRPC_AGENT_TOOL_SAFETY_POLICY"

// NetworkPolicy configures network-egress detection.
type NetworkPolicy struct {
	// Commands are executables treated as network clients.
	Commands []string `json:"commands" yaml:"commands"`
	// AllowedDomains are hosts a network command may reach. A command whose
	// target host is not covered here is denied.
	AllowedDomains []string `json:"allowed_domains" yaml:"allowed_domains"`
}

// DependencyRule matches a dependency-install invocation.
type DependencyRule struct {
	// Cmd is the executable basename, e.g. "pip".
	Cmd string `json:"cmd" yaml:"cmd"`
	// ArgsPrefix are argument tokens that must all be present, e.g. ["install"].
	ArgsPrefix []string `json:"args_prefix" yaml:"args_prefix"`
}

// DependencyPolicy configures dependency-install detection.
type DependencyPolicy struct {
	// Decision is the verdict for a matched install command.
	Decision Decision `json:"decision" yaml:"decision"`
	// Patterns are the install invocations to match.
	Patterns []DependencyRule `json:"patterns" yaml:"patterns"`
}

// LimitsPolicy configures resource limits.
type LimitsPolicy struct {
	// MaxTimeoutSec is the largest accepted requested timeout, in seconds.
	MaxTimeoutSec int `json:"max_timeout_sec" yaml:"max_timeout_sec"`
	// MaxOutputBytes caps an exec tool's captured output. It is enforced at
	// execution time by the AfterTool output-limit callback
	// (PermissionPolicy.OutputLimitCallback), which truncates output beyond
	// this size. Zero disables the cap.
	MaxOutputBytes int64 `json:"max_output_bytes" yaml:"max_output_bytes"`
}

// SecretPattern names a regular expression that identifies an inline secret.
type SecretPattern struct {
	// Name labels the pattern in redaction and reports.
	Name string `json:"name" yaml:"name"`
	// Regex is the secret-matching regular expression (Go/RE2 syntax).
	Regex string `json:"regex" yaml:"regex"`
}

// Policy is the configurable ruleset that drives the scanner. Every risk
// dimension (allowed/denied commands, forbidden paths, network allowlist,
// limits, secret patterns) is data-driven so behaviour changes without code
// changes.
type Policy struct {
	// Version is the policy schema version.
	Version int `json:"version" yaml:"version"`
	// EnforceAllowlist, when true, asks for approval on any command not in
	// AllowedCommands and not otherwise flagged. Off by default so the guard
	// is risk-based rather than a strict allowlist.
	EnforceAllowlist bool `json:"enforce_allowlist" yaml:"enforce_allowlist"`
	// DefaultDecisionOnParseFailure is applied when a command cannot be
	// conservatively parsed (deny or ask; never allow).
	DefaultDecisionOnParseFailure Decision `json:"default_decision_on_parse_failure" yaml:"default_decision_on_parse_failure"`
	// AllowedCommands are executables permitted when EnforceAllowlist is on.
	AllowedCommands []string `json:"allowed_commands" yaml:"allowed_commands"`
	// DeniedCommands are executables always blocked.
	DeniedCommands []string `json:"denied_commands" yaml:"denied_commands"`
	// DeniedPathPatterns are path globs whose access is blocked (secrets,
	// credentials, sensitive system files).
	DeniedPathPatterns []string `json:"denied_path_patterns" yaml:"denied_path_patterns"`
	// Network configures egress detection.
	Network NetworkPolicy `json:"network" yaml:"network"`
	// DependencyInstall configures dependency-install detection.
	DependencyInstall DependencyPolicy `json:"dependency_install" yaml:"dependency_install"`
	// Limits configures resource limits.
	Limits LimitsPolicy `json:"limits" yaml:"limits"`
	// EnvWhitelist are environment variable names allowed in per-call env.
	EnvWhitelist []string `json:"env_whitelist" yaml:"env_whitelist"`
	// SecretPatterns identify inline secrets for redaction and flagging.
	SecretPatterns []SecretPattern `json:"secret_patterns" yaml:"secret_patterns"`
	// RiskOverrides bumps or lowers a rule's risk level by rule id.
	RiskOverrides map[string]RiskLevel `json:"risk_overrides" yaml:"risk_overrides"`

	// Compiled lookup structures (populated by compile, not serialised).
	compiled        bool
	deniedCmdSet    map[string]struct{}
	allowedCmdSet   map[string]struct{}
	networkCmdSet   map[string]struct{}
	envWhitelistSet map[string]struct{}
	allowedDomains  []string
	deniedPaths     []*pathMatcher
	secrets         []compiledSecret
}

type compiledSecret struct {
	name string
	re   *regexp.Regexp
}

// pathMatcher matches a normalised path argument against a policy pattern.
// Glob patterns compile to a regexp; plain patterns match by path component.
type pathMatcher struct {
	raw string
	re  *regexp.Regexp
}

func (m *pathMatcher) match(normArg string) bool {
	if m.re != nil {
		return m.re.MatchString(normArg)
	}
	p := m.raw
	switch {
	case normArg == p,
		strings.HasPrefix(normArg, p+"/"),
		strings.HasSuffix(normArg, "/"+p),
		strings.Contains(normArg, "/"+p+"/"):
		return true
	}
	if !strings.Contains(p, "/") {
		base := normArg
		if idx := strings.LastIndex(normArg, "/"); idx >= 0 {
			base = normArg[idx+1:]
		}
		return base == p
	}
	return false
}

// LoadPolicy reads a policy from a .yaml/.yml or .json file, applies defaults
// for any unset essential fields and compiles its lookup structures.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-provided config path
	if err != nil {
		return nil, fmt.Errorf("safety: read policy %q: %w", path, err)
	}
	var p Policy
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("safety: parse yaml policy %q: %w", path, err)
		}
	case ".json":
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("safety: parse json policy %q: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("safety: unsupported policy extension %q (use .yaml/.yml/.json)", filepath.Ext(path))
	}
	if err := p.compile(); err != nil {
		return nil, err
	}
	return &p, nil
}

// LoadPolicyFromEnv loads the policy referenced by EnvPolicyPath, or returns
// DefaultPolicy when the variable is unset.
func LoadPolicyFromEnv() (*Policy, error) {
	if path := strings.TrimSpace(os.Getenv(EnvPolicyPath)); path != "" {
		return LoadPolicy(path)
	}
	return DefaultPolicy(), nil
}

// DefaultPolicy returns a compiled, batteries-included policy. It mirrors the
// bundled tool_safety_policy.yaml and is used when no file is supplied.
func DefaultPolicy() *Policy {
	p := &Policy{
		Version:                       1,
		EnforceAllowlist:              false,
		DefaultDecisionOnParseFailure: DecisionDeny,
		AllowedCommands:               []string{"go", "git", "ls", "cat", "grep", "echo", "gofmt", "test", "head", "tail", "wc", "sed", "awk"},
		DeniedCommands:                []string{"rm", "dd", "mkfs", "shred", "shutdown", "reboot", "mkfs.ext4"},
		DeniedPathPatterns: []string{
			"~/.ssh", "**/id_rsa", "**/id_ed25519", "**/*.pem",
			"**/.env*", "/etc/shadow", "~/.aws/credentials", "~/.netrc",
			"~/.docker/config.json",
		},
		Network: NetworkPolicy{
			Commands:       defaultNetworkCommands(),
			AllowedDomains: []string{"github.com", "proxy.golang.org", "goproxy.cn", "pkg.go.dev"},
		},
		DependencyInstall: DependencyPolicy{
			Decision: DecisionAsk,
			Patterns: defaultDependencyRules(),
		},
		Limits:       LimitsPolicy{MaxTimeoutSec: 120, MaxOutputBytes: 1 << 20},
		EnvWhitelist: []string{"PATH", "HOME", "GOFLAGS", "GOCACHE", "GOPATH"},
		SecretPatterns: []SecretPattern{
			// Broad key=value form first so it masks the whole assignment
			// before narrower fixed-shape patterns match a prefix.
			{Name: "generic_secret", Regex: `(?i)(api[_-]?key|token|secret|password|passwd|pwd)\s*[=:]\s*['"]?[^\s'"]{6,}`},
			{Name: "aws_access_key", Regex: `AKIA[0-9A-Z]{16}`},
			{Name: "private_key", Regex: `-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----`},
		},
	}
	if err := p.compile(); err != nil {
		// DefaultPolicy is built from constant, valid data; a failure here is a
		// programming error, not a runtime condition.
		panic(fmt.Sprintf("safety: default policy failed to compile: %v", err))
	}
	return p
}

func defaultNetworkCommands() []string {
	return []string{"curl", "wget", "nc", "ncat", "ssh", "scp", "sftp", "telnet", "socat"}
}

func defaultDependencyRules() []DependencyRule {
	return []DependencyRule{
		{Cmd: "go", ArgsPrefix: []string{"install"}},
		{Cmd: "npm", ArgsPrefix: []string{"install"}},
		{Cmd: "pnpm", ArgsPrefix: []string{"install"}},
		{Cmd: "yarn", ArgsPrefix: []string{"add"}},
		{Cmd: "pip", ArgsPrefix: []string{"install"}},
		{Cmd: "pip3", ArgsPrefix: []string{"install"}},
		{Cmd: "apt", ArgsPrefix: []string{"install"}},
		{Cmd: "apt-get", ArgsPrefix: []string{"install"}},
		{Cmd: "brew", ArgsPrefix: []string{"install"}},
		{Cmd: "gem", ArgsPrefix: []string{"install"}},
		{Cmd: "cargo", ArgsPrefix: []string{"install"}},
	}
}

// applyDefaults fills essential fields that were left empty by a minimal file
// so partial policies still detect the core risks.
func (p *Policy) applyDefaults() {
	if p.DefaultDecisionOnParseFailure == "" {
		p.DefaultDecisionOnParseFailure = DecisionDeny
	}
	if len(p.Network.Commands) == 0 {
		p.Network.Commands = defaultNetworkCommands()
	}
	if p.DependencyInstall.Decision == "" {
		p.DependencyInstall.Decision = DecisionAsk
	}
	if len(p.DependencyInstall.Patterns) == 0 {
		p.DependencyInstall.Patterns = defaultDependencyRules()
	}
	if p.Limits.MaxTimeoutSec == 0 {
		p.Limits.MaxTimeoutSec = 120
	}
	if p.Limits.MaxOutputBytes == 0 {
		p.Limits.MaxOutputBytes = 1 << 20
	}
	if len(p.SecretPatterns) == 0 {
		p.SecretPatterns = DefaultPolicy().SecretPatterns
	}
}

// compile validates and builds the lookup structures. It is safe to call more
// than once.
func (p *Policy) compile() error {
	if d := p.DefaultDecisionOnParseFailure; d != "" && d != DecisionDeny && d != DecisionAsk {
		return fmt.Errorf("safety: default_decision_on_parse_failure must be deny or ask, got %q", d)
	}
	p.applyDefaults()

	// Validate enum-typed config so a typo (e.g. decision "block") cannot
	// silently rank as allow/none and defeat blocking. decisionRank/riskRank
	// treat any unknown string as the zero (allow/none) rank.
	switch p.DependencyInstall.Decision {
	case DecisionAllow, DecisionAsk, DecisionNeedsHumanReview, DecisionDeny:
	default:
		return fmt.Errorf("safety: dependency_install.decision has unknown value %q", p.DependencyInstall.Decision)
	}
	for id, r := range p.RiskOverrides {
		switch r {
		case RiskNone, RiskLow, RiskMedium, RiskHigh, RiskCritical:
		default:
			return fmt.Errorf("safety: risk_overrides[%q] has unknown risk level %q", id, r)
		}
	}

	p.deniedCmdSet = toCommandSet(p.DeniedCommands)
	p.allowedCmdSet = toCommandSet(p.AllowedCommands)
	p.networkCmdSet = toCommandSet(p.Network.Commands)

	p.envWhitelistSet = make(map[string]struct{}, len(p.EnvWhitelist))
	for _, k := range p.EnvWhitelist {
		p.envWhitelistSet[strings.ToUpper(strings.TrimSpace(k))] = struct{}{}
	}

	p.allowedDomains = p.allowedDomains[:0]
	for _, d := range p.Network.AllowedDomains {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			p.allowedDomains = append(p.allowedDomains, d)
		}
	}

	p.deniedPaths = p.deniedPaths[:0]
	for _, pat := range p.DeniedPathPatterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		norm := normalizePathArg(pat)
		if strings.ContainsAny(pat, "*?") {
			re, err := regexp.Compile(globToRegex(norm))
			if err != nil {
				return fmt.Errorf("safety: bad denied_path_pattern %q: %w", pat, err)
			}
			p.deniedPaths = append(p.deniedPaths, &pathMatcher{raw: norm, re: re})
			continue
		}
		p.deniedPaths = append(p.deniedPaths, &pathMatcher{raw: norm})
		// A "~/..." pattern also matches the same file under a concrete home
		// directory (e.g. /home/u/.aws/credentials), which normalizePathArg
		// cannot fold because the real home prefix is unknown. Add a tail
		// matcher for the home-relative portion so absolute home paths match.
		if tail := strings.TrimPrefix(norm, "~/"); tail != norm && tail != "" {
			p.deniedPaths = append(p.deniedPaths, &pathMatcher{raw: tail})
		}
	}

	p.secrets = p.secrets[:0]
	for _, s := range p.SecretPatterns {
		if strings.TrimSpace(s.Regex) == "" {
			continue
		}
		re, err := regexp.Compile(s.Regex)
		if err != nil {
			return fmt.Errorf("safety: bad secret_pattern %q: %w", s.Name, err)
		}
		p.secrets = append(p.secrets, compiledSecret{name: s.Name, re: re})
	}
	p.compiled = true
	return nil
}

// riskFor returns the configured risk override for a rule id, or the fallback.
func (p *Policy) riskFor(ruleID string, fallback RiskLevel) RiskLevel {
	if p.RiskOverrides != nil {
		if r, ok := p.RiskOverrides[ruleID]; ok && r != "" {
			return r
		}
	}
	return fallback
}

// envAllowed reports whether an environment key is on the whitelist (compared
// case-insensitively by upper-casing, matching envWhitelistSet's keys).
func (p *Policy) envAllowed(key string) bool {
	_, ok := p.envWhitelistSet[strings.ToUpper(strings.TrimSpace(key))]
	return ok
}

// matchesDeniedPath reports whether a path argument matches any denied path
// pattern, returning the matched pattern for evidence.
func (p *Policy) matchesDeniedPath(word string) (string, bool) {
	n := normalizePathArg(word)
	if n == "" {
		return "", false
	}
	for _, m := range p.deniedPaths {
		if m.match(n) {
			return m.raw, true
		}
	}
	return "", false
}

// isDomainAllowed reports whether host is covered by the allowlist, either
// exactly or as a subdomain.
func (p *Policy) isDomainAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, d := range p.allowedDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func toCommandSet(cmds []string) map[string]struct{} {
	set := make(map[string]struct{}, len(cmds))
	for _, c := range cmds {
		if b := commandBase(c); b != "" {
			set[b] = struct{}{}
		}
	}
	return set
}

// globToRegex converts a restricted path glob (**, *, ?) to an anchored regexp
// over forward-slash paths.
func globToRegex(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			switch {
			case i+2 < len(glob) && glob[i+1] == '*' && glob[i+2] == '/':
				b.WriteString("(?:.*/)?")
				i += 2
			case i+1 < len(glob) && glob[i+1] == '*':
				b.WriteString(".*")
				i++
			default:
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return b.String()
}
