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
	ruleMetaID      = "R-META-001"
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
	catMetadata    = "tool_metadata"
)

// Recommendation strings attached to each finding.
const (
	recDangerous   = "Avoid destructive commands; scope deletions to the workspace and never target system paths."
	recCredential  = "This path holds credentials/keys; remove the access or use a dedicated secrets mechanism." //nolint:gosec // G101 false positive: a recommendation string, not a credential.
	recNetwork     = "Target host is not in network.allowed_domains; add it to the whitelist or use a vetted download script."
	recShellBypass = "Wrap complex shell usage in an auditable workspace script and add it to allowed_commands."
	recCommandPol  = "Command is not in commands.allowed; add it to the allow list if it is expected, or keep it blocked."
	recHost        = "Background/PTY/privilege use outside a sandbox is high risk; run under an isolating executor (and set workspace_isolated only when the workspace tool truly is sandboxed)."
	recDependency  = "Dependency installs mutate the environment; vendor dependencies or run installs in a sandbox."
	recResource    = "Command may exhaust resources; lower the timeout/output or rely on sandbox runtime limits."
	recSecret      = "Command/env contains a secret-like value; pass secrets via a secret store, not inline." //nolint:gosec // G101 false positive: a recommendation string, not a credential.
	recEnv         = "Environment key is not in env.allowed_keys; add it to the whitelist or drop the override."
	recMetadata    = "The tool publishes destructive metadata; review the call or use a narrower, read-only tool."
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
	rulePipelineReview,
	ruleDangerousArgs,
	ruleForbiddenPath,
	ruleNetwork,
	ruleHostRisk,
	ruleDependency,
	ruleResource,
	ruleSecret,
	ruleEnvKeys,
	ruleToolMetadata,
}

// scan parses the command once, runs every rule, applies overrides and
// aggregates the verdict. A command shellsafe cannot parse yields a shell-
// bypass finding whose action is the policy's unparsable_action (fail
// closed); the secret and resource rules still run on the raw command so an
// unparsable command is not a blind spot. For the code backend, shell-language
// code blocks are parsed and merged into the pipeline so every argv-level rule
// applies to them; other languages get the code-specific checks.
func (p *Policy) scan(er ExecRequest, backend string) ([]Finding, Decision, RiskLevel) {
	var findings []Finding
	var pipe *shellsafe.Pipeline
	if backend == BackendCode {
		findings, pipe = p.scanCodeBlocks(er.CodeBlocks)
	} else if strings.TrimSpace(er.Command) != "" {
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

// scanCodeBlocks processes execute_code blocks. Shell-language blocks are
// parsed with shellsafe and merged into one pipeline, so the command policy,
// dangerous-argument, forbidden-path, network and dependency rules all apply
// to code that is really just shell; an unparsable shell block fails closed
// via unparsable_action exactly like an unparsable command. Non-shell blocks
// (python, js, ...) get the code-specific checks: shell-bridge calls that
// would sidestep every argv-level rule, and a URL whitelist pass over the
// source. The raw-text rules (secret, resource) run separately on the
// concatenated Command.
func (p *Policy) scanCodeBlocks(blocks []CodeBlock) ([]Finding, *shellsafe.Pipeline) {
	var findings []Finding
	var merged *shellsafe.Pipeline
	for _, b := range blocks {
		if strings.TrimSpace(b.Code) == "" {
			continue
		}
		if !isShellLanguage(b.Language) {
			findings = append(findings, codeBridgeFindings(b)...)
			findings = append(findings, p.codeNetworkFindings(b.Code)...)
			continue
		}
		parsed, err := shellsafe.Parse(b.Code)
		if err != nil {
			f := shellBypassFinding(err)
			f.action = p.UnparsableAction
			findings = append(findings, f)
			continue
		}
		if merged == nil {
			merged = &shellsafe.Pipeline{}
		}
		merged.Commands = append(merged.Commands, parsed.Commands...)
	}
	return findings, merged
}

// isShellLanguage reports whether a code block's language means the block is
// really a shell command. An empty language is treated as shell so an
// unlabeled block cannot dodge the argv-level rules.
func isShellLanguage(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "sh", "bash", "shell", "zsh":
		return true
	}
	return false
}

// codeBridgePatterns are substrings of non-shell code that bridge into shell
// execution (python os.system/subprocess, JS child_process, generic exec()),
// which would bypass every argv-level rule; their presence routes the block to
// human review.
var codeBridgePatterns = []string{
	"os.system", "os.popen", "subprocess.", "exec(", "execsync(", "child_process",
}

// codeBridgeFindings flags non-shell code that can launch shell commands.
func codeBridgeFindings(b CodeBlock) []Finding {
	low := strings.ToLower(b.Code)
	for _, pat := range codeBridgePatterns {
		if strings.Contains(low, pat) {
			return []Finding{{
				RuleID:         ruleShellID,
				Category:       catShellBypass,
				RiskLevel:      RiskMedium,
				Evidence:       b.Language + " code can launch shell commands (" + pat + ")",
				Recommendation: recShellBypass,
			}}
		}
	}
	return nil
}

// codeNetworkFindings runs the network whitelist over URLs embedded in
// non-shell code. Bare hosts are not extracted here (arbitrary source text
// would be far too noisy); full URLs are unambiguous and are exactly what
// download-and-execute code contains.
func (p *Policy) codeNetworkFindings(code string) []Finding {
	var out []Finding
	seen := make(map[string]bool)
	for _, m := range urlRe.FindAllString(code, -1) {
		u, err := url.Parse(m)
		if err != nil || u.Hostname() == "" {
			continue
		}
		host := u.Hostname()
		if seen[host] || p.domainAllowed(host) {
			continue
		}
		seen[host] = true
		out = append(out, Finding{
			RuleID:         ruleNetworkID,
			Category:       catNetwork,
			RiskLevel:      RiskHigh,
			Evidence:       "code -> " + host,
			Recommendation: recNetwork,
			action:         p.Network.OnNonWhitelisted,
		})
	}
	return out
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
// does not see: recursive rm (with force, or aimed at the root / a system
// directory even without force — "rm -r /etc" destroys the system just as
// surely as "rm -rf /etc") and recursive chmod.
func ruleDangerousArgs(c ruleCtx) []Finding {
	if c.pipe == nil {
		return nil
	}
	var out []Finding
	for _, argv := range c.pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		switch lowerBase(argv[0]) {
		case "rm":
			if f, ok := rmFinding(argv); ok {
				out = append(out, f)
			}
		case "chmod":
			if chmodRecursive(argv[1:]) {
				out = append(out, Finding{
					RuleID:         ruleDangerousID,
					Category:       catDangerous,
					RiskLevel:      RiskMedium,
					Evidence:       strings.Join(argv, " "),
					Recommendation: recDangerous,
				})
			}
		}
	}
	return out
}

// rmFinding evaluates one rm invocation: recursive with force is high risk;
// recursive aimed at the root or a system directory is critical whether or not
// force is present.
func rmFinding(argv []string) (Finding, bool) {
	recursive, force := recursiveForceFlags(argv[1:])
	if !recursive {
		return Finding{}, false
	}
	system := false
	for _, a := range argv[1:] {
		if !strings.HasPrefix(a, "-") && isRootOrSystem(a) {
			system = true
			break
		}
	}
	if !force && !system {
		return Finding{}, false
	}
	risk := RiskHigh
	if system {
		risk = RiskCritical
	}
	return Finding{
		RuleID:         ruleDangerousID,
		Category:       catDangerous,
		RiskLevel:      risk,
		Evidence:       strings.Join(argv, " "),
		Recommendation: recDangerous,
	}, true
}

// chmodRecursive reports whether a chmod invocation is recursive. Only the
// capital-R spellings count: a lowercase "-r" is a symbolic mode ("remove
// read"), not a flag.
func chmodRecursive(args []string) bool {
	for _, a := range args {
		if a == "--recursive" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") &&
			strings.ContainsRune(a[1:], 'R') {
			return true
		}
	}
	return false
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
		before := len(out)
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
		hosts := extractHosts(cmd, argv[1:])
		for _, host := range hosts {
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
		// Fallback: the command carries a non-option operand we could not turn
		// into a checkable host (a URL hidden in an unrecognized option, a
		// listener spec, ...). We cannot clear it against the whitelist, so
		// route it to review instead of silently allowing it. Pure-flag
		// invocations (curl --version, wget --help) have no operand and no
		// egress, so they are left to allow rather than flagged.
		if len(hosts) == 0 && len(out) == before && hasNonOptionOperand(argv[1:]) {
			out = append(out, Finding{
				RuleID:         ruleNetworkID,
				Category:       catNetwork,
				RiskLevel:      RiskMedium,
				Evidence:       argv[0] + " (no parseable network target to check against the whitelist)",
				Recommendation: recNetwork,
			})
		}
	}
	return out
}

// hasNonOptionOperand reports whether any argument is a bare (non-option)
// operand — a token that is not empty and does not start with "-". It gates the
// network no-target fallback so a pure-flag invocation (curl --version, wget
// --help, curl -V) is not flagged: with no operand there is nothing that could
// be a smuggled target. A value token that follows a value-taking option
// (wget -O out.txt) counts as an operand, so a download command that names a
// file but no URL is still routed to review — a degenerate, rare case.
func hasNonOptionOperand(args []string) bool {
	for _, a := range args {
		if a != "" && !strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}

// ruleHostRisk applies to backends that execute on the host: the host backend
// always, and the workspace backend unless the policy declares it sandboxed
// (workspace_isolated: true). The tool name alone proves nothing about
// isolation — workspace_exec backed by codeexecutor/local starts the command
// directly on the host, where background/PTY sessions and privilege
// escalation are exactly as risky as on the host shell.
func ruleHostRisk(c ruleCtx) []Finding {
	if !c.policy.hostRiskBackend(c.backend) {
		return nil
	}
	where := "host shell"
	if c.backend == BackendWorkspace {
		where = "workspace backend without declared sandbox isolation"
	}
	var out []Finding
	r := c.policy.Resources
	if c.er.Background && r.DenyBackgroundOnHost {
		out = append(out, hostFinding(RiskHigh, "background process on "+where))
	}
	if c.er.PTY && r.DenyPTYOnHost {
		out = append(out, hostFinding(RiskHigh, "PTY/TTY session on "+where))
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
			out = append(out, resourceArgvFindings(argv, r)...)
		}
	}
	out = append(out, resourceTextFindings(c.er.Command)...)
	return out
}

// resourceArgvFindings applies the per-command resource checks to one parsed
// pipeline segment.
func resourceArgvFindings(argv []string, r ResourcePolicy) []Finding {
	if len(argv) == 0 {
		return nil
	}
	switch lowerBase(argv[0]) {
	case "sleep":
		if len(argv) > 1 {
			if secs, ok := parseSleep(argv[1]); ok && r.MaxSleepSec > 0 && secs > r.MaxSleepSec {
				return []Finding{resourceFinding(RiskMedium, "sleep "+argv[1])}
			}
		}
	case "yes":
		return []Finding{resourceFinding(RiskHigh, "yes produces unbounded output")}
	case "head":
		if n, ok := headByteCount(argv[1:]); ok && r.MaxOutputBytes > 0 && n > r.MaxOutputBytes {
			return []Finding{resourceFinding(RiskMedium, fmt.Sprintf(
				"head -c %d exceeds max_output_bytes %d", n, r.MaxOutputBytes))}
		}
	case "xargs":
		return workerFindings(argv[1:], "-P", "--max-procs", "xargs", "parallel workers")
	case "parallel":
		return workerFindings(argv[1:], "-j", "--jobs", "parallel", "jobs")
	}
	return nil
}

// workerFindings flags an explicit high or unlimited worker count on a
// parallel-execution command.
func workerFindings(args []string, short, long, cmd, noun string) []Finding {
	n, ok := flagIntValue(args, short, long)
	if !ok || (n > 0 && n <= maxParallelWorkers) {
		return nil
	}
	return []Finding{resourceFinding(RiskMedium,
		cmd+" requests "+workerCount(n)+" "+noun)}
}

// resourceTextFindings applies the raw-text resource heuristics (infinite
// loops, interpreter string multiplication) to the full command/code text.
func resourceTextFindings(command string) []Finding {
	var out []Finding
	low := strings.ToLower(command)
	if strings.Contains(low, "while true") ||
		strings.Contains(strings.ReplaceAll(low, " ", ""), "for(;;)") {
		out = append(out, resourceFinding(RiskHigh, "infinite loop pattern"))
	}
	if printRepeatRe.MatchString(low) {
		out = append(out, resourceFinding(RiskMedium, "large string-multiplication output pattern"))
	}
	return out
}

// maxParallelWorkers is the built-in review threshold for explicit xargs -P /
// parallel -j worker counts; 0 means "unlimited" to both tools and is always
// flagged.
const maxParallelWorkers = 8

// printRepeatRe catches interpreter one-liners that materialize a huge string
// by repetition (print("x" * 10000000)), a cheap way to blow the output cap.
var printRepeatRe = regexp.MustCompile(`print\s*\([^)]*\*\s*[0-9]{7,}`)

// workerCount renders an xargs/parallel worker count for evidence text.
func workerCount(n int) string {
	if n == 0 {
		return "unlimited"
	}
	return strconv.Itoa(n)
}

// headByteCount returns the byte count requested by a "head -c N" / "--bytes=N"
// invocation, honoring the common K/M/G binary suffixes (optional trailing B).
func headByteCount(args []string) (int, bool) {
	for i, a := range args {
		switch {
		case a == "-c" || a == "--bytes":
			if i+1 < len(args) {
				return parseByteCount(args[i+1])
			}
		case strings.HasPrefix(a, "-c") && len(a) > 2:
			return parseByteCount(a[2:])
		case strings.HasPrefix(a, "--bytes="):
			return parseByteCount(a[len("--bytes="):])
		}
	}
	return 0, false
}

// parseByteCount parses a size with an optional K/M/G suffix (and optional
// trailing B), e.g. "512", "4K", "10MB".
func parseByteCount(s string) (int, bool) {
	s = strings.TrimSpace(strings.ToUpper(s))
	s = strings.TrimSuffix(s, "B")
	mult := 1
	switch {
	case strings.HasSuffix(s, "K"):
		mult, s = 1024, s[:len(s)-1]
	case strings.HasSuffix(s, "M"):
		mult, s = 1024*1024, s[:len(s)-1]
	case strings.HasSuffix(s, "G"):
		mult, s = 1024*1024*1024, s[:len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n * mult, true
}

// flagIntValue finds an integer option value in "-P 4", "-P4" or "--jobs=4"
// form.
func flagIntValue(args []string, short, long string) (int, bool) {
	for i, a := range args {
		switch {
		case a == short || a == long:
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					return n, true
				}
			}
		case strings.HasPrefix(a, short) && len(a) > len(short):
			if n, err := strconv.Atoi(a[len(short):]); err == nil {
				return n, true
			}
		case strings.HasPrefix(a, long+"="):
			if n, err := strconv.Atoi(a[len(long)+1:]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
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
	// The key participates in the match ("key=value") so name-based patterns
	// (password=, api_key=, ...) catch a secret-named env override whatever
	// its value looks like.
	for k, v := range c.er.Env {
		if matchAnyRegex(res, k+"="+v) {
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

// ruleToolMetadata routes tools whose published metadata marks them as
// destructive (tool.ToolMetadata.Destructive) to human review. The built-in
// exec tools do not publish the flag, so this only fires for tools that
// explicitly declare irreversible side effects.
func ruleToolMetadata(c ruleCtx) []Finding {
	if !c.er.ToolDestructive {
		return nil
	}
	return []Finding{{
		RuleID:         ruleMetaID,
		Category:       catMetadata,
		RiskLevel:      RiskMedium,
		Evidence:       "tool metadata marks this tool as destructive",
		Recommendation: recMetadata,
	}}
}

// rulePipelineReview is the opt-in commands.review_pipelines knob: any
// multi-segment pipeline or command chain is routed to human review, for
// operators who want a coarse "no unreviewed shell plumbing" posture on top of
// the per-command rules. Off by default so legitimate pipes stay allowed.
func rulePipelineReview(c ruleCtx) []Finding {
	if c.pipe == nil || !c.policy.Commands.ReviewPipelines || len(c.pipe.Commands) < 2 {
		return nil
	}
	return []Finding{{
		RuleID:         ruleCmdID,
		Category:       catCommandPol,
		RiskLevel:      RiskMedium,
		Evidence:       fmt.Sprintf("pipeline with %d commands (commands.review_pipelines)", len(c.pipe.Commands)),
		Recommendation: recShellBypass,
	}}
}

// recursiveForceFlags reports which of recursive and force deletion the flags
// request, covering separate, combined, and long-option spellings
// (-r -f, -rf, -fr, -Rf, --recursive --force).
func recursiveForceFlags(args []string) (recursive, force bool) {
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
	return recursive, force
}

var systemDirs = []string{
	"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64",
	"/boot", "/sys", "/proc", "/var", "/dev", "/root",
}

// windowsSystemDirs are matched case-insensitively after backslash-to-slash
// normalization.
var windowsSystemDirs = []string{
	"c:/windows", "c:/program files", "c:/program files (x86)", "c:/programdata",
}

func isRootOrSystem(p string) bool {
	// Normalize separators explicitly: the scanned command may target a
	// Windows path even when the guard runs on Linux, where filepath.ToSlash
	// is a no-op for backslashes. Dot segments are resolved lexically so
	// "/tmp/../etc" is recognized as the /etc it resolves to.
	clean := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if clean != "" {
		clean = path.Clean(clean)
	}
	clean = strings.TrimRight(clean, "/")
	if clean == "" || p == "/" {
		return true
	}
	for _, sys := range systemDirs {
		if clean == sys || strings.HasPrefix(clean, sys+"/") {
			return true
		}
	}
	low := strings.ToLower(clean)
	// A bare drive root ("C:", after the trailing slash was trimmed) is as
	// destructive a target as "/".
	if len(low) == 2 && low[1] == ':' && low[0] >= 'a' && low[0] <= 'z' {
		return true
	}
	for _, sys := range windowsSystemDirs {
		if low == sys || strings.HasPrefix(low, sys+"/") {
			return true
		}
	}
	return false
}

func pathCandidates(c ruleCtx) []string {
	var out []string
	cwd := strings.TrimSpace(c.er.Cwd)
	if cwd != "" {
		out = append(out, cwd)
	}
	if c.pipe != nil {
		for _, argv := range c.pipe.Commands {
			for _, a := range argv {
				out = append(out, a)
				// A file:// URI is a filesystem access in disguise: add its
				// decoded path so "curl file:///etc/shadow" matches the
				// forbidden-path globs, not just the raw URI string.
				if p := fileURIPath(a); p != "" {
					out = append(out, p)
				}
				// A relative path is what the OS resolves against cwd: add the
				// resolved form so "cat ../../etc/shadow" run from /var/www
				// matches an absolute forbidden pattern too.
				if j := resolveAgainstCwd(cwd, a); j != "" {
					out = append(out, j)
				}
			}
		}
	}
	return out
}

// resolveAgainstCwd joins a relative, path-like argument onto the request's
// working directory (path.Join also resolves the dot segments), or returns ""
// when the argument is not a relative path. Only arguments containing a
// separator are resolved: a bare word ("cat", "id_rsa") is already matched in
// its raw form by the **-globs, and gluing every word onto cwd would only add
// noise.
func resolveAgainstCwd(cwd, arg string) string {
	if cwd == "" {
		return ""
	}
	a := strings.ReplaceAll(strings.TrimSpace(arg), "\\", "/")
	if a == "" || strings.HasPrefix(a, "-") || !strings.Contains(a, "/") {
		return ""
	}
	// Absolute paths, home-rooted paths, drive-letter paths and URLs are not
	// cwd-relative.
	if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") ||
		strings.Contains(a, "://") || (len(a) >= 2 && a[1] == ':') {
		return ""
	}
	return path.Join(strings.ReplaceAll(cwd, "\\", "/"), a)
}

// fileURIPath extracts the filesystem path from a file: URI embedded in an
// argument, or "" when the argument carries none. All RFC 8089 spellings curl
// accepts resolve to the same path: file:///etc/shadow, file:/etc/shadow and
// file://localhost/etc/shadow.
func fileURIPath(a string) string {
	i := strings.Index(strings.ToLower(a), "file:")
	if i < 0 {
		return ""
	}
	u, err := url.Parse(a[i:])
	if err != nil {
		return ""
	}
	if u.Path != "" {
		return u.Path
	}
	// file:etc/passwd — the pathless opaque form still names a file.
	return u.Opaque
}

// argsHavePrefix reports whether args, after the leading option flags, begins
// with the prefix sequence (e.g. "install"). A leading option's arity is
// unknown to the guard ("go -C /tmp install" carries a value in the next
// token, "pip -q install" does not), so both readings of every leading option
// are explored: standing alone, or consuming the following token as its value
// (skipped when the value is already inline via "="). Over-matching is
// accepted — a value that happens to spell the subcommand flags a benign call
// for review — because under-matching would let "go -C /tmp install" bypass a
// configured denial.
func argsHavePrefix(args, prefix []string) bool {
	if len(prefix) == 0 {
		return false
	}
	// BFS over the positions where the subcommand could start; each leading
	// option branches into its boolean and value-consuming readings.
	starts := []int{0}
	seen := map[int]bool{0: true}
	for n := 0; n < len(starts); n++ {
		i := starts[n]
		if i >= len(args) {
			continue
		}
		if !strings.HasPrefix(args[i], "-") {
			if prefixMatchesAt(args, prefix, i) {
				return true
			}
			continue
		}
		if !seen[i+1] {
			seen[i+1] = true
			starts = append(starts, i+1)
		}
		if !strings.Contains(args[i], "=") && !seen[i+2] {
			seen[i+2] = true
			starts = append(starts, i+2)
		}
	}
	return false
}

// prefixMatchesAt reports whether args[i:] begins with the prefix sequence,
// case-insensitively.
func prefixMatchesAt(args, prefix []string, i int) bool {
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
