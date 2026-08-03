//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

//

package safety

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Scanner performs pre-execution safety analysis on tool commands.
type Scanner struct {
	policy     *Policy
	compiledRe []compiledRule
	auditor    *Auditor
}

// compiledRule holds a pre-compiled regex for each rule pattern.
type compiledRule struct {
	Rule     Rule
	Patterns []*regexp.Regexp
}

// NewScanner creates a new Scanner with the given policy.
// A nil policy defaults to DefaultPolicy() for safety.
func NewScanner(policy *Policy) *Scanner {
	if policy == nil {
		policy = DefaultPolicy()
	}
	s := &Scanner{
		policy:  policy,
		auditor: NewAuditor(),
	}
	s.compileRules()
	return s
}

// compileRules pre-compiles all regex patterns for performance.
// Invalid patterns are skipped with a warning to stderr so that
// operators are aware of misconfigured rules.
func (s *Scanner) compileRules() {
	s.compiledRe = make([]compiledRule, 0, len(s.policy.Rules))
	for _, rule := range s.policy.Rules {
		cr := compiledRule{Rule: rule}
		for _, pattern := range rule.Patterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				fmt.Fprintf(os.Stderr,
					"tool/safety: invalid regex pattern in rule %q: %v (skipping)\n",
					rule.ID, err,
				)
				continue
			}
			cr.Patterns = append(cr.Patterns, re)
		}
		// Only add the rule if at least one pattern compiled.
		if len(cr.Patterns) > 0 {
			s.compiledRe = append(s.compiledRe, cr)
		}
	}
}

// ScanRequest contains all information needed to scan a tool command.
type ScanRequest struct {
	ToolName string   // Name of the tool being called.
	Command  string   // The full command string (including args).
	Args     []string // Arguments to the command.
	WorkDir  string   // Working directory for the command.
	EnvVars  []string // Environment variables.
	Backend  string   // "workspaceexec", "hostexec", "codeexec".
	// Language is the language of the code/script being executed, when known
	// (e.g. codeexec's "language" argument: "shell", "python", "javascript").
	// Non-shell languages cannot be structurally parsed by shellsafe, so they
	// fail closed to ask unless a regex rule already produced a worse verdict.
	Language string
}

// Scan performs a safety scan on the given command and returns a report.
func (s *Scanner) Scan(ctx context.Context, req ScanRequest) ScanReport {
	start := time.Now()

	// Combine command and args for analysis.
	fullCommand := req.Command
	if len(req.Args) > 0 {
		fullCommand += " " + strings.Join(req.Args, " ")
	}

	report := ScanReport{
		Decision:    DecisionAllow,
		RiskLevel:   RiskLow,
		ToolName:    req.ToolName,
		Command:     fullCommand,
		Backend:     req.Backend,
		Intercepted: false,
	}

	// 1.5 保守解析：shell 命令无法安全解析 → fail-closed deny；
	// 非 shell 代码（python/javascript 等）无法用 shellsafe 结构化建模 →
	// fail-closed ask，除非已有更严格的规则命中。
	// （issue 要求：对无法安全解析的命令返回 deny 或 ask，不得默认 allow）
	if fullCommand != "" {
		if isForeignCode(req.Language) {
			if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskHigh
			}
			if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
				report.Decision = DecisionAsk
				report.RuleID = "foreign_code_unscanned"
				report.Evidence = req.Language
				report.Category = "code_analysis"
				report.Recommendation = fmt.Sprintf(
					"%s code cannot be structurally parsed by shellsafe; review required", req.Language)
			}
		} else if _, perr := parseShellCommands(fullCommand); perr != nil {
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskCritical
			}
			if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
				report.Decision = DecisionDeny
			}
			report.RuleID = "shell_parse_failed"
			report.Evidence = perr.Error()
			report.Category = "shell_parse"
			report.Recommendation = fmt.Sprintf(
				"Command could not be safely parsed by shellsafe: %v", perr)
		}
	}

	// 1. Check DeniedCommands (blacklist — highest priority).
	cmdName := extractCommandName(fullCommand)
	for _, denied := range s.policy.DeniedCommands {
		if cmdName == denied {
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskCritical
			}
			if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
				report.Decision = DecisionDeny
			}
			report.RuleID = "denied_command"
			report.Evidence = cmdName
			report.Category = "dangerous_commands"
			report.Recommendation = fmt.Sprintf(
				"Command %q is explicitly denied by policy", cmdName,
			)
		}
	}

	// 2. Check against all regex rules. Worst risk level and most
	// restrictive action win. Rule metadata (RuleID, Evidence,
	// Category, Recommendation) is updated when the risk or action
	// is escalated, so the report always points at the rule that
	// drove the most restrictive decision.
	for _, cr := range s.compiledRe {
		for _, re := range cr.Patterns {
			if matches := re.FindStringSubmatch(fullCommand); matches != nil {
				matchedRisk := riskOrder(cr.Rule.RiskLevel)
				currentRisk := riskOrder(report.RiskLevel)
				matchedAction := actionOrder(cr.Rule.Action)
				currentAction := actionOrder(report.Decision)

				riskUpgraded := matchedRisk > currentRisk
				actionUpgraded := matchedAction > currentAction

				// Update metadata when:
				// 1. This match has worse risk, OR
				// 2. This match has equal risk and >= action, OR
				// 3. This match caused an action escalation (even
				//    with lower risk).  Case 3 is important: if a
				//    lower-risk rule pushes the action from Ask to
				//    Deny, the metadata must reference that rule to
				//    explain the Deny.
				if riskUpgraded ||
					(matchedRisk == currentRisk && matchedAction >= currentAction) ||
					actionUpgraded {
					report.RuleID = cr.Rule.ID
					report.Evidence = matches[0]
					report.Category = cr.Rule.Category
					report.Recommendation = fmt.Sprintf(
						"Rule %s (%s): %s",
						cr.Rule.ID, cr.Rule.Category, cr.Rule.Description,
					)
				}

				// Upgrade risk level if this rule is worse.
				if riskUpgraded {
					report.RiskLevel = cr.Rule.RiskLevel
				}
				// Upgrade action if this rule is more restrictive.
				if actionUpgraded {
					report.Decision = cr.Rule.Action
				}
			}
		}
	}

	// 4. Check for dangerous paths in command and WorkDir.
	// ~ and ~user prefixes are also checked in normalized form so that path
	// traversal through a home directory (~root/../etc/shadow) resolves to the
	// real target instead of evading the literal forbidden-path match.
	normalizedCommand := normalizeTildePaths(fullCommand)
	normalizedWorkDir := normalizeTildePaths(req.WorkDir)
	for _, forbidden := range s.policy.ForbiddenPaths {
		if strings.Contains(fullCommand, forbidden) ||
			strings.Contains(normalizedCommand, forbidden) ||
			strings.Contains(req.WorkDir, forbidden) ||
			strings.Contains(normalizedWorkDir, forbidden) {
			// Only update metadata on a strict upgrade: an earlier rule at
			// the same severity (e.g. secrets_001 for ~/.ssh) stays the
			// reported rule because it is more specific than the generic
			// forbidden-path finding.
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
				report.RuleID = "forbidden_path"
				report.Evidence = forbidden
				report.Category = "dangerous_commands"
				report.Recommendation = fmt.Sprintf(
					"Command references forbidden path: %s", forbidden,
				)
			}
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskCritical
			}
			if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
				report.Decision = DecisionDeny
			}
		}
	}

	// 5. Check AllowedCommands whitelist — if configured, commands
	// not in the allowlist are denied, but only when no regex rule
	// has already made a more nuanced decision (ask/deny from regex
	// takes priority over the allowlist gate).
	if len(s.policy.AllowedCommands) > 0 && report.Decision == DecisionAllow && cmdName != "" {
		// shellsafe 结构策略：对 pipeline 的每个 segment 的命令做 allowlist 裁决，
		// 捕获 `wget | sh` 这类"首段在白名单但后续段绕过"的多段绕过，以及
		// 多行脚本中任一行的命令不在白名单的情况。
		notAllowed := !isAllowed(cmdName, s.policy.AllowedCommands)
		if segCmds, serr := parseShellCommands(fullCommand); serr == nil {
			for _, argv := range segCmds {
				if len(argv) > 0 && !isAllowed(argv[0], s.policy.AllowedCommands) {
					notAllowed = true
					break
				}
			}
		}
		if notAllowed {
			if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskHigh
			}
			if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
				report.Decision = DecisionAsk
			}
			report.RuleID = "not_allowed_command"
			report.Evidence = cmdName
			report.Category = "dangerous_commands"
			report.Recommendation = fmt.Sprintf(
				"Command %q (or one of its pipeline segments) is not in the allowed commands list", cmdName,
			)
		}
	}

	// 6a. Hidden command execution / unmodellable network configuration.
	// These fail closed even when no host allowlist is configured, because the
	// effective destination (or code run) cannot be statically modelled:
	//   - ssh -o ProxyCommand/LocalCommand, scp -S, rsync -e/--rsh name a
	//     program that executes behind an allowlisted destination;
	//   - curl -K/--config reads a config file whose proxy/URL settings are
	//     invisible to the scanner; curl --connect-to/--resolve rewrite the
	//     real connection target; wget -e <non-proxy wgetrc> injects arbitrary
	//     wgetrc configuration.
	if cmdName == "ssh" || cmdName == "sftp" || cmdName == "scp" || cmdName == "rsync" {
		if opt, found := netCommandOption(cmdName, fullCommand); found {
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskCritical
			}
			if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
				report.Decision = DecisionDeny
			}
			report.RuleID = "net_command_option"
			report.Evidence = opt
			report.Category = "network_egress"
			report.Recommendation = fmt.Sprintf(
				"%s carries a hidden command/program option (%s) that cannot be safely modelled", cmdName, opt,
			)
		}
	}
	if cmdName == "curl" {
		if config, routing, found := curlNetOverride(fullCommand); found {
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskCritical
			}
			if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
				report.Decision = DecisionDeny
			}
			report.RuleID = "net_routing_override"
			if config {
				report.RuleID = "net_config_file"
			}
			report.Evidence = routing
			report.Category = "network_egress"
			report.Recommendation = "curl routing/config cannot be safely modelled; denying"
		}
	}
	if cmdName == "wget" {
		if val := wgetConfigOverride(fullCommand); val != "" {
			if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskHigh
			}
			if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
				report.Decision = DecisionDeny
			}
			report.RuleID = "net_routing_override"
			report.Evidence = val
			report.Category = "network_egress"
			report.Recommendation = "wget -e injects wgetrc configuration that cannot be safely modelled; denying"
		}
	}

	// 6b. Check allowlisted hosts for network egress commands. Every extracted
	// target (destination, -J jump peers, proxy URLs) must clear the allowlist.
	if isNetCommand(cmdName) && len(s.policy.AllowlistedHosts) > 0 {
		for _, target := range extractNetworkTargets(cmdName, fullCommand) {
			if target != "" && !isAllowed(target, s.policy.AllowlistedHosts) {
				if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
					report.RiskLevel = RiskHigh
				}
				if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
					report.Decision = DecisionDeny
				}
				report.RuleID = "non_allowlisted_host"
				report.Evidence = target
				report.Category = "network_egress"
				report.Recommendation = fmt.Sprintf(
					"Target host %q is not in the allowlisted hosts", target,
				)
			}
		}
	}

	// 7. Check environment variable allowlist.
	if len(s.policy.EnvAllowlist) > 0 && len(req.EnvVars) > 0 {
		for _, ev := range req.EnvVars {
			name := extractEnvVarName(ev)
			if name != "" && !isAllowed(name, s.policy.EnvAllowlist) {
				if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
					report.RiskLevel = RiskHigh
				}
				if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
					report.Decision = DecisionDeny
				}
				report.RuleID = "env_not_allowlisted"
				report.Evidence = name
				report.Category = "dangerous_commands"
				report.Recommendation = fmt.Sprintf(
					"Environment variable %q is not in the allowlist", name,
				)
			}
		}
	}

	// 8. Check excessive command length (potential abuse).
	// Only upgrade — never downgrade a more severe finding.
	if len(fullCommand) > 10000 {
		if riskOrder(RiskMedium) > riskOrder(report.RiskLevel) {
			report.RiskLevel = RiskMedium
		}
		if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
			report.Decision = DecisionAsk
		}
		// Only set metadata if this is the worst finding.
		if riskOrder(report.RiskLevel) <= riskOrder(RiskMedium) {
			report.RuleID = "excessive_length"
			report.Category = "resource_abuse"
			report.Recommendation = "Command exceeds 10000 characters; review required."
		}
	}

	// 8.5 Resource abuse: enforce MaxTimeoutSec on sleep durations.
	if s.policy.MaxTimeoutSec > 0 {
		if secs, ok := parseSleepSeconds(fullCommand); ok && secs > s.policy.MaxTimeoutSec {
			if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskHigh
			}
			if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
				report.Decision = DecisionAsk
			}
			if metadataUpgrades(report, RiskHigh, DecisionAsk) {
				report.RuleID = "sleep_timeout_exceeded"
				report.Evidence = fmt.Sprintf("sleep %ds", secs)
				report.Category = "resource_abuse"
				report.Recommendation = fmt.Sprintf(
					"sleep %ds exceeds max timeout %ds", secs, s.policy.MaxTimeoutSec)
			}
		}
	}

	// 8.6 Resource abuse: output flood / unbounded stream.
	if s.policy.MaxOutputBytes > 0 {
		if flood, isFlood := matchesOutputFlood(fullCommand); isFlood {
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskCritical
			}
			if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
				report.Decision = DecisionDeny
			}
			if metadataUpgrades(report, RiskCritical, DecisionDeny) {
				report.RuleID = "output_flood"
				report.Evidence = flood
				report.Category = "resource_abuse"
				report.Recommendation = "Command streams unbounded output (/dev/zero, yes, etc.)"
			}
		}
	}

	// 8.7 Resource abuse: concurrency flags (parallel fan-out).
	if concurrency, isConcurrent := matchesConcurrency(fullCommand); isConcurrent {
		if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
			report.RiskLevel = RiskHigh
		}
		if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
			report.Decision = DecisionAsk
		}
		if metadataUpgrades(report, RiskHigh, DecisionAsk) {
			report.RuleID = "concurrent_execution"
			report.Evidence = concurrency
			report.Category = "resource_abuse"
			report.Recommendation = "Command requests concurrent/parallel execution; review required"
		}
	}

	// 8.8 HostExec safety boundary: interactive / long-lived PTY sessions
	// run directly on the host shell and carry higher risk.
	if req.Backend == "hostexec" {
		if session, isLongSession := matchesHostExecRisk(fullCommand); isLongSession {
			if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskHigh
			}
			if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
				report.Decision = DecisionAsk
			}
			if metadataUpgrades(report, RiskHigh, DecisionAsk) {
				report.RuleID = "hostexec_long_session"
				report.Evidence = session
				report.Category = "host_execution"
				report.Recommendation = "hostexec interactive/long-lived session (top, tail -f, editor, etc.); review required"
			}
		}
	}

	// Desensitize the command and evidence stored in the report so secrets
	// never leak into reports, logs, or audit events.
	fullCommand = redactSecrets(fullCommand)
	report.Command = fullCommand
	report.Evidence = redactSecrets(report.Evidence)

	// Record decision.
	if report.Decision != DecisionAllow {
		report.Intercepted = true
	}

	// Audit.
	s.auditor.Record(AuditEvent{
		ToolName:     req.ToolName,
		Decision:     report.Decision,
		RiskLevel:    report.RiskLevel,
		RuleID:       report.RuleID,
		DurationMs:   time.Since(start).Milliseconds(),
		Desensitized: true,
		Intercepted:  report.Intercepted,
		CommandHash:  hashCommand(fullCommand),
	})

	return report
}

// CheckToolPermission implements tool.PermissionPolicy so that
// the Scanner can be plugged into the agent's permission framework.
func (s *Scanner) CheckToolPermission(
	ctx context.Context, req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	command := extractCommandFromArgs(req.Arguments)

	scanReq := ScanRequest{
		ToolName: req.ToolName,
		Command:  command,
		Backend:  "permission_check",
		Language: extractLanguageFromArgs(req.Arguments),
	}
	report := s.Scan(ctx, scanReq)

	// The core permission framework only normalizes allow/deny/ask
	// (NormalizePermissionDecision). Map needs_human_review to ask so a
	// policy rule configured with that action doesn't turn into an
	// "unknown permission action" error downstream.
	action := tool.PermissionAction(report.Decision)
	if report.Decision == DecisionNeedsReview {
		action = tool.PermissionActionAsk
	}
	decision := tool.PermissionDecision{
		Action: action,
		Reason: report.Recommendation,
	}
	return decision, nil
}

// parseShellCommands conservatively parses fullCommand with shellsafe,
// handling multi-line scripts line by line. It returns the argv of every
// pipeline segment across all lines; an error means some line could not be
// safely parsed (command substitution, backticks, redirection, unterminated
// quotes, etc.) and callers should fail closed.
func parseShellCommands(fullCommand string) ([][]string, error) {
	var allCommands [][]string
	for _, line := range strings.Split(fullCommand, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		pipe, err := shellsafe.Parse(line)
		if err != nil {
			return nil, err
		}
		allCommands = append(allCommands, pipe.Commands...)
	}
	return allCommands, nil
}

// extractCommandName returns the first token (command name) from
// a full command string, stripping any leading path.
func extractCommandName(fullCommand string) string {
	if fullCommand == "" {
		return ""
	}
	// Strip leading whitespace.
	cmd := strings.TrimSpace(fullCommand)
	// Take everything before the first space or line break.
	if idx := strings.IndexAny(cmd, " \t\n\r"); idx >= 0 {
		cmd = cmd[:idx]
	}
	// Strip path prefix (e.g. "/usr/bin/rm" → "rm").
	if idx := strings.LastIndex(cmd, "/"); idx >= 0 {
		cmd = cmd[idx+1:]
	}
	return cmd
}

// extractHostTarget returns the first network destination of a command, for
// backward-compatible single-target callers. Multi-target analysis (jump
// hosts, proxies) is done by extractNetworkTargets.
func extractHostTarget(fullCommand string) string {
	targets := extractNetworkTargets(extractCommandName(fullCommand), fullCommand)
	if len(targets) > 0 {
		return targets[0]
	}
	return ""
}

// netCommands lists commands whose operands may name a network destination.
// Each family has its own operand grammar; git counts only for its remote
// subcommands (clone/fetch/pull/push/ls-remote).
var netCommands = map[string]struct{}{
	"curl": {}, "wget": {}, "nc": {}, "ncat": {}, "telnet": {}, "socat": {},
	"ssh": {}, "scp": {}, "sftp": {}, "rsync": {}, "git": {},
}

func isNetCommand(name string) bool {
	_, ok := netCommands[name]
	return ok
}

// gitNetSubcommands are the git subcommands that touch the network; local
// ones (status, diff, log, ...) are left alone.
var gitNetSubcommands = map[string]struct{}{
	"clone": {}, "fetch": {}, "pull": {}, "push": {}, "ls-remote": {},
}

// Flag sets: which options consume the next argv token, and which option
// values are local files (never a host). Inline --flag=value / -fvalue forms
// never consume a following token.
var (
	curlFileFlags = map[string]struct{}{
		"-o": {}, "--output": {}, "--output-dir": {}, "-T": {}, "--upload-file": {},
		"-K": {}, "--config": {}, "-b": {}, "--cookie": {}, "-c": {}, "--cookie-jar": {},
		"-d": {}, "--data": {}, "--data-raw": {}, "--data-binary": {}, "-F": {},
		"--form": {}, "-E": {}, "--cert": {}, "--key": {}, "--cacert": {},
		"--capath": {}, "-H": {}, "--header": {}, "-u": {}, "--user": {},
	}
	wgetFileFlags = map[string]struct{}{
		"-O": {}, "--output-document": {}, "-o": {}, "--output-file": {},
		"-a": {}, "--append-output": {}, "-P": {}, "--directory-prefix": {},
		"-i": {}, "--input-file": {}, "--config": {},
		"--load-cookies": {}, "--save-cookies": {},
	}
	sshValueFlags = map[string]struct{}{
		"-i": {}, "-o": {}, "-p": {}, "-P": {}, "-Q": {}, "-R": {}, "-S": {},
		"-W": {}, "-w": {}, "-l": {}, "-F": {}, "-J": {},
	}
	scpValueFlags = map[string]struct{}{
		"-c": {}, "-D": {}, "-F": {}, "-i": {}, "-l": {}, "-o": {}, "-P": {},
		"-S": {}, "-X": {}, "-J": {},
	}
	rsyncValueFlags = map[string]struct{}{
		"-e": {}, "--rsh": {}, "-p": {}, "--port": {}, "-i": {}, "--identity": {},
		"--log-file": {}, "--password-file": {}, "--rsync-path": {},
		"--include-from": {}, "--exclude-from": {},
	}
	// ssh_config options whose value is an arbitrary command executed behind
	// an allowlisted destination → cannot be modelled, deny.
	sshCommandOptions = map[string]struct{}{
		"proxycommand": {}, "localcommand": {}, "remotecommand": {},
		"permitlocalcommand": {}, "match": {},
	}
)

func isWgetFileFlag(flag string) bool {
	_, ok := wgetFileFlags[flag]
	return ok
}

// splitFlagValue splits one option token into flag/value/attached.
func splitFlagValue(a string) (flag, value string, attached bool) {
	if strings.HasPrefix(a, "--") {
		if i := strings.Index(a, "="); i >= 0 {
			return a[:i], a[i+1:], true
		}
		return a, "", false
	}
	if len(a) > 2 && strings.HasPrefix(a, "-") {
		return a[:2], a[2:], true
	}
	return a, "", false
}

// bareHost reduces a URL / user@host[:port][/path] / host:path token to its
// bare hostname. Tokens without a recognizable host form yield "".
func bareHost(tok string) string {
	tok = strings.TrimSpace(tok)
	tok = strings.Trim(tok, `"'`)
	if strings.HasPrefix(tok, "-") {
		return "" // an option token is never a host
	}
	for _, scheme := range []string{"https://", "http://", "ftp://", "sftp://",
		"ssh://", "git://", "rsync://", "ws://", "wss://"} {
		tok = strings.TrimPrefix(tok, scheme)
	}
	if i := strings.Index(tok, "@"); i >= 0 {
		tok = tok[i+1:]
	}
	if i := strings.IndexAny(tok, "/:?"); i >= 0 {
		tok = tok[:i]
	}
	if tok == "" {
		return ""
	}
	return strings.ToLower(tok)
}

// dedupHosts returns hosts in first-seen order without duplicates.
func dedupHosts(hosts []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// jumpHosts splits a ssh -J value (host[,host...], each [user@]host[:port])
// into its egress peers.
func jumpHosts(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if h := bareHost(part); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// pendingValue records what the token following a flag means when that flag
// took its value in separate form.
type pendingValue int

const (
	pendingNone pendingValue = iota
	pendingSkip              // value is a local file/identity, not a host
	pendingHost              // value is a host or URL
)

// extractNetworkTargets returns every hostname that a network command may
// reach: positional URLs, option values that name hosts (proxies, -J peers),
// and per-family operand grammars (ssh/sftp first-operand, scp/rsync
// host:path, git remote subcommands). Local-file option values are skipped so
// `wget -O release.tar.gz https://x` reads x, not release.tar.gz.
func extractNetworkTargets(cmdName, fullCommand string) []string {
	parts := strings.Fields(fullCommand)
	if len(parts) < 2 {
		return nil
	}
	switch cmdName {
	case "curl":
		return curlTargets(parts)
	case "wget":
		return wgetTargets(parts)
	case "ssh", "sftp":
		return sshStyleTargets(parts)
	case "scp", "rsync":
		return scpStyleTargets(parts)
	case "git":
		return gitTargets(parts)
	default:
		return genericTargets(parts)
	}
}

// genericTargets handles nc/ncat/telnet/socat: the first non-flag operand is
// the destination host.
func genericTargets(parts []string) []string {
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "-") {
			continue
		}
		if h := bareHost(p); h != "" {
			return []string{h}
		}
	}
	return nil
}

// curlTargets: positional operands are URLs; values of host-bearing options
// (--proxy/-x, --socks5) are egress peers; values of file-valued flags (-o,
// -K, -H, -d, ...) are never hosts.
func curlTargets(parts []string) []string {
	var hosts []string
	pending := pendingNone
	for i := 1; i < len(parts); i++ {
		a := parts[i]
		if pending != pendingNone {
			switch pending {
			case pendingSkip:
			case pendingHost:
				if h := bareHost(a); h != "" {
					hosts = append(hosts, h)
				}
			}
			pending = pendingNone
			continue
		}
		if strings.HasPrefix(a, "-") {
			flag, val, attached := splitFlagValue(a)
			switch {
			case isCurlFileFlag(flag) && !attached:
				pending = pendingSkip
			case isCurlHostFlag(flag):
				if attached {
					if h := bareHost(val); h != "" {
						hosts = append(hosts, h)
					}
				} else {
					pending = pendingHost
				}
			}
			continue
		}
		if h := bareHost(a); h != "" {
			hosts = append(hosts, h)
		}
	}
	return dedupHosts(hosts)
}

func isCurlFileFlag(flag string) bool {
	_, ok := curlFileFlags[flag]
	return ok
}

// curlHostFlags are curl options whose value names a host/URL (proxy or
// socks routing), a real egress peer in addition to the positional URL.
var curlHostFlags = map[string]struct{}{
	"-x": {}, "--proxy": {}, "--socks5": {}, "--socks5-hostname": {},
	"--socks4": {}, "--socks4a": {}, "--socks": {},
}

func isCurlHostFlag(flag string) bool {
	_, ok := curlHostFlags[flag]
	return ok
}

// curlNetOverride reports whether curl carries a config file (-K/--config)
// or a connection-routing flag (--connect-to/--resolve) whose effective
// destination cannot be statically modelled. Returns (configFile, evidence).
func curlNetOverride(fullCommand string) (bool, string, bool) {
	parts := strings.Fields(fullCommand)
	for i := 1; i < len(parts); i++ {
		a := parts[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		flag, val, attached := splitFlagValue(a)
		if flag == "-K" || flag == "--config" || flag == "--connect-to" || flag == "--resolve" {
			if !attached && i+1 < len(parts) {
				val = parts[i+1]
			}
			return flag == "-K" || flag == "--config", val, true
		}
	}
	return false, "", false
}

// wgetTargets: positional operands are URLs; -e <key>=<value> proxy
// assignments contribute the proxy as an egress peer; values of wget's
// file-valued flags are never hosts.
func wgetTargets(parts []string) []string {
	var hosts []string
	pending := pendingNone
	for i := 1; i < len(parts); i++ {
		a := parts[i]
		if pending != pendingNone {
			if pending == pendingHost {
				if h := bareHost(a); h != "" {
					hosts = append(hosts, h)
				}
			}
			pending = pendingNone
			continue
		}
		if strings.HasPrefix(a, "-") {
			flag, val, attached := splitFlagValue(a)
			switch {
			case isWgetFileFlag(flag) && !attached:
				pending = pendingSkip
			case flag == "-e" || flag == "--execute":
				if !attached {
					if i+1 < len(parts) {
						if _, v, ok := proxyAssignment(parts[i+1]); ok {
							if h := bareHost(v); h != "" {
								hosts = append(hosts, h)
							}
						}
						i++
					}
					continue
				}
				if _, v, ok := proxyAssignment(val); ok {
					if h := bareHost(v); h != "" {
						hosts = append(hosts, h)
					}
				}
			}
			continue
		}
		if h := bareHost(a); h != "" {
			hosts = append(hosts, h)
		}
	}
	return dedupHosts(hosts)
}

// proxyAssignment reports whether a wget -e value is a proxy assignment
// (<key>_proxy=URL). Any other -e value rewrites arbitrary wgetrc config and
// is handled as an unmodellable override (wgetConfigOverride).
func proxyAssignment(v string) (key, value string, ok bool) {
	k, val, found := strings.Cut(v, "=")
	if !found || val == "" {
		return "", "", false
	}
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(k)), "_proxy") {
		return "", "", false
	}
	return k, val, true
}

// wgetConfigOverride returns the first non-proxy -e wgetrc assignment, which
// injects arbitrary wget configuration the scanner cannot model.
func wgetConfigOverride(fullCommand string) string {
	parts := strings.Fields(fullCommand)
	for i := 1; i < len(parts); i++ {
		a := parts[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		flag, val, attached := splitFlagValue(a)
		if flag != "-e" && flag != "--execute" {
			continue
		}
		if !attached {
			if i+1 >= len(parts) {
				continue
			}
			val = parts[i+1]
			i++
		}
		if _, _, isProxy := proxyAssignment(val); !isProxy {
			return val
		}
	}
	return ""
}

// sshStyleTargets handles ssh/sftp: option values are skipped, -J jump hosts
// are egress peers, and the first non-flag operand is the destination.
func sshStyleTargets(parts []string) []string {
	var hosts []string
	skipNext, isJump := false, false
	for i := 1; i < len(parts); i++ {
		a := parts[i]
		if skipNext {
			if isJump {
				hosts = append(hosts, jumpHosts(a)...)
			}
			skipNext, isJump = false, false
			continue
		}
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			flag, val, attached := splitFlagValue(a)
			switch {
			case flag == "-J":
				if attached {
					hosts = append(hosts, jumpHosts(val)...)
				} else {
					skipNext, isJump = true, true
				}
			default:
				if _, ok := sshValueFlags[flag]; ok && !attached {
					skipNext = true
				}
			}
			continue
		}
		if h := bareHost(a); h != "" {
			return dedupHosts(append(hosts, h))
		}
	}
	return dedupHosts(hosts)
}

// scpStyleTargets handles scp/rsync: a remote operand carries a colon
// ([user@]host:path) or a scheme URL; local files have neither and are not
// hosts. Option values are skipped and -J jump peers are egress hosts.
func scpStyleTargets(parts []string) []string {
	var hosts []string
	skipNext, isJump := false, false
	for i := 1; i < len(parts); i++ {
		a := parts[i]
		if skipNext {
			if isJump {
				hosts = append(hosts, jumpHosts(a)...)
			}
			skipNext, isJump = false, false
			continue
		}
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			flag, val, attached := splitFlagValue(a)
			switch {
			case flag == "-J":
				if attached {
					hosts = append(hosts, jumpHosts(val)...)
				} else {
					skipNext, isJump = true, true
				}
			default:
				if _, ok := scpValueFlags[flag]; ok && !attached {
					skipNext = true
				}
				if _, ok := rsyncValueFlags[flag]; ok && !attached {
					skipNext = true
				}
			}
			continue
		}
		if strings.Contains(a, "://") || strings.Contains(a, ":") {
			if h := bareHost(a); h != "" {
				hosts = append(hosts, h)
			}
		}
	}
	return dedupHosts(hosts)
}

// gitTargets: only network subcommands (clone/fetch/pull/push/ls-remote) are
// egress. Their operands are URLs (https://..., git@host:path) or remote
// names; remote names are configured locally and are not network targets.
func gitTargets(parts []string) []string {
	if len(parts) < 2 {
		return nil
	}
	if _, ok := gitNetSubcommands[parts[1]]; !ok {
		return nil
	}
	var hosts []string
	for _, a := range parts[2:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		a = strings.Trim(a, `"'`)
		if !strings.Contains(a, "://") && !strings.Contains(a, "@") {
			continue // bare remote name (origin) — locally configured
		}
		if h := bareHost(a); h != "" {
			hosts = append(hosts, h)
		}
	}
	return dedupHosts(hosts)
}

// netCommandOption finds a hidden program/command option in ssh/scp/sftp/
// rsync argv: ssh -o ProxyCommand=..., scp -S prog, rsync -e 'prog args',
// rsync --rsh=prog. Returns the option value when found.
func netCommandOption(cmd, fullCommand string) (string, bool) {
	parts := strings.Fields(fullCommand)
	for i := 1; i < len(parts); i++ {
		a := parts[i]
		if !strings.HasPrefix(a, "-") {
			// ssh/sftp: the first non-flag token is the destination; later
			// operands are remote commands, which are out of scope here.
			if cmd == "ssh" || cmd == "sftp" {
				return "", false
			}
			continue
		}
		flag, val, attached := splitFlagValue(a)
		switch {
		case flag == "-o":
			if !attached {
				if i+1 >= len(parts) {
					continue
				}
				val = parts[i+1]
				i++
			}
			if k, _, found := strings.Cut(val, "="); found && isSSHCommandOption(k) {
				return val, true
			}
		case cmd == "scp" || cmd == "sftp":
			if flag == "-S" {
				if attached {
					return val, true
				}
				if i+1 < len(parts) {
					return parts[i+1], true
				}
			}
		case cmd == "rsync":
			if flag == "-e" || flag == "--rsh" {
				if attached {
					return val, true
				}
				if i+1 < len(parts) {
					return parts[i+1], true
				}
			}
		}
	}
	return "", false
}

func isSSHCommandOption(key string) bool {
	_, ok := sshCommandOptions[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// isForeignCode reports whether the language of the code being executed is a
// non-shell language that shellsafe cannot structurally parse.
func isForeignCode(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "shell", "bash", "sh", "zsh", "powershell", "pwsh":
		return false
	default:
		return true
	}
}

// normalizeTildePaths rewrites ~ and ~user path tokens in a command so that
// path traversal through a home directory resolves to its real target for
// forbidden-path matching: ~root/../etc/shadow must be seen as /etc/shadow,
// while traversal that stays inside the home (~/notes/../todo.txt) keeps its
// ~ prefix. Non-path tokens are left untouched.
func normalizeTildePaths(cmd string) string {
	fields := strings.Fields(cmd)
	changed := false
	for i, f := range fields {
		if nf, ok := normalizeTildeToken(f); ok {
			fields[i] = nf
			changed = true
		}
	}
	if !changed {
		return cmd
	}
	return strings.Join(fields, " ")
}

func normalizeTildeToken(f string) (string, bool) {
	trimmed := strings.Trim(f, `"'`)
	if !strings.HasPrefix(trimmed, "~") {
		return f, false
	}
	var folded string
	if idx := strings.Index(trimmed, "/"); idx >= 0 {
		if idx == 1 {
			folded = trimmed // "~/..."
		} else {
			// Fold the ~user segment: ~root/.ssh → ~/.ssh so forbidden-path
			// patterns anchored on "~/" still match after traversal.
			folded = "~" + trimmed[idx:]
		}
	} else {
		return f, false // bare "~" or "~user" — no path to resolve
	}
	c := path.Clean("/" + folded)
	if strings.HasPrefix(c, "/~") {
		return strings.Replace(f, trimmed, c[1:], 1), true
	}
	return strings.Replace(f, trimmed, c, 1), true
}

// extractEnvVarName extracts the variable name from "KEY=value" format.
func extractEnvVarName(envVar string) string {
	if idx := strings.Index(envVar, "="); idx >= 0 {
		return envVar[:idx]
	}
	return envVar
}

// isAllowed checks if a value is in the allowlist.
func isAllowed(val string, allowlist []string) bool {
	for _, a := range allowlist {
		if a == val {
			return true
		}
	}
	return false
}

// parseSleepSeconds finds "sleep <n>[s|m|h]" in a command and returns the
// duration in seconds. Returns ok=false when no sleep is present.
func parseSleepSeconds(cmd string) (int, bool) {
	re := regexp.MustCompile(`sleep\s+(\d+)\s*([smhd]?)`)
	m := re.FindStringSubmatch(cmd)
	if len(m) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	unit := "s"
	if len(m) > 2 && m[2] != "" {
		unit = m[2]
	}
	switch unit {
	case "m":
		n *= 60
	case "h":
		n *= 3600
	case "d":
		n *= 86400
	}
	return n, true
}

// matchesOutputFlood reports commands that stream unbounded output
// (/dev/zero, /dev/urandom, `yes`, `: > /dev/...`, `cat /dev/`), returning
// the matched token as evidence.
func matchesOutputFlood(cmd string) (string, bool) {
	for _, pat := range []string{
		"/dev/zero", "/dev/urandom", "/dev/random",
		"cat /dev/", ": > /dev/", "yes ", "yes|", "yes >",
	} {
		if strings.Contains(cmd, pat) {
			return pat, true
		}
	}
	return "", false
}

// matchesConcurrency reports commands requesting parallel fan-out
// (xargs -P, make -j, --parallel, GNU parallel, -P <n>), returning the
// matched fragment as evidence.
func matchesConcurrency(cmd string) (string, bool) {
	re := regexp.MustCompile(`(\bxargs\b[^|&]*\s-P\s*\d+|\s-j\d+\b|--parallel\b|\bparallel\s|curl[^|&]*-Z\b)`)
	m := re.FindString(cmd)
	return m, m != ""
}

// matchesHostExecRisk reports interactive / long-lived host-shell commands
// that are risky to run without review (PTY sessions, editors, followers),
// returning the matched command as evidence.
func matchesHostExecRisk(cmd string) (string, bool) {
	re := regexp.MustCompile(`(^|\s)(top|htop|less|more|vim|vi|nano|tail\s+-f|tail\s+--follow|ssh\s+-t|docker\s+exec\s+-it)(\s|$)`)
	m := re.FindString(cmd)
	return strings.TrimSpace(m), m != ""
}

// redactSecrets masks API keys, tokens, passwords and private-key material
// so they never appear in reports, logs or audit events.
func redactSecrets(text string) string {
	if text == "" {
		return ""
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),
		regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|BEGIN\s+[A-Z ]*PRIVATE\s+KEY)\s*[=:]\s*[^\s&|;]+`),
		regexp.MustCompile(`-----BEGIN\s+[A-Z ]*PRIVATE\s+KEY-----`),
	}
	for _, re := range patterns {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// extractCommandFromArgs parses JSON arguments to find a command string.
// When the JSON contains separate "args" or "arguments" arrays, those
// are appended so that the full command+args string can be scanned by
// the regex rules (e.g. {"command":"rm","args":["-rf","/"]} → "rm -rf /").
//
// When no command field is present (nil/empty/invalid JSON, or none of
// command/cmd/script/code/shell_command), it returns "" — a non-command
// tool like search/read must not have its tool name treated as a shell
// command (which would wrongly trip the allowed-commands gate).
func extractCommandFromArgs(args []byte) string {
	if len(args) == 0 {
		return ""
	}

	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}

	cmdKeys := []string{"command", "cmd", "script", "code", "shell_command"}
	var command string
	for _, key := range cmdKeys {
		if val, ok := m[key].(string); ok && val != "" {
			command = val
			break
		}
	}
	if command == "" {
		return ""
	}

	// Also extract args/arguments arrays and append for full
	// command analysis by the scanner.
	for _, argKey := range []string{"args", "arguments"} {
		if argsVal, ok := m[argKey]; ok {
			var argList []string
			switch v := argsVal.(type) {
			case []interface{}:
				for _, a := range v {
					if s, ok := a.(string); ok {
						argList = append(argList, s)
					}
				}
			case []string:
				argList = v
			}
			if len(argList) > 0 {
				command += " " + strings.Join(argList, " ")
			}
		}
	}
	return command
}

// extractLanguageFromArgs parses JSON arguments to find a "language"/"lang"
// field (codeexec-style tools). An empty result means the language is unknown
// and the caller should fall back to shell semantics.
func extractLanguageFromArgs(args []byte) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	for _, key := range []string{"language", "lang"} {
		if v, ok := m[key].(string); ok {
			return v
		}
	}
	return ""
}

// riskOrder returns an integer ordering for risk levels (higher = worse).
func riskOrder(r RiskLevel) int {
	switch r {
	case RiskLow:
		return 0
	case RiskMedium:
		return 1
	case RiskHigh:
		return 2
	case RiskCritical:
		return 3
	default:
		return 0
	}
}

// metadataUpgrades reports whether a finding with the given risk/action is at
// least as severe as the current report state — the condition under which a
// rule may overwrite the report's metadata (RuleID/Evidence/Recommendation).
func metadataUpgrades(report ScanReport, risk RiskLevel, action Decision) bool {
	if riskOrder(risk) > riskOrder(report.RiskLevel) {
		return true
	}
	return riskOrder(risk) == riskOrder(report.RiskLevel) &&
		actionOrder(action) >= actionOrder(report.Decision)
}

// actionOrder returns an integer ordering for actions (higher = more restrictive).
func actionOrder(d Decision) int {
	switch d {
	case DecisionAllow:
		return 0
	case DecisionAsk:
		return 1
	case DecisionNeedsReview:
		return 2
	case DecisionDeny:
		return 3
	default:
		return 0
	}
}

// hashCommand returns a truncated SHA-256 hash of the command.
func hashCommand(cmd string) string {
	h := sha256.Sum256([]byte(cmd))
	return fmt.Sprintf("%x", h[:8])
}
