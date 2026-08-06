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
	"os"
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
	ruleSessionID   = "R-SESSION-001"
)

// knownRuleIDs indexes every built-in rule id. Policy compilation rejects a
// rule_overrides entry keyed by an unknown id: there is no custom-rule
// extension point, so an unmatched key is always a typo (R-NTE-001) that would
// otherwise load fine, have no effect, and leave the live policy weaker than
// the file suggests.
var knownRuleIDs = map[string]bool{
	ruleDangerousID: true, ruleCredID: true, ruleNetworkID: true,
	ruleShellID: true, ruleCmdID: true, ruleHostID: true,
	ruleDepID: true, ruleResourceID: true, ruleSecretID: true,
	ruleEnvID: true, ruleMetaID: true, ruleSessionID: true,
}

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
	catSessionIn   = "session_input"
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
	recSessionIn   = "Characters written into a live session bypass the command rules; enable session_input.scan for non-interactive deployments, or rely on sandbox isolation."
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
	er      execRequest
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
func (p *Policy) scan(er execRequest, backend string) ([]Finding, Decision, RiskLevel) {
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
func (p *Policy) scanCodeBlocks(blocks []codeBlock) ([]Finding, *shellsafe.Pipeline) {
	var findings []Finding
	var merged *shellsafe.Pipeline
	for _, b := range blocks {
		if strings.TrimSpace(b.Code) == "" {
			continue
		}
		if !isShellLanguage(b.Language) {
			findings = append(findings, p.genericCodeFindings(b)...)
			continue
		}
		if isNonPOSIXShellLanguage(b.Language) {
			// A shell the guard has no parser for: the argv-level rules cannot
			// be applied, so the block fails closed exactly like an unparsable
			// POSIX command. The generic code checks still run so a policy that
			// relaxes unparsable_action keeps some coverage.
			f := nonPOSIXShellFinding(b.Language)
			f.action = p.UnparsableAction
			findings = append(findings, f)
			findings = append(findings, p.genericCodeFindings(b)...)
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
// really a shell command, whether or not the guard can parse that shell. An
// empty language is treated as shell so an unlabeled block cannot dodge the
// argv-level rules. The non-POSIX members (see isNonPOSIXShellLanguage) belong
// here so they are never demoted to the generic code checks; scanCodeBlocks
// fails them closed instead.
func isShellLanguage(lang string) bool {
	if isNonPOSIXShellLanguage(lang) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "sh", "bash", "shell", "zsh":
		return true
	}
	return false
}

// nonPOSIXShellLanguages are the shell languages an executor may accept
// (codeexecutor/jupyter recognizes pwsh/powershell/ps1, and codeexec's
// WithLanguages can expose them) whose grammar shellsafe does not implement.
// Parsing them as POSIX shell would incorrectly tokenize the command, so they are
// treated as shell — never as inert "code" — and failed closed.
var nonPOSIXShellLanguages = map[string]bool{
	"pwsh": true, "powershell": true, "ps1": true, "posh": true,
	"cmd": true, "bat": true, "batch": true,
}

// isNonPOSIXShellLanguage reports whether lang names a shell the guard cannot
// parse into argv.
func isNonPOSIXShellLanguage(lang string) bool {
	return nonPOSIXShellLanguages[strings.ToLower(strings.TrimSpace(lang))]
}

// nonPOSIXShellFinding fails a shell block closed because no parser exists for
// its grammar. The caller sets the policy's unparsable_action on it.
func nonPOSIXShellFinding(lang string) Finding {
	return Finding{
		RuleID:    ruleShellID,
		Category:  catShellBypass,
		RiskLevel: RiskHigh,
		Evidence: strings.TrimSpace(lang) +
			" code block: no parser for this shell, so the command-level rules cannot be applied",
		Recommendation: recShellBypass,
	}
}

// codeBridgePatterns are substrings of non-shell code that bridge into shell
// execution (python os.system/subprocess, JS child_process, generic exec()),
// which would bypass every argv-level rule; their presence routes the block to
// human review.
var codeBridgePatterns = []string{
	"os.system", "os.popen", "subprocess.", "exec(", "execsync(", "child_process",
}

// codeBridgeFindings flags non-shell code that can launch shell commands.
func codeBridgeFindings(b codeBlock) []Finding {
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

// genericCodeFindings runs the checks that apply to a code block the guard
// cannot turn into argv: the shell-bridge check, the forbidden-path pass over
// the block's string literals and the URL whitelist pass. It covers both
// non-shell languages and the shells with no parser (pwsh, cmd), which reach
// the rules as opaque text either way.
func (p *Policy) genericCodeFindings(b codeBlock) []Finding {
	out := codeBridgeFindings(b)
	out = append(out, p.codePathFindings(b.Code)...)
	if p.codeNetworkActive() {
		out = append(out, p.codeNetworkFindings(b.Code)...)
	}
	return out
}

// codeStringLiteralRe matches a single-, double- or backtick-quoted string
// literal. Paths in source code are always quoted, so the literals are the
// units to test against forbidden_paths — matching raw source text would flag
// identifiers and comments that merely contain a pattern.
var codeStringLiteralRe = regexp.MustCompile(
	"\"(?:[^\"\\\\\\n]|\\\\.)*\"|'(?:[^'\\\\\\n]|\\\\.)*'|`[^`]*`")

// codePathFindings runs the forbidden-path rule over the string literals of a
// code block the guard cannot parse into argv. Without it the credential rule
// covers only shell commands, and open("/root/.ssh/id_rsa") in a python block
// reaches the executor unflagged — the same read the shell rules deny for
// "cat /root/.ssh/id_rsa".
//
// This is the code-side counterpart of ruleForbiddenPath (which walks argv) and
// of codeNetworkFindings (which walks URLs). Unlike the network pass it is not
// gated on any opt-in configuration: forbidden_paths is populated by
// DefaultPolicy, so credential reads are caught out of the box on every backend.
// String literals are also concatenation-blind — "/root/.ssh/" + name defeats
// it, as does any dynamically built path — so this narrows the blind spot
// rather than closing it; sandbox isolation remains the real boundary.
func (p *Policy) codePathFindings(code string) []Finding {
	var out []Finding
	seen := make(map[string]bool)
	for _, lit := range codeStringLiteralRe.FindAllString(code, -1) {
		if len(lit) < 2 {
			continue
		}
		val := unescapeCodeLiteral(lit[1 : len(lit)-1])
		if !pathLikeLiteral(val) || seen[val] {
			continue
		}
		seen[val] = true
		// A file: URI is a filesystem access in disguise here too, matching the
		// argv-side handling in pathCandidates.
		for _, cand := range []string{val, fileURIPath(val)} {
			pat, ok := p.forbiddenMatch(cand)
			if !ok {
				continue
			}
			out = append(out, Finding{
				RuleID:         ruleCredID,
				Category:       catCredential,
				RiskLevel:      RiskCritical,
				Evidence:       "code -> " + cand + " (matches " + pat + ")",
				Recommendation: recCredential,
			})
			break
		}
	}
	return out
}

// pathLikeLiteral reports whether a string literal is shaped like a filesystem
// path: it carries a separator, or begins with "~" or "." (so a bare ".env" or
// "./key" still counts).
//
// The argv side (pathCandidates) tests every token, bare words included,
// because a shell operand overwhelmingly is a path or a command. Source code
// inverts that base rate — most literals are messages, keys and identifiers —
// so a bare word is not treated as a path here: a forbidden_paths entry such as
// "**/credentials" would otherwise deny a plain print("credentials"), spending
// the false-positive budget on a string that never opens a file. The cost is
// that a key opened by bare name in the working directory (open("id_rsa")) is
// not matched; a path with any directory component still is.
func pathLikeLiteral(v string) bool {
	if v == "" {
		return false
	}
	return strings.ContainsAny(v, `/\`) || v[0] == '~' || v[0] == '.'
}

// unescapeCodeLiteral resolves the escape sequences that matter to a path
// inside a quoted literal: an escaped quote or backslash. A Windows literal
// ("C:\\Users\\me\\.ssh") therefore normalizes to single separators before
// forbiddenMatch folds them to slashes. Other escapes (\n, \t) are left as
// written; they never appear in a path the rule would match.
func unescapeCodeLiteral(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\', '"', '\'':
				sb.WriteByte(s[i+1])
				i++
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// codeNetworkActive reports whether the URL whitelist pass applies to
// non-shell code. It mirrors the shell side, where ruleNetwork only fires for
// commands listed in network.download_commands: the built-in DefaultPolicy
// configures neither download commands nor allowed domains and is documented
// as having no network whitelist, so NewGuard() must not deny a benign
// print("https://example.com"). Any explicit network configuration — a domain
// whitelist or download commands — turns the code URL check on (an empty
// whitelist then denies every URL, matching the shell behavior).
func (p *Policy) codeNetworkActive() bool {
	return len(p.Network.AllowedDomains) > 0 || len(p.Network.DownloadCommands) > 0
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
// surely as "rm -rf /etc"), recursive chmod, and their cmd.exe equivalents
// (del / erase / rd / rmdir with /s /q, or aimed at a Windows system path).
// hostexec runs commands through cmd.exe on Windows, so the native spellings
// have to be covered too, not just the Unix tools.
func ruleDangerousArgs(c ruleCtx) []Finding {
	if c.pipe == nil {
		return nil
	}
	var out []Finding
	for _, argv := range c.pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		name := lowerBase(argv[0])
		if windowsDeleteCommands[name] {
			if f, ok := windowsDeleteFinding(name, argv, c.er.Cwd); ok {
				out = append(out, f)
			}
			continue
		}
		switch name {
		case "rm":
			if f, ok := rmFinding(argv, c.er.Cwd); ok {
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

// windowsDeleteCommands are the cmd.exe built-ins that delete files or whole
// trees. "rmdir" also exists on POSIX, where it only removes empty directories
// and takes no "/switches"; windowsDeleteFinding therefore requires the
// Windows recursive switch before it treats rmdir/rd as destructive.
var windowsDeleteCommands = map[string]bool{
	"del": true, "erase": true, "rd": true, "rmdir": true,
}

// windowsDeleteFinding evaluates one cmd.exe delete invocation. It mirrors
// rmFinding: a recursive+quiet/force delete is high risk, and anything aimed
// at a drive root or system directory is critical when recursive and high
// otherwise. A plain "del build\out.txt" produces no finding.
func windowsDeleteFinding(name string, argv []string, cwd string) (Finding, bool) {
	recursive, force := windowsDeleteSwitches(argv[1:])
	if (name == "rmdir" || name == "rd") && !recursive {
		// The POSIX rmdir: empty directories only, nothing to flag.
		return Finding{}, false
	}
	system := false
	for _, a := range argv[1:] {
		if isWindowsSwitch(a) {
			continue
		}
		if isRootOrSystem(a) {
			system = true
			break
		}
		if j := resolveAgainstCwd(cwd, a); j != "" && isRootOrSystem(j) {
			system = true
			break
		}
	}
	if !system && !(recursive && force) {
		return Finding{}, false
	}
	risk := RiskHigh
	if system && recursive {
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

// windowsDeleteSwitches reports which of recursive (/s) and unattended
// deletion (/q quiet, /f force) the cmd.exe switches request. Switches are
// case-insensitive and may carry a value ("/A:R").
func windowsDeleteSwitches(args []string) (recursive, force bool) {
	for _, a := range args {
		if !isWindowsSwitch(a) {
			continue
		}
		switch strings.ToLower(a[1:2]) {
		case "s":
			recursive = true
		case "q", "f":
			force = true
		}
	}
	return recursive, force
}

// isWindowsSwitch reports whether a token is a cmd.exe switch ("/S", "/A:R")
// rather than an operand. A POSIX absolute path ("/etc/passwd") is not one:
// switches are a single letter, optionally followed by ":value".
func isWindowsSwitch(a string) bool {
	if len(a) < 2 || a[0] != '/' {
		return false
	}
	c := a[1]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return false
	}
	return len(a) == 2 || a[2] == ':'
}

// rmFinding evaluates one rm invocation: recursive with force is high risk;
// recursive aimed at the root or a system directory is critical whether or not
// force is present. Relative targets are also resolved against the request's
// cwd, so "rm -rf .." run from /etc/apt is recognized as the /etc it deletes.
func rmFinding(argv []string, cwd string) (Finding, bool) {
	recursive, force := recursiveForceFlags(argv[1:])
	if !recursive {
		return Finding{}, false
	}
	system := false
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if isRootOrSystem(a) {
			system = true
			break
		}
		if j := resolveAgainstCwd(cwd, a); j != "" && isRootOrSystem(j) {
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
	sawDownload := false
	for _, argv := range c.pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		cmd := lowerBase(argv[0])
		if !dl[cmd] {
			continue
		}
		sawDownload = true
		out = append(out, downloadCommandFindings(c, cmd, argv)...)
	}
	// A proxy environment override redirects a download command's real egress
	// just like a command-line proxy option, so its destination must clear the
	// same whitelist.
	if sawDownload {
		out = append(out, proxyEnvFindings(c)...)
	}
	return out
}

// downloadCommandFindings scans one download-command invocation: first the
// egress controls that fail closed on their own merits, then every host the
// command names against the whitelist, then the no-target fallback.
func downloadCommandFindings(c ruleCtx, cmd string, argv []string) []Finding {
	args := argv[1:]
	out := egressControlFindings(c, cmd, argv)
	hosts := extractHosts(cmd, args)
	for _, host := range hosts {
		if c.policy.domainAllowed(host) {
			continue
		}
		out = append(out, networkFinding(c, argv[0]+" -> "+host))
	}
	// Fallback: the command carries a non-option operand we could not turn
	// into a checkable host (a URL hidden in an unrecognized option, a
	// listener spec, ...). We cannot clear it against the whitelist, so route
	// it to review instead of silently allowing it. Pure-flag invocations
	// (curl --version, wget --help) have no operand and no egress, so they are
	// left to allow rather than flagged.
	if len(hosts) == 0 && len(out) == 0 && hasNonOptionOperand(args) {
		out = append(out, Finding{
			RuleID:         ruleNetworkID,
			Category:       catNetwork,
			RiskLevel:      RiskMedium,
			Evidence:       argv[0] + " (no parseable network target to check against the whitelist)",
			Recommendation: recNetwork,
		})
	}
	return out
}

// egressControlFindings flags the client options that decide where a download
// command really connects, independently of the host named on the command
// line: an opaque config the guard cannot read, a destination override, or
// redirect following whose target is the server's choice at runtime.
func egressControlFindings(c ruleCtx, cmd string, argv []string) []Finding {
	args := argv[1:]
	var out []Finding
	if cmd == "curl" {
		out = append(out, curlEgressFindings(c, argv)...)
	} else if opt, ok := genericOpaqueOption(cmd, args); ok {
		// Non-curl equivalents of the opaque curl config: wget
		// -e/--execute/--config, ssh/scp -o/-F, ssh -D (dynamic SOCKS proxy),
		// scp/sftp -S/-D can redirect the real egress (proxy, ProxyCommand,
		// tunnel, transport program) in ways the guard cannot read, so they
		// fail closed too.
		out = append(out, networkFinding(c,
			argv[0]+" "+opt+" (opaque option may redirect egress via proxy/config)"))
	}
	// wget follows redirects by default, so under the opt-in egress-boundary
	// posture every wget without an explicit --max-redirect=0 fails closed:
	// the redirect target is the server's choice, not the whitelisted URL's.
	if cmd == "wget" && c.policy.Network.RequireRedirectFree && !wgetRedirectsDisabled(args) {
		out = append(out, networkFinding(c,
			argv[0]+" (follows redirects by default; pass --max-redirect=0 under require_redirect_free)"))
	}
	return out
}

// curlEgressFindings covers curl's own destination-control surface: the opaque
// -K/--config file and the Unix-socket overrides fail closed regardless of the
// whitelist, the implicit ~/.curlrc is fail-closed under the opt-in
// curl_require_disabled_config, and -L/--location is fail-closed under the
// opt-in require_redirect_free (a redirect-following client can be bounced
// from a whitelisted URL to any host, so the whitelist proves nothing about
// the final destination).
func curlEgressFindings(c ruleCtx, argv []string) []Finding {
	args := argv[1:]
	var out []Finding
	if opt, ok := curlOpaqueOption(args); ok {
		out = append(out, networkFinding(c, argv[0]+" "+opt+" "+curlOpaqueReason(opt)))
	} else if c.policy.Network.CurlRequireDisabledConfig && !curlDefaultConfigDisabled(args) {
		out = append(out, networkFinding(c,
			argv[0]+" (implicit curl config may define url/proxy/resolve; pass -q/--disable first)"))
	}
	if c.policy.Network.RequireRedirectFree && curlFollowsRedirects(args) {
		out = append(out, networkFinding(c, argv[0]+
			" -L/--location (redirect target cannot be statically verified; drop -L under require_redirect_free)"))
	}
	return out
}

// curlOpaqueReason explains why an opaque curl option fails closed: a socket
// override reroutes the connection away from the URL's host entirely, while a
// config file can define url/proxy/resolve egress the guard cannot see.
func curlOpaqueReason(opt string) string {
	if opt == "--unix-socket" || opt == "--abstract-unix-socket" {
		return "(connection rerouted to a local socket the whitelist cannot vet)"
	}
	return "(opaque config may define url/proxy/resolve)"
}

// networkFinding builds a whitelist-level R-NET-001 finding whose action
// follows network.on_non_whitelisted.
func networkFinding(c ruleCtx, evidence string) Finding {
	return Finding{
		RuleID:         ruleNetworkID,
		Category:       catNetwork,
		RiskLevel:      RiskHigh,
		Evidence:       evidence,
		Recommendation: recNetwork,
		action:         c.policy.Network.OnNonWhitelisted,
	}
}

// proxyEnvVars are the environment keys (matched case-insensitively) that
// curl, wget and friends honor for proxy routing. Their values redirect the
// real egress, so the hosts they name must clear the same whitelist as a
// command-line proxy option — otherwise HTTPS_PROXY=http://evil.io:8080
// tunnels a whitelisted "curl https://github.com/a" through an unapproved
// relay. The env-key rule (R-ENV-001) cannot be relied on for this: it is
// opt-in, and an empty env.allowed_keys is documented as disabling it.
var proxyEnvVars = map[string]bool{
	"http_proxy": true, "https_proxy": true, "all_proxy": true,
	"ftp_proxy": true, "ftps_proxy": true,
}

// proxyEnvFindings whitelist-checks the proxy destinations the command will
// actually see. It runs only when the pipeline contains a download command. A
// proxy value with no parseable host fails closed like the no-target operand
// fallback, but at the on_non_whitelisted action: the value demonstrably
// redirects egress somewhere the guard cannot check.
func proxyEnvFindings(c ruleCtx) []Finding {
	var out []Finding
	for _, e := range effectiveProxyEnv(c) {
		hosts := hostsFromCurlValue("--proxy", e.value)
		if len(hosts) == 0 {
			out = append(out, Finding{
				RuleID:         ruleNetworkID,
				Category:       catNetwork,
				RiskLevel:      RiskHigh,
				Evidence:       e.label() + " (no parseable proxy host to check against the whitelist)",
				Recommendation: recNetwork,
				action:         c.policy.Network.OnNonWhitelisted,
			})
			continue
		}
		for _, h := range hosts {
			if c.policy.domainAllowed(h) {
				continue
			}
			out = append(out, Finding{
				RuleID:         ruleNetworkID,
				Category:       catNetwork,
				RiskLevel:      RiskHigh,
				Evidence:       e.label() + " -> " + h,
				Recommendation: recNetwork,
				action:         c.policy.Network.OnNonWhitelisted,
			})
		}
	}
	return out
}

// Sources of a proxy environment value, in increasing precedence. They mirror
// how hostexec builds a command's environment: the guard process environment
// the child inherits, then the executor's base env (hostexec.WithBaseEnv,
// mirrored into the guard via WithExecutorEnv), then the per-request overrides
// the model supplied.
const (
	proxySrcInherited = "inherited"
	proxySrcBase      = "executor base env"
	proxySrcRequest   = ""
)

// proxyEnvEntry is one proxy variable that will be in effect for the command.
type proxyEnvEntry struct {
	key    string
	value  string
	source string
}

// label renders the evidence prefix, naming the source for anything the model
// did not pass itself so an operator can tell a request override from an
// inherited one.
func (e proxyEnvEntry) label() string {
	if e.source == proxySrcRequest {
		return "env " + e.key
	}
	return "env " + e.key + " (" + e.source + ")"
}

// osEnviron is os.Environ, indirected for tests.
var osEnviron = os.Environ

// effectiveProxyEnv resolves the proxy variables the executed command will see,
// in sorted key order. Checking only the request overrides would leave the
// boundary open: hostexec passes the guard process environment through to every
// command (mergedEnv starts from os.Environ, and a command with no overrides
// inherits it outright), so an ambient HTTPS_PROXY reroutes an otherwise
// whitelisted "curl https://github.com/a" through an unapproved relay.
//
// The inherited environment is consulted only for backends that really run in
// the guard's process environment: the code backend executes inside its own
// runtime (container / e2b / jupyter kernel), and an explicitly isolated
// workspace backend likewise does not inherit it.
func effectiveProxyEnv(c ruleCtx) []proxyEnvEntry {
	merged := map[string]proxyEnvEntry{}
	add := func(k, v, src string) {
		if !proxyEnvVars[strings.ToLower(k)] {
			return
		}
		if v = strings.TrimSpace(v); v == "" {
			// An empty value disables the proxy for that variable, and it also
			// overrides a lower-precedence one.
			delete(merged, k)
			return
		}
		merged[k] = proxyEnvEntry{key: k, value: v, source: src}
	}
	if inheritsProcessEnv(c) {
		for _, kv := range osEnviron() {
			if i := strings.IndexByte(kv, '='); i > 0 {
				add(kv[:i], kv[i+1:], proxySrcInherited)
			}
		}
	}
	for k, v := range c.er.BaseEnv {
		add(k, v, proxySrcBase)
	}
	for k, v := range c.er.Env {
		add(k, v, proxySrcRequest)
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]proxyEnvEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, merged[k])
	}
	return out
}

// inheritsProcessEnv reports whether the backend runs the command in the guard
// process's own environment.
func inheritsProcessEnv(c ruleCtx) bool {
	switch c.backend {
	case BackendCode:
		return false
	case BackendWorkspace:
		return !c.policy.WorkspaceIsolated
	default:
		return true
	}
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

// sessionInputUnscannedFinding records that a session-input tool ran without
// being scanned (session_input.scan is off). The call is still allowed — the
// guard deliberately does not judge input it did not parse — but the audit
// trail now shows the command rules were bypassed, instead of the call being
// invisible.
func sessionInputUnscannedFinding(toolName string) Finding {
	return Finding{
		RuleID:         ruleSessionID,
		Category:       catSessionIn,
		RiskLevel:      RiskLow,
		Evidence:       toolName + " wrote to a live session without command-level scanning (session_input.scan is off)",
		Recommendation: recSessionIn,
		action:         ActionAllow,
	}
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

// windowsSystemEnvDirs are the environment-variable spellings of the same
// locations. cmd.exe expands them at run time, so "rmdir /s /q %SystemRoot%"
// destroys the system just as surely as the literal path.
var windowsSystemEnvDirs = []string{
	"%systemroot%", "%windir%", "%systemdrive%",
	"%programfiles%", "%programfiles(x86)%", "%programdata%",
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
	if isWindowsSystemPath(clean) {
		return true
	}
	return false
}

// isWindowsSystemPath reports whether an already slash-normalized path names a
// Windows drive root or system directory, including the environment-variable
// spellings cmd.exe expands ("%SystemRoot%", "%WINDIR%\System32").
func isWindowsSystemPath(clean string) bool {
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
	for _, sys := range windowsSystemEnvDirs {
		// A plain prefix test is enough: the "%" delimiters make the variable
		// name unambiguous, and it also covers the spelling left behind when
		// the POSIX word splitter eats the backslash ("%WINDIR%System32").
		if strings.HasPrefix(low, sys) {
			return true
		}
	}
	// A command written for cmd.exe uses backslashes, which the POSIX word
	// splitter consumes as escapes: "del /s C:\Windows\System32" reaches the
	// rules as "C:WindowsSystem32". Compare the separator-free form of a
	// drive-qualified path against the separator-free system directories so the
	// mangled spelling is still recognized. Gated on the drive prefix so no
	// POSIX path can match.
	if len(low) >= 2 && low[1] == ':' && low[0] >= 'a' && low[0] <= 'z' {
		for _, sys := range windowsSystemDirs {
			if strings.HasPrefix(low, strings.ReplaceAll(sys, "/", "")) {
				return true
			}
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
				// Each argv token contributes itself plus any path-bearing
				// portion embedded in an option value ("--upload-file=/x",
				// "-T/x", "@/x"); every candidate then gets the file-URI and
				// cwd-resolved variants.
				for _, cand := range append([]string{a}, optionValueCandidates(a)...) {
					out = append(out, cand)
					// A file:// URI is a filesystem access in disguise: add its
					// decoded path so "curl file:///etc/shadow" matches the
					// forbidden-path globs, not just the raw URI string.
					if p := fileURIPath(cand); p != "" {
						out = append(out, p)
					}
					// A relative path is what the OS resolves against cwd: add
					// the resolved form so "cat ../../etc/shadow" run from
					// /var/www matches an absolute forbidden pattern too.
					if j := resolveAgainstCwd(cwd, cand); j != "" {
						out = append(out, j)
					}
				}
			}
		}
	}
	return out
}

// optionValueCandidates extracts path-bearing portions embedded inside one
// argv token, so a forbidden path cannot hide in an option value that never
// stands alone as a token: "--upload-file=/etc/shadow" (inline long-option
// value), "-T/etc/shadow" (inline short-option value), and curl's
// read-from-file markers ("--data-binary=@/etc/shadow", "-F name=@/etc/shadow",
// "-d @/etc/shadow", "--data-urlencode name@/etc/shadow",
// "-F story=</etc/shadow"). Extraction is command-agnostic and deliberately
// over-inclusive: an extra candidate matters only if it matches a forbidden
// pattern, and over-matching fails toward protection.
func optionValueCandidates(a string) []string {
	var out []string
	add := func(v string) {
		if v == "" {
			return
		}
		out = append(out, v)
		out = append(out, fileMarkerPaths(v)...)
	}
	if i := strings.IndexByte(a, '='); i >= 0 {
		// "--flag=value" and form-field "name=@file" / "name=<file" tokens.
		add(a[i+1:])
		return out
	}
	// Inline short-option value ("-T/etc/shadow", "-d@/etc/shadow",
	// "-T~/.ssh/id_rsa"): the path-like tail starts at the first /, ~, @ or <.
	if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
		if idx := strings.IndexAny(a, "/~@<"); idx > 0 {
			add(a[idx:])
		}
		return out
	}
	// A bare (non-option) token can still carry a read-from-file marker: the
	// value of "-d"/"--data-urlencode"/"-F" arrives as its own token, and the
	// path may sit behind a field name ("name@/etc/shadow") or a "<" that the
	// shell never saw because the value was quoted.
	return append(out, fileMarkerPaths(a)...)
}

// fileMarkerPaths returns the path portions hidden behind curl's
// read-from-file markers in v. "<" is the form-field file read
// ("-F story=</etc/shadow" uploads the file's contents) and "@" marks a file
// either as a prefix ("@/etc/shadow") or as a field-name separator
// ("--data-urlencode name@/etc/shadow"), neither of which leaves the bare path
// as a token of its own.
func fileMarkerPaths(v string) []string {
	var out []string
	if rest := strings.TrimPrefix(v, "<"); rest != v && rest != "" {
		out = append(out, rest)
		v = rest
	}
	if i := strings.IndexByte(v, '@'); i >= 0 && i+1 < len(v) {
		out = append(out, v[i+1:])
	}
	return out
}

// resolveAgainstCwd joins a relative, path-like argument onto the request's
// working directory (path.Join also resolves the dot segments), or returns ""
// when the argument is not a relative path. Bare words are resolved too:
// "cat shadow" run from /etc names /etc/shadow just as surely as a form with
// a separator, and an absolute forbidden pattern only matches the resolved
// spelling (the **-globs still catch the raw word, so the join adds coverage,
// not noise).
func resolveAgainstCwd(cwd, arg string) string {
	if cwd == "" {
		return ""
	}
	a := strings.ReplaceAll(strings.TrimSpace(arg), "\\", "/")
	if a == "" || strings.HasPrefix(a, "-") {
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
