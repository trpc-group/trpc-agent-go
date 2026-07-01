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
	"net"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// Decision is the guard's verdict for a tool call. needs_human_review is the
// public spelling of the internal ActionAsk.
type Decision string

const (
	// DecisionAllow lets the tool call run.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks the tool call.
	DecisionDeny Decision = "deny"
	// DecisionReview routes the tool call to human / model approval.
	DecisionReview Decision = "needs_human_review"
)

// Rule identifiers, stable across reports and audit events.
const (
	ruleDangerousID = "R-DEL-001"
	ruleCredID      = "R-CRED-001" //nolint:gosec // G101 false positive: a rule identifier, not a credential.
	ruleNetworkID   = "R-NET-001"
	ruleShellID     = "R-SHELL-001"
	ruleCmdID       = "R-CMD-001"
	ruleHostID      = "R-HOST-001"
	ruleDepID       = "R-DEP-001"
	ruleResourceID  = "R-RES-001"
	ruleSecretID    = "R-SECRET-001"
	ruleEnvID       = "R-ENV-001"
)

// Finding categories.
const (
	catDangerous   = "dangerous_command"
	catCredential  = "credential_access" //nolint:gosec // G101 false positive: a finding category, not a credential.
	catNetwork     = "network"
	catShellBypass = "shell_bypass"
	catCommandPol  = "command_policy"
	catHostRisk    = "host_risk"
	catDependency  = "dependency"
	catResource    = "resource_abuse"
	catSecret      = "secret_leak"
	catEnvKey      = "env_policy"
)

// Recommendation strings attached to each finding.
const (
	recDangerous   = "Avoid destructive commands; scope deletions to the workspace and never target system paths."
	recCredential  = "This path holds credentials/keys; remove the access or use a dedicated secrets mechanism." //nolint:gosec // G101 false positive: a recommendation string, not a credential.
	recNetwork     = "Target host is not in network.allowed_domains; add it to the whitelist or use a vetted download script."
	recShellBypass = "Wrap complex shell usage in an auditable workspace script and add it to allowed_commands."
	recCommandPol  = "Command is not in commands.allowed; add it to the allow list if it is expected, or keep it blocked."
	recHost        = "Host-shell background/PTY/privilege use is high risk; prefer the sandboxed workspace_exec backend."
	recDependency  = "Dependency installs mutate the environment; vendor dependencies or run installs in a sandbox."
	recResource    = "Command may exhaust resources; lower the timeout/output or rely on sandbox runtime limits."
	recSecret      = "Command/env contains a secret-like value; pass secrets via a secret store, not inline." //nolint:gosec // G101 false positive: a recommendation string, not a credential.
	recEnv         = "Environment key is not in env.allowed_keys; add it to the whitelist or drop the override."
)

// Finding is one detected risk. action is the internal, post-override action
// the finding implies; when empty it is derived from RiskLevel.
type Finding struct {
	RuleID         string    `json:"rule_id"`
	Category       string    `json:"category"`
	RiskLevel      RiskLevel `json:"risk_level"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`

	action Action
}

// effectiveAction returns the action the finding implies after overrides.
func (f Finding) effectiveAction() Action {
	if f.action != "" {
		return f.action
	}
	return riskToAction(f.RiskLevel)
}

// ruleCtx bundles the inputs handed to every rule.
type ruleCtx struct {
	er      ExecRequest
	pipe    *shellsafe.Pipeline
	policy  *Policy
	backend string
}

// ruleFn inspects a request and returns zero or more findings.
type ruleFn func(ruleCtx) []Finding

// builtinRules runs in order; findings are aggregated by severity afterwards.
// argv[0] allow/deny and shell-wrapper detection belong to ruleCommandPolicy
// (which delegates to shellsafe); every other rule only inspects the
// argument-level risks shellsafe does not cover.
var builtinRules = []ruleFn{
	ruleCommandPolicy,
	ruleDangerousArgs,
	ruleForbiddenPath,
	ruleNetwork,
	ruleHostRisk,
	ruleDependency,
	ruleResource,
	ruleSecret,
	ruleEnvKeys,
}

// scan parses the command once, runs every rule, applies overrides and
// aggregates the verdict. A command shellsafe cannot parse yields a shell-
// bypass finding whose action is the policy's unparsable_action (fail
// closed); the secret and resource rules still run on the raw command so an
// unparsable command is not a blind spot.
func (p *Policy) scan(er ExecRequest, backend string) ([]Finding, Decision, RiskLevel) {
	var findings []Finding
	var pipe *shellsafe.Pipeline
	if backend != BackendCode && strings.TrimSpace(er.Command) != "" {
		parsed, err := shellsafe.Parse(er.Command)
		if err != nil {
			f := shellBypassFinding(err)
			f.action = p.UnparsableAction
			findings = append(findings, f)
		} else {
			pipe = parsed
		}
	}
	ctx := ruleCtx{er: er, pipe: pipe, policy: p, backend: backend}
	for _, rule := range builtinRules {
		findings = append(findings, rule(ctx)...)
	}
	findings = applyOverrides(findings, p.RuleOverrides)
	decision, risk := p.decide(findings)
	return findings, decision, risk
}

// decide aggregates findings into a single decision and the highest risk seen.
// Only when no finding carries an explicit action does it fall back to the
// policy default action. A finding overridden to allow (actionRank 0) ranks the
// same as the empty sentinel, so "an action was set" is tracked separately from
// the ranked action; otherwise an explicit allow would be lost to a deny
// default_action.
func (p *Policy) decide(findings []Finding) (Decision, RiskLevel) {
	top := RiskNone
	strongest := Action("")
	actionSet := false
	for _, f := range findings {
		if riskRank(f.RiskLevel) > riskRank(top) {
			top = f.RiskLevel
		}
		a := f.effectiveAction()
		if a == "" {
			continue
		}
		if !actionSet || actionRank(a) > actionRank(strongest) {
			strongest = a
			actionSet = true
		}
	}
	if !actionSet {
		strongest = p.DefaultAction
	}
	return actionToDecision(strongest), top
}

// applyOverrides rewrites the risk level and/or action of findings whose rule
// id appears in the overrides map.
func applyOverrides(findings []Finding, overrides map[string]Override) []Finding {
	if len(overrides) == 0 {
		return findings
	}
	for i := range findings {
		ov, ok := overrides[findings[i].RuleID]
		if !ok {
			continue
		}
		if ov.RiskLevel != "" {
			findings[i].RiskLevel = ov.RiskLevel
		}
		if ov.Action != "" {
			findings[i].action = ov.Action
		}
	}
	return findings
}

// shellBypassFinding wraps a shellsafe parse error as a finding.
func shellBypassFinding(err error) Finding {
	return Finding{
		RuleID:         ruleShellID,
		Category:       catShellBypass,
		RiskLevel:      RiskHigh,
		Evidence:       "unparsable command: " + err.Error(),
		Recommendation: recShellBypass,
	}
}

// ruleCommandPolicy delegates argv[0] allow/deny and shell-wrapper detection to
// shellsafe in a single call. The three shellsafe failure modes map to three
// distinct findings so the report is not misleading: a user-denied command is a
// dangerous command (R-DEL-001); a shell wrapper / re-executing builtin that can
// bypass the allow/deny list is a shell bypass (R-SHELL-001); a plain command
// that is simply not in the allow list is an allow-list miss (R-CMD-001), not a
// "bypass". All three default-deny at high/critical risk; only the label and
// rule id differ.
func ruleCommandPolicy(c ruleCtx) []Finding {
	if c.pipe == nil {
		return nil
	}
	sp := c.policy.shellPolicy()
	if !sp.Active() {
		return nil
	}
	err := sp.Check(c.pipe)
	if err == nil {
		return nil
	}
	if cmd, ok := deniedSegment(c.pipe, c.policy.Commands.Denied); ok {
		return []Finding{{
			RuleID:         ruleDangerousID,
			Category:       catDangerous,
			RiskLevel:      RiskCritical,
			Evidence:       "denied command: " + cmd,
			Recommendation: recDangerous,
		}}
	}
	if isAllowListMiss(err) {
		return []Finding{{
			RuleID:         ruleCmdID,
			Category:       catCommandPol,
			RiskLevel:      RiskHigh,
			Evidence:       err.Error(),
			Recommendation: recCommandPol,
		}}
	}
	return []Finding{{
		RuleID:         ruleShellID,
		Category:       catShellBypass,
		RiskLevel:      RiskHigh,
		Evidence:       err.Error(),
		Recommendation: recShellBypass,
	}}
}

// isAllowListMiss reports whether a shellsafe error is the "not in the allow
// list" case rather than a shell-wrapper / re-executing-builtin rejection. The
// substring matches the message shellsafe.Policy.Check returns for an
// allow-list miss; anything else (wrappers, implicit deny) stays a shell bypass.
func isAllowListMiss(err error) bool {
	return err != nil && strings.Contains(err.Error(), "is not in allowed_commands")
}

// ruleDangerousArgs catches argument-level destructive patterns that shellsafe
// does not see: recursive+force rm, escalated to critical when the target is
// the root or a system directory.
func ruleDangerousArgs(c ruleCtx) []Finding {
	if c.pipe == nil {
		return nil
	}
	var out []Finding
	for _, argv := range c.pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		if lowerBase(argv[0]) != "rm" || !hasRecursiveForce(argv[1:]) {
			continue
		}
		risk := RiskHigh
		for _, a := range argv[1:] {
			if !strings.HasPrefix(a, "-") && isRootOrSystem(a) {
				risk = RiskCritical
				break
			}
		}
		out = append(out, Finding{
			RuleID:         ruleDangerousID,
			Category:       catDangerous,
			RiskLevel:      risk,
			Evidence:       strings.Join(argv, " "),
			Recommendation: recDangerous,
		})
	}
	return out
}

// ruleForbiddenPath flags any argv word or cwd that matches a forbidden path
// (credentials, ssh keys, .env, ...).
func ruleForbiddenPath(c ruleCtx) []Finding {
	var out []Finding
	seen := make(map[string]bool)
	for _, cand := range pathCandidates(c) {
		pat, ok := c.policy.forbiddenMatch(cand)
		if !ok || seen[cand] {
			continue
		}
		seen[cand] = true
		out = append(out, Finding{
			RuleID:         ruleCredID,
			Category:       catCredential,
			RiskLevel:      RiskCritical,
			Evidence:       cand + " (matches " + pat + ")",
			Recommendation: recCredential,
		})
	}
	return out
}

// ruleNetwork flags download commands whose target host is not whitelisted.
// The finding's action follows network.on_non_whitelisted.
func ruleNetwork(c ruleCtx) []Finding {
	if c.pipe == nil {
		return nil
	}
	dl := toLowerSet(c.policy.Network.DownloadCommands)
	var out []Finding
	for _, argv := range c.pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		cmd := lowerBase(argv[0])
		if !dl[cmd] {
			continue
		}
		for _, host := range extractHosts(cmd, argv[1:]) {
			if c.policy.domainAllowed(host) {
				continue
			}
			out = append(out, Finding{
				RuleID:         ruleNetworkID,
				Category:       catNetwork,
				RiskLevel:      RiskHigh,
				Evidence:       argv[0] + " -> " + host,
				Recommendation: recNetwork,
				action:         c.policy.Network.OnNonWhitelisted,
			})
		}
	}
	return out
}

// ruleHostRisk only applies to the host backend: background/PTY sessions and
// privilege escalation are higher risk on the host shell than in the sandbox.
func ruleHostRisk(c ruleCtx) []Finding {
	if c.backend != BackendHost {
		return nil
	}
	var out []Finding
	r := c.policy.Resources
	if c.er.Background && r.DenyBackgroundOnHost {
		out = append(out, hostFinding(RiskHigh, "background process on host shell"))
	}
	if c.er.PTY && r.DenyPTYOnHost {
		out = append(out, hostFinding(RiskHigh, "PTY/TTY session on host shell"))
	}
	if c.pipe != nil {
		for _, argv := range c.pipe.Commands {
			if len(argv) == 0 {
				continue
			}
			switch lowerBase(argv[0]) {
			case "sudo", "su", "doas":
				out = append(out, hostFinding(RiskCritical, "privilege escalation: "+argv[0]))
			case "nohup":
				out = append(out, hostFinding(RiskHigh, "nohup detaches a process from the session"))
			}
		}
	}
	return out
}

func hostFinding(risk RiskLevel, evidence string) Finding {
	return Finding{
		RuleID:         ruleHostID,
		Category:       catHostRisk,
		RiskLevel:      risk,
		Evidence:       evidence,
		Recommendation: recHost,
	}
}

// ruleDependency flags configured dependency-install subcommands.
func ruleDependency(c ruleCtx) []Finding {
	if c.pipe == nil {
		return nil
	}
	var out []Finding
	for _, argv := range c.pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		base := lowerBase(argv[0])
		for _, sub := range c.policy.DeniedSubcommands {
			if strings.EqualFold(sub.Cmd, base) && argsHavePrefix(argv[1:], sub.ArgsPrefix) {
				out = append(out, Finding{
					RuleID:         ruleDepID,
					Category:       catDependency,
					RiskLevel:      RiskMedium,
					Evidence:       strings.Join(argv, " "),
					Recommendation: recDependency,
				})
				break
			}
		}
	}
	return out
}

// ruleResource is a best-effort pre-filter for resource abuse. The real
// enforcement is the runtime timeout / output cap in workspaceexec and the
// sandbox.
func ruleResource(c ruleCtx) []Finding {
	var out []Finding
	r := c.policy.Resources
	if r.MaxTimeoutSec > 0 && c.er.TimeoutSec > r.MaxTimeoutSec {
		out = append(out, resourceFinding(RiskMedium,
			fmt.Sprintf("timeout %ds exceeds max %ds", c.er.TimeoutSec, r.MaxTimeoutSec)))
	}
	if c.pipe != nil {
		for _, argv := range c.pipe.Commands {
			if len(argv) == 0 {
				continue
			}
			switch lowerBase(argv[0]) {
			case "sleep":
				if len(argv) > 1 {
					if secs, ok := parseSleep(argv[1]); ok && r.MaxSleepSec > 0 && secs > r.MaxSleepSec {
						out = append(out, resourceFinding(RiskMedium, "sleep "+argv[1]))
					}
				}
			case "yes":
				out = append(out, resourceFinding(RiskHigh, "yes produces unbounded output"))
			}
		}
	}
	low := strings.ToLower(c.er.Command)
	if strings.Contains(low, "while true") ||
		strings.Contains(strings.ReplaceAll(low, " ", ""), "for(;;)") {
		out = append(out, resourceFinding(RiskHigh, "infinite loop pattern"))
	}
	return out
}

func resourceFinding(risk RiskLevel, evidence string) Finding {
	return Finding{
		RuleID:         ruleResourceID,
		Category:       catResource,
		RiskLevel:      risk,
		Evidence:       evidence,
		Recommendation: recResource,
	}
}

// ruleSecret flags secret-like values in the command string or env values. The
// evidence is intentionally generic so the secret itself is never embedded.
func ruleSecret(c ruleCtx) []Finding {
	res := c.policy.compiled.secretRes
	if len(res) == 0 {
		return nil
	}
	var out []Finding
	if matchAnyRegex(res, c.er.Command) {
		out = append(out, secretFinding("secret-like value in command"))
	}
	for k, v := range c.er.Env {
		if matchAnyRegex(res, v) {
			out = append(out, secretFinding("secret-like value in env "+k))
		}
	}
	return out
}

func secretFinding(evidence string) Finding {
	return Finding{
		RuleID:         ruleSecretID,
		Category:       catSecret,
		RiskLevel:      RiskMedium,
		Evidence:       evidence,
		Recommendation: recSecret,
	}
}

// ruleEnvKeys flags environment-variable keys not present in env.allowed_keys.
// It is opt-in: with an empty allow list the rule is inert. The guard can only
// flag a non-whitelisted key, not strip it; actual env isolation is enforced by
// the runtime (workspaceexec / sandbox).
func ruleEnvKeys(c ruleCtx) []Finding {
	allowed := c.policy.Env.AllowedKeys
	if len(allowed) == 0 || len(c.er.Env) == 0 {
		return nil
	}
	set := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		set[k] = true
	}
	keys := make([]string, 0, len(c.er.Env))
	for k := range c.er.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Finding
	for _, k := range keys {
		if !set[k] {
			out = append(out, Finding{
				RuleID:         ruleEnvID,
				Category:       catEnvKey,
				RiskLevel:      RiskMedium,
				Evidence:       "env key not in allowed_keys: " + k,
				Recommendation: recEnv,
			})
		}
	}
	return out
}

var (
	urlRe      = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s'"]+`)
	userHostRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+)(?::.*)?$`)
	domainRe   = regexp.MustCompile(`(?i)^[a-z0-9.-]+$`)
)

// optionsWithValue lists, per download command, the option flags whose value is
// the *next* argument, so that value is not mistaken for a bare host (e.g.
// "curl -o config.yaml" must not treat config.yaml as a host). Only flags that
// commonly take a filename or arbitrary string are listed; a value-taking flag
// left out would at worst yield an extra fail-closed host candidate. Crucially
// the reverse mistake is avoided: boolean flags (curl -sSL, -v, wget -q) are
// NOT listed, so the operand after them is still parsed and "curl -sSL evil.io"
// cannot bypass the whitelist. The "--flag=value" form carries its value inline
// and consumes no following argument.
var optionsWithValue = map[string]map[string]bool{
	"curl": {
		"-o": true, "--output": true, "-T": true, "--upload-file": true,
		"-d": true, "--data": true, "-F": true, "--form": true,
		"-H": true, "--header": true, "-A": true, "--user-agent": true,
		"-e": true, "--referer": true, "-b": true, "--cookie": true,
		"-c": true, "--cookie-jar": true, "-u": true, "--user": true,
		"-K": true, "--config": true,
	},
	"wget": {
		"-O": true, "--output-document": true, "-o": true, "--output-file": true,
		"-a": true, "--append-output": true, "-P": true, "--directory-prefix": true,
		"-U": true, "--user-agent": true, "--header": true,
	},
}

// extractHosts pulls candidate hosts from a download command's arguments. It is
// only called for configured download commands (curl, wget, nc, ssh, scp, ...),
// all of which take a host/URL operand, so bare domain-like operands are parsed
// for every such command. To avoid mistaking a filename for a host (e.g.
// curl -o config.yaml), the operand that immediately follows a value-taking
// option (optionsWithValue) is skipped; boolean flags such as "curl -sSL" do
// not consume their following operand, so "curl -sSL evil.io" is still flagged.
// URLs and user@host operands are detected unconditionally as they are
// unambiguous.
func extractHosts(cmd string, args []string) []string {
	var hosts []string
	valueOpts := optionsWithValue[cmd]
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if m := urlRe.FindString(a); m != "" {
			if u, err := url.Parse(m); err == nil && u.Hostname() != "" {
				hosts = append(hosts, u.Hostname())
				continue
			}
		}
		// user@host or scp/ssh user@host:/path.
		if mm := userHostRe.FindStringSubmatch(a); mm != nil {
			hosts = append(hosts, mm[1])
			continue
		}
		if strings.HasPrefix(a, "-") {
			// An option that consumes the following argument as its value; the
			// "--flag=value" form is self-contained and consumes nothing.
			if valueOpts[a] && !strings.Contains(a, "=") {
				skipNext = true
			}
			continue
		}
		// Bare host, host:port, scp host:/path, or schemeless host/path. Strip
		// everything from the first colon (port / scp path) and then from the
		// first slash (URL path) so a trailing port or path cannot hide the
		// host. A local relative path like "dir/config" survives as "dir",
		// which is not domain-like and is dropped below.
		host := a
		if i := strings.IndexByte(host, ':'); i > 0 {
			host = host[:i]
		}
		if i := strings.IndexByte(host, '/'); i >= 0 {
			host = host[:i]
		}
		// A surviving @ means this was a user@path form, not a bare host.
		if host == "" || strings.ContainsRune(host, '@') {
			continue
		}
		// Accept domains and raw IPs (ssh 1.2.3.4 must not bypass the whitelist).
		if domainLike(host) || net.ParseIP(host) != nil {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func domainLike(s string) bool {
	return strings.Contains(s, ".") && domainRe.MatchString(s) &&
		strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

// hasRecursiveForce reports whether the flags request both recursive and force
// deletion, covering separate, combined, and long-option spellings
// (-r -f, -rf, -fr, -Rf, --recursive --force).
func hasRecursiveForce(args []string) bool {
	recursive, force := false, false
	for _, a := range args {
		la := strings.ToLower(a)
		switch {
		case la == "--recursive" || la == "-r":
			recursive = true
		case la == "--force" || la == "-f":
			force = true
		case strings.HasPrefix(la, "-") && !strings.HasPrefix(la, "--"):
			flags := la[1:]
			if strings.ContainsRune(flags, 'r') {
				recursive = true
			}
			if strings.ContainsRune(flags, 'f') {
				force = true
			}
		}
	}
	return recursive && force
}

var systemDirs = []string{
	"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64",
	"/boot", "/sys", "/proc", "/var", "/dev", "/root",
}

func isRootOrSystem(p string) bool {
	clean := strings.TrimSpace(filepath.ToSlash(p))
	clean = strings.TrimRight(clean, "/")
	if clean == "" || p == "/" {
		return true
	}
	for _, sys := range systemDirs {
		if clean == sys || strings.HasPrefix(clean, sys+"/") {
			return true
		}
	}
	return false
}

func pathCandidates(c ruleCtx) []string {
	var out []string
	if strings.TrimSpace(c.er.Cwd) != "" {
		out = append(out, c.er.Cwd)
	}
	if c.pipe != nil {
		for _, argv := range c.pipe.Commands {
			out = append(out, argv...)
		}
	}
	return out
}

// argsHavePrefix reports whether args, after skipping leading option flags,
// begins with the prefix sequence (e.g. "install").
func argsHavePrefix(args, prefix []string) bool {
	if len(prefix) == 0 {
		return false
	}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	if i+len(prefix) > len(args) {
		return false
	}
	for j, p := range prefix {
		if !strings.EqualFold(args[i+j], p) {
			return false
		}
	}
	return true
}

// parseSleep parses a sleep argument into seconds, honoring s/m/h suffixes.
func parseSleep(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	mult := 1
	switch s[len(s)-1] {
	case 's', 'S':
		s = s[:len(s)-1]
	case 'm', 'M':
		mult, s = 60, s[:len(s)-1]
	case 'h', 'H':
		mult, s = 3600, s[:len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n * mult, true
}

func matchAnyRegex(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func deniedSegment(pipe *shellsafe.Pipeline, denied []string) (string, bool) {
	set := toLowerSet(denied)
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		if set[lowerBase(argv[0])] {
			return argv[0], true
		}
	}
	return "", false
}

func toLowerSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			m[lowerBase(it)] = true
		}
	}
	return m
}

var windowsExecExts = []string{".exe", ".cmd", ".bat", ".com", ".ps1"}

// lowerBase returns the lower-cased basename of a command, stripping common
// Windows executable suffixes so "Curl.EXE" and "curl" compare equal.
func lowerBase(cmd string) string {
	b := strings.ToLower(path.Base(filepath.ToSlash(cmd)))
	for _, ext := range windowsExecExts {
		if strings.HasSuffix(b, ext) {
			return b[:len(b)-len(ext)]
		}
	}
	return b
}

func riskToAction(r RiskLevel) Action {
	switch r {
	case RiskCritical, RiskHigh:
		return ActionDeny
	case RiskMedium:
		return ActionAsk
	default:
		return ""
	}
}

func riskRank(r RiskLevel) int {
	switch r {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

func actionRank(a Action) int {
	switch a {
	case ActionDeny:
		return 2
	case ActionAsk:
		return 1
	default:
		return 0
	}
}

func actionToDecision(a Action) Decision {
	switch a {
	case ActionDeny:
		return DecisionDeny
	case ActionAsk:
		return DecisionReview
	default:
		return DecisionAllow
	}
}
