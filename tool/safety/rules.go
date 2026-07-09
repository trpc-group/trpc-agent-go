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
		// An opaque curl config file (-K/--config) can define url/proxy/resolve
		// and other egress controls the guard cannot see, so it fails closed
		// regardless of the whitelist.
		if cmd == "curl" {
			if opt, ok := curlOpaqueConfigOption(argv[1:]); ok {
				out = append(out, Finding{
					RuleID:         ruleNetworkID,
					Category:       catNetwork,
					RiskLevel:      RiskHigh,
					Evidence:       argv[0] + " " + opt + " (opaque config may define url/proxy/resolve)",
					Recommendation: recNetwork,
					action:         c.policy.Network.OnNonWhitelisted,
				})
			} else if c.policy.Network.CurlRequireDisabledConfig &&
				!curlDefaultConfigDisabled(argv[1:]) {
				// curl reads an implicit default config (~/.curlrc et al.) that
				// can inject url/proxy/resolve unless -q/--disable is the first
				// option. Opt-in fail-closed; the guard cannot see the file.
				out = append(out, Finding{
					RuleID:         ruleNetworkID,
					Category:       catNetwork,
					RiskLevel:      RiskHigh,
					Evidence:       argv[0] + " (implicit curl config may define url/proxy/resolve; pass -q/--disable first)",
					Recommendation: recNetwork,
					action:         c.policy.Network.OnNonWhitelisted,
				})
			}
		} else if opt, ok := genericOpaqueOption(cmd, argv[1:]); ok {
			// Non-curl equivalents of the opaque curl config: wget
			// -e/--execute/--config, ssh/scp -o/-F, scp/sftp -S can redirect
			// the real egress (proxy, ProxyCommand, transport program) in ways
			// the guard cannot read, so they fail closed too.
			out = append(out, Finding{
				RuleID:         ruleNetworkID,
				Category:       catNetwork,
				RiskLevel:      RiskHigh,
				Evidence:       argv[0] + " " + opt + " (opaque option may redirect egress via proxy/config)",
				Recommendation: recNetwork,
				action:         c.policy.Network.OnNonWhitelisted,
			})
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

// optClass classifies an option of a non-curl download command for the network
// rule. Unlisted options are treated as boolean, which fails toward flagging: a
// boolean never consumes an operand, so the operand after it is still parsed as
// a potential host.
type optClass int

const (
	// optValue: the option's value is an opaque non-host string (filename,
	// header, credential) carried inline ("--flag=value", "-Xvalue") or in the
	// next argument; it is consumed so it is not mistaken for a bare host
	// (e.g. "wget -O config.yaml" must not treat config.yaml as a host).
	optValue optClass = iota + 1
	// optHost: the option's value names the real connection target(s) — proxy,
	// jump hosts, forwarding specs — and every host/IP in it must be checked
	// against the whitelist.
	optHost
	// optOpaque: the option is an out-of-band egress control the guard cannot
	// audit (a config file, an arbitrary rc directive or client option, a
	// replacement transport program); its mere presence fails closed, mirroring
	// curl -K/--config.
	optOpaque
)

// genericOptions classifies, per non-curl download command, the options the
// network rule must not treat as booleans. Boolean flags (wget -q, ssh -v) are
// NOT listed, so the operand after them is still parsed and "wget -q evil.io"
// cannot bypass the whitelist. Long options match exactly; single-letter
// options are also resolved inside getopt-style bundles and inline values
// ("-qe X", "-Jhost", "-oKey=Val"). curl is not here — its richer option
// surface is handled by extractCurlHosts / curlOpaqueConfigOption.
var genericOptions = map[string]map[string]optClass{
	"wget": {
		"-O": optValue, "--output-document": optValue,
		"-o": optValue, "--output-file": optValue,
		"-a": optValue, "--append-output": optValue,
		"-P": optValue, "--directory-prefix": optValue,
		"-U": optValue, "--user-agent": optValue,
		"--header": optValue, "--proxy-user": optValue, "--proxy-password": optValue,
		// -e/--execute injects an arbitrary .wgetrc directive
		// (http_proxy/https_proxy/use_proxy redirect the real egress);
		// --config points at an opaque config file. Both fail closed.
		"-e": optOpaque, "--execute": optOpaque, "--config": optOpaque,
	},
	"ssh": {
		// -J routes through jump hosts; -W/-L/-R carry host:port forwarding
		// targets. Every host in their values is whitelist-checked.
		"-J": optHost, "-W": optHost, "-L": optHost, "-R": optHost,
		// -o sets any client option (ProxyCommand/ProxyJump/Hostname);
		// -F reads an opaque config file. Both fail closed.
		"-o": optOpaque, "-F": optOpaque,
		"-i": optValue, "-l": optValue, "-p": optValue, "-b": optValue,
		"-c": optValue, "-m": optValue, "-e": optValue, "-E": optValue,
		"-D": optValue, "-I": optValue, "-Q": optValue, "-S": optValue,
		"-w": optValue, "-B": optValue,
	},
	"scp": {
		"-J": optHost,
		// -o/-F as for ssh; -S swaps in an arbitrary transport program that
		// then owns the connection, so it is as opaque as a ProxyCommand.
		"-o": optOpaque, "-F": optOpaque, "-S": optOpaque,
		"-i": optValue, "-l": optValue, "-P": optValue, "-c": optValue,
		"-D": optValue, "-X": optValue,
	},
	"sftp": {
		"-J": optHost,
		"-o": optOpaque, "-F": optOpaque, "-S": optOpaque,
		"-i": optValue, "-l": optValue, "-P": optValue, "-c": optValue,
		"-b": optValue, "-B": optValue, "-D": optValue, "-R": optValue,
		"-X": optValue,
	},
	"nc": {
		// -x routes through a SOCKS/HTTP proxy.
		"-x": optHost,
		"-X": optValue, "-p": optValue, "-s": optValue, "-w": optValue,
		"-i": optValue, "-I": optValue, "-O": optValue, "-T": optValue,
	},
}

// curlHostBearingLong are curl long options whose value carries a connection
// target or the request URL that the whitelist must still see. --connect-to and
// --resolve redirect the connection to a host different from the request URL;
// --proxy/--preproxy route all traffic through a proxy; --url sets the request
// URL out of band. Every host/IP in the value is extracted so a redirect such as
// "--connect-to github.com:443:evil.io:443" cannot smuggle evil.io past a
// github.com whitelist.
var curlHostBearingLong = map[string]bool{
	"--connect-to": true, "--resolve": true,
	"--proxy": true, "--preproxy": true, "--url": true,
	"--dns-servers": true, "--doh-url": true,
}

// curlLongValueOptions are curl long options whose value is an opaque
// string/filename to skip (so it is not mistaken for a host). Host-bearing long
// options are handled separately and are intentionally absent here.
var curlLongValueOptions = map[string]bool{
	"--output": true, "--upload-file": true, "--data": true, "--data-binary": true,
	"--data-raw": true, "--data-ascii": true, "--form": true, "--header": true,
	"--user-agent": true, "--referer": true, "--cookie": true, "--cookie-jar": true,
	"--user": true, "--config": true, "--output-dir": true,
}

// curlShortValueBytes are curl short flags that consume a value: the value is the
// remainder of the bundle token if any, else the next argument. 'x' is the proxy
// flag (its value is a host); 'K' is the opaque config file (detected for
// fail-closed by curlOpaqueConfigOption). Boolean flags (s, S, L, f, v, k, ...)
// are absent, so "curl -sSL evil.io" still parses evil.io.
var curlShortValueBytes = map[byte]bool{
	'o': true, 'T': true, 'd': true, 'F': true, 'H': true, 'A': true,
	'e': true, 'b': true, 'c': true, 'u': true, 'K': true, 'x': true,
}

// extractHosts pulls candidate hosts from a download command's arguments. It is
// only called for configured download commands (curl, wget, nc, ssh, scp, ...),
// all of which take a host/URL operand. curl gets a dedicated parser because its
// option surface (short bundles, connection-redirect and proxy options, the
// opaque config file) is where whitelist bypasses hide; other commands share a
// generic parser driven by the per-command genericOptions classification
// (opaque egress controls are handled separately by genericOpaqueOption in
// ruleNetwork).
func extractHosts(cmd string, args []string) []string {
	if cmd == "curl" {
		return extractCurlHosts(args)
	}
	return extractGenericHosts(cmd, args)
}

// extractGenericHosts parses non-curl download commands (wget, nc, ssh, scp,
// ftp, ...). A value-taking option (genericOptions) consumes its following
// operand; a host-bearing option (proxy, jump host, forwarding spec)
// contributes every host in its value; everything else that is a URL,
// user@host or bare domain/IP operand is a host candidate. Short options are
// resolved through getopt-style bundles ("-vJhost", "-qO file") so a bundled
// host-bearing flag cannot hide its value.
func extractGenericHosts(cmd string, args []string) []string {
	var hosts []string
	opts := genericOptions[cmd]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || len(a) == 1 {
			hosts = append(hosts, operandHosts(a)...)
			continue
		}
		var cl optClass
		var inline string
		var hasInline bool
		if strings.HasPrefix(a, "--") {
			var flag string
			flag, inline, hasInline = splitFlagValue(a)
			cl = opts[flag]
		} else {
			cl, _, inline, hasInline = scanGenericShortBundle(opts, a)
		}
		switch cl {
		case optHost:
			val := inline
			if !hasInline && i+1 < len(args) {
				val, i = args[i+1], i+1
			}
			hosts = append(hosts, hostsFromGenericValue(val)...)
		case optValue, optOpaque:
			if !hasInline {
				i++
			}
		}
	}
	return hosts
}

// scanGenericShortBundle walks a getopt-style short-option token ("-vJhost").
// Letters not in opts are booleans and are skipped; the first classified
// (value-taking) letter wins and its value is the remainder of the token when
// non-empty, else the next argument. A token with no classified letter is all
// booleans: class 0, nothing consumed.
func scanGenericShortBundle(opts map[string]optClass, token string) (cl optClass, flag, inline string, hasInline bool) {
	for j := 1; j < len(token); j++ {
		f := "-" + string(token[j])
		c, ok := opts[f]
		if !ok {
			continue
		}
		rest := token[j+1:]
		return c, f, rest, rest != ""
	}
	return 0, "", "", false
}

// hostsFromGenericValue extracts every host/IP from a host-bearing option
// value: comma-separated [user@]host[:port] hops (ssh/scp -J), a
// [bind:]port:host:hostport forwarding spec (ssh -W/-L/-R) or a proxy address
// (nc -x). Ports and empty fields are dropped; it is deliberately
// over-inclusive so any non-whitelisted destination in the spec trips the
// network rule.
func hostsFromGenericValue(val string) []string {
	var hosts []string
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if m := userHostRe.FindStringSubmatch(part); m != nil {
			hosts = append(hosts, m[1])
			continue
		}
		hosts = append(hosts, hostsFromColonSpec(part)...)
	}
	return hosts
}

// genericOpaqueOption reports whether a non-curl download command carries an
// option classified optOpaque (wget -e/--execute/--config, ssh/scp -o/-F,
// scp/sftp -S): an out-of-band egress control whose effect the guard cannot
// read, so its mere presence fails closed, mirroring curl -K/--config. Short
// options are resolved through getopt bundles ("-qe X", "-oKey=Val") so a
// bundled opaque flag still counts, while an opaque letter inside another
// option's inline value does not.
func genericOpaqueOption(cmd string, args []string) (string, bool) {
	opts := genericOptions[cmd]
	if len(opts) == 0 {
		return "", false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || len(a) == 1 {
			continue
		}
		var cl optClass
		var flag string
		var hasInline bool
		if strings.HasPrefix(a, "--") {
			flag, _, hasInline = splitFlagValue(a)
			cl = opts[flag]
		} else {
			cl, flag, _, hasInline = scanGenericShortBundle(opts, a)
		}
		if cl == optOpaque {
			return flag, true
		}
		// Any value-taking option consumes the next argument, so that value is
		// not itself scanned as an option token.
		if cl != 0 && !hasInline {
			i++
		}
	}
	return "", false
}

// extractCurlHosts parses curl arguments, resolving short-flag bundles, long
// options in both "--flag value" and "--flag=value" forms, connection-redirect
// and proxy options, and the request URL operand. Boolean flags never consume an
// operand, so "curl -sSL evil.io" is still flagged.
func extractCurlHosts(args []string) []string {
	var hosts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--"):
			flag, inlineVal, hasInline := splitFlagValue(a)
			if curlHostBearingLong[flag] {
				val := inlineVal
				if !hasInline && i+1 < len(args) {
					val, i = args[i+1], i+1
				}
				hosts = append(hosts, hostsFromCurlValue(flag, val)...)
				continue
			}
			if curlLongValueOptions[flag] && !hasInline {
				i++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			extra, consumesNext, proxyNext := parseCurlShortBundle(a)
			hosts = append(hosts, extra...)
			if consumesNext && i+1 < len(args) {
				if proxyNext {
					hosts = append(hosts, hostsFromCurlValue("-x", args[i+1])...)
				}
				i++
			}
		default:
			hosts = append(hosts, operandHosts(a)...)
		}
	}
	return hosts
}

// parseCurlShortBundle walks a "-sSx"-style short-flag bundle. curl's first
// value-taking flag consumes the remainder of the token as its value, or the
// next argument when the token ends there. It returns any hosts read from an
// inline proxy value, whether the following argument is consumed as a value, and
// whether that following argument is a proxy host (so the caller parses it).
func parseCurlShortBundle(token string) (hosts []string, consumesNext, proxyNext bool) {
	for j := 1; j < len(token); j++ {
		c := token[j]
		if !curlShortValueBytes[c] {
			continue // boolean flag; keep scanning the bundle
		}
		rest := token[j+1:]
		if c == 'x' { // proxy: value is the rest of the token or the next arg
			if rest != "" {
				return hostsFromCurlValue("-x", rest), false, false
			}
			return nil, true, true
		}
		// Other value flags (incl 'K'): consume the rest of the token, or the
		// next arg when the value is not inline. The value itself is not a host.
		return nil, rest == "", false
	}
	return nil, false, false
}

// operandHosts extracts host candidates from a non-option operand: a full URL, a
// user@host (scp/ssh) form, or a bare domain/IP (with optional port or path).
func operandHosts(a string) []string {
	if h := hostFromURL(a); h != "" {
		return []string{h}
	}
	if mm := userHostRe.FindStringSubmatch(a); mm != nil {
		return []string{mm[1]}
	}
	return bareHost(a)
}

// hostFromURL returns the host of a URL embedded in a, or "" when there is none.
func hostFromURL(a string) string {
	if m := urlRe.FindString(a); m != "" {
		if u, err := url.Parse(m); err == nil {
			return u.Hostname()
		}
	}
	return ""
}

// bareHost extracts a single host from a "host[:port][/path]" operand. It strips
// the port and path and drops non-host tokens (relative paths, user@path forms,
// pure ports). Both domains and raw IPs are accepted so "ssh 1.2.3.4" cannot
// bypass the whitelist.
func bareHost(a string) []string {
	host := a
	if j := strings.IndexByte(host, ':'); j > 0 {
		host = host[:j]
	}
	if j := strings.IndexByte(host, '/'); j >= 0 {
		host = host[:j]
	}
	if host == "" || strings.ContainsRune(host, '@') {
		return nil
	}
	if domainLike(host) || net.ParseIP(host) != nil {
		return []string{host}
	}
	return nil
}

// splitFlagValue splits "--flag=value" into its parts. Without an "=" it returns
// the whole flag and hasInline=false.
func splitFlagValue(arg string) (flag, val string, hasInline bool) {
	if i := strings.IndexByte(arg, '='); i >= 0 {
		return arg[:i], arg[i+1:], true
	}
	return arg, "", false
}

// hostsFromCurlValue extracts the real destination host(s)/IP(s) from a curl
// host-bearing option value.
func hostsFromCurlValue(flag, val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	switch flag {
	case "-x", "--proxy", "--preproxy":
		// [scheme://]host[:port].
		if strings.Contains(val, "://") {
			if u, err := url.Parse(val); err == nil && u.Hostname() != "" {
				return []string{u.Hostname()}
			}
		}
		return hostsFromColonSpec(val)
	case "--resolve":
		// [+]HOST:PORT:ADDR[,ADDR]. The address tail is everything after the
		// second colon, so a dedicated parser is needed: the generic colon
		// splitter shatters an unbracketed IPv6 addr (2001:db8::1) into port-
		// like fragments that all get dropped, letting the rewrite ride a
		// whitelisted HOST past the network rule.
		return hostsFromResolveSpec(val)
	case "--connect-to", "--dns-servers":
		// HOST1:PORT1:HOST2:PORT2 / IP[,IP]: every host/IP field (an alternate
		// DNS server is itself an egress control). IPv6 here requires brackets,
		// which hostsFromColonSpec already honors.
		return hostsFromColonSpec(val)
	case "--url", "--doh-url":
		if h := hostFromURL(val); h != "" {
			return []string{h}
		}
		return bareHost(val)
	}
	return nil
}

// hostsFromColonSpec extracts host/IP-like tokens from a colon/comma-separated
// spec, honoring bracketed IPv6 literals. Numeric-only fields (ports) are
// dropped. It is deliberately over-inclusive: extracting every host/IP field so
// that any non-whitelisted destination in the spec trips the network rule.
func hostsFromColonSpec(spec string) []string {
	var hosts []string
	rest := spec
	// Pull out bracketed IPv6 literals first so their inner colons survive.
	for {
		open := strings.IndexByte(rest, '[')
		if open < 0 {
			break
		}
		closeRel := strings.IndexByte(rest[open:], ']')
		if closeRel < 0 {
			break
		}
		end := open + closeRel
		if inner := strings.TrimSpace(rest[open+1 : end]); inner != "" {
			hosts = append(hosts, inner)
		}
		rest = rest[:open] + " " + rest[end+1:]
	}
	for _, f := range strings.FieldsFunc(rest, func(r rune) bool {
		return r == ':' || r == ',' || r == ' '
	}) {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if domainLike(f) || net.ParseIP(f) != nil {
			hosts = append(hosts, f)
		}
	}
	return hosts
}

// hostsFromResolveSpec parses a curl --resolve value of the form
// "[+]HOST:PORT:ADDR[,ADDR]...". curl treats everything after the second colon
// as the address list, so ADDR may be an unbracketed IPv6 literal
// ("github.com:443:2001:db8::1") whose inner colons must not be split. Splitting
// on every colon (as hostsFromColonSpec does) drops the IPv6 addr entirely and
// lets the rewrite ride the whitelisted HOST past R-NET-001. Both HOST and every
// address (each optionally bracketed or "+"-prefixed) are returned so a redirect
// to a non-whitelisted endpoint trips the network rule.
func hostsFromResolveSpec(val string) []string {
	val = strings.TrimSpace(val)
	// The optional leading "+" marks a TTL-honoring entry; strip it off HOST.
	val = strings.TrimPrefix(val, "+")
	c1 := strings.IndexByte(val, ':')
	if c1 < 0 {
		// Malformed (no port/addr); fall back to the over-inclusive splitter.
		return hostsFromColonSpec(val)
	}
	var hosts []string
	if h := cleanHostField(val[:c1]); h != "" {
		hosts = append(hosts, h)
	}
	rest := val[c1+1:]
	c2 := strings.IndexByte(rest, ':')
	if c2 < 0 {
		return hosts // "HOST:PORT" with no address list.
	}
	for _, addr := range strings.Split(rest[c2+1:], ",") {
		if h := cleanHostField(addr); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// cleanHostField normalizes a single --resolve host/address field: it strips a
// leading "+", surrounding IPv6 brackets and surrounding whitespace, then keeps
// the value only if it is a domain or a parseable IP (so ports and empty fields
// are dropped). Unbracketed IPv6 literals survive because net.ParseIP accepts
// them.
func cleanHostField(f string) string {
	f = strings.TrimSpace(f)
	f = strings.TrimPrefix(f, "+")
	f = strings.TrimPrefix(f, "[")
	f = strings.TrimSuffix(f, "]")
	f = strings.TrimSpace(f)
	if f == "" {
		return ""
	}
	if domainLike(f) || net.ParseIP(f) != nil {
		return f
	}
	return ""
}

// curlOpaqueConfigOption reports whether a -K/--config option is present, in the
// "-K file", "--config=file" or bundled short-flag ("-sK file") form. Its file
// can define url/proxy/resolve and other egress controls the guard cannot read,
// so its mere presence fails closed. Detection is intentionally conservative: a
// short-flag token containing 'K' anywhere counts, erring toward fail-closed.
func curlOpaqueConfigOption(args []string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			if flag, _, _ := splitFlagValue(a); flag == "--config" {
				return "--config", true
			}
			continue
		}
		// Short-flag bundle: -K may appear anywhere (e.g. -sK).
		if strings.HasPrefix(a, "-") && len(a) > 1 &&
			strings.IndexByte(a[1:], 'K') >= 0 {
			return "-K", true
		}
	}
	return "", false
}

// curlDefaultConfigDisabled reports whether curl's implicit default config is
// suppressed, which is true only when -q or --disable is the very first option.
// curl checks solely the first parameter, so a later or bundled -q (e.g. "-sq")
// does not count; this stays conservative and matches curl's own behavior.
func curlDefaultConfigDisabled(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return args[0] == "-q" || args[0] == "--disable"
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
