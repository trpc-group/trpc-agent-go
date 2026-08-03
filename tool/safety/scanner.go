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

	// 1.5 ShellSafe 保守解析：无法安全解析的命令 fail-closed → deny。
	// （issue 要求：对无法安全解析的命令返回 deny 或 ask，不得默认 allow）
	if fullCommand != "" {
		if _, perr := parseShellCommands(fullCommand); perr != nil {
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
	for _, forbidden := range s.policy.ForbiddenPaths {
		if strings.Contains(fullCommand, forbidden) || strings.Contains(req.WorkDir, forbidden) {
			// Apply the same guard as regex rules: only update
			// metadata when the finding is worse than or equal
			// to the current report state.
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) ||
				(riskOrder(RiskCritical) == riskOrder(report.RiskLevel) &&
					actionOrder(DecisionDeny) >= actionOrder(report.Decision)) {
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

	// 6. Check allowlisted hosts for network egress commands.
	if cmdName == "curl" || cmdName == "wget" || cmdName == "nc" || cmdName == "ssh" {
		if len(s.policy.AllowlistedHosts) > 0 {
			target := extractHostTarget(fullCommand)
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
			report.RuleID = "sleep_timeout_exceeded"
			report.Evidence = fmt.Sprintf("sleep %ds", secs)
			report.Category = "resource_abuse"
			report.Recommendation = fmt.Sprintf(
				"sleep %ds exceeds max timeout %ds", secs, s.policy.MaxTimeoutSec)
		}
	}

	// 8.6 Resource abuse: output flood / unbounded stream.
	if s.policy.MaxOutputBytes > 0 && matchesOutputFlood(fullCommand) {
		if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
			report.RiskLevel = RiskCritical
		}
		if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
			report.Decision = DecisionDeny
		}
		report.RuleID = "output_flood"
		report.Category = "resource_abuse"
		report.Recommendation = "Command streams unbounded output (/dev/zero, yes, etc.)"
	}

	// 8.7 Resource abuse: concurrency flags (parallel fan-out).
	if matchesConcurrency(fullCommand) {
		if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
			report.RiskLevel = RiskHigh
		}
		if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
			report.Decision = DecisionAsk
		}
		report.RuleID = "concurrent_execution"
		report.Category = "resource_abuse"
		report.Recommendation = "Command requests concurrent/parallel execution; review required"
	}

	// 8.8 HostExec safety boundary: interactive / long-lived PTY sessions
	// run directly on the host shell and carry higher risk.
	if req.Backend == "hostexec" && matchesHostExecRisk(fullCommand) {
		if riskOrder(RiskHigh) > riskOrder(report.RiskLevel) {
			report.RiskLevel = RiskHigh
		}
		if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
			report.Decision = DecisionAsk
		}
		report.RuleID = "hostexec_long_session"
		report.Category = "host_execution"
		report.Recommendation = "hostexec interactive/long-lived session (top, tail -f, editor, etc.); review required"
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

// extractHostTarget extracts a hostname or IP from a curl/wget/nc/ssh
// command arguments for allowlist checking.
//
// A URL-form token (contains "://") is the strongest signal of the real
// target and wins over any plain token seen earlier — this prevents a
// flag value such as `curl -o api.github.com https://evil.example.com`
// from being mistaken for the destination. Without a URL, the first
// non-flag token is used.
func extractHostTarget(fullCommand string) string {
	parts := strings.Fields(fullCommand)
	firstPlain := ""
	for i, p := range parts {
		if i == 0 {
			continue // skip the command itself.
		}
		// Skip flags (including inline --flag=value forms).
		if strings.HasPrefix(p, "-") {
			continue
		}
		if strings.Contains(p, "://") {
			// A URL is almost certainly the actual target.
			return normalizeHost(p)
		}
		if firstPlain == "" {
			firstPlain = normalizeHost(p)
		}
	}
	return firstPlain
}

// normalizeHost strips a URL scheme and path/port suffix from a token.
func normalizeHost(p string) string {
	for _, scheme := range []string{"https://", "http://", "ftp://"} {
		p = strings.TrimPrefix(p, scheme)
	}
	if idx := strings.IndexAny(p, "/:"); idx >= 0 {
		p = p[:idx]
	}
	return p
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
// (/dev/zero, /dev/urandom, `yes`, `: > /dev/...`, `cat /dev/`).
func matchesOutputFlood(cmd string) bool {
	for _, pat := range []string{
		"/dev/zero", "/dev/urandom", "/dev/random",
		"cat /dev/", ": > /dev/", "yes ", "yes|", "yes >",
	} {
		if strings.Contains(cmd, pat) {
			return true
		}
	}
	return false
}

// matchesConcurrency reports commands requesting parallel fan-out
// (xargs -P, make -j, --parallel, GNU parallel, -P <n>).
func matchesConcurrency(cmd string) bool {
	re := regexp.MustCompile(`(\bxargs\b[^|&]*\s-P\s*\d+|\s-j\d+\b|--parallel\b|\bparallel\s|curl[^|&]*-Z\b)`)
	return re.MatchString(cmd)
}

// matchesHostExecRisk reports interactive / long-lived host-shell commands
// that are risky to run without review (PTY sessions, editors, followers).
func matchesHostExecRisk(cmd string) bool {
	re := regexp.MustCompile(`(^|\s)(top|htop|less|more|vim|vi|nano|tail\s+-f|tail\s+--follow|ssh\s+-t|docker\s+exec\s+-it)(\s|$)`)
	return re.MatchString(cmd)
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
