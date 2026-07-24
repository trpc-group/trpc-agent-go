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
	"strings"
	"time"

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
	// Category, Recommendation) is only updated when the risk or
	// action is worse, so the report always points at the most
	// important rule that matched.
	for _, cr := range s.compiledRe {
		for _, re := range cr.Patterns {
			if matches := re.FindStringSubmatch(fullCommand); matches != nil {
				matchedRisk := riskOrder(cr.Rule.RiskLevel)
				currentRisk := riskOrder(report.RiskLevel)
				matchedAction := actionOrder(cr.Rule.Action)
				currentAction := actionOrder(report.Decision)

				// Update metadata only when this match is worse or equal.
				if matchedRisk > currentRisk ||
					(matchedRisk == currentRisk && matchedAction >= currentAction) {
					report.RuleID = cr.Rule.ID
					report.Evidence = matches[0]
					report.Category = cr.Rule.Category
					report.Recommendation = fmt.Sprintf(
						"Rule %s (%s): %s",
						cr.Rule.ID, cr.Rule.Category, cr.Rule.Description,
					)
				}

				// Upgrade risk level if this rule is worse.
				if matchedRisk > currentRisk {
					report.RiskLevel = cr.Rule.RiskLevel
				}
				// Upgrade action if this rule is more restrictive.
				if matchedAction > currentAction {
					report.Decision = cr.Rule.Action
				}
			}
		}
	}

	// 4. Check for dangerous paths in command.
	for _, forbidden := range s.policy.ForbiddenPaths {
		if strings.Contains(fullCommand, forbidden) {
			if riskOrder(RiskCritical) > riskOrder(report.RiskLevel) {
				report.RiskLevel = RiskCritical
			}
			if actionOrder(DecisionDeny) > actionOrder(report.Decision) {
				report.Decision = DecisionDeny
			}
			report.RuleID = "forbidden_path"
			report.Evidence = forbidden
			report.Category = "dangerous_commands"
			report.Recommendation = fmt.Sprintf(
				"Command references forbidden path: %s", forbidden,
			)
		}
	}

	// 5. Check AllowedCommands whitelist — if configured, commands
	// not in the allowlist are denied, but only when no regex rule
	// has already made a more nuanced decision (ask/deny from regex
	// takes priority over the allowlist gate).
	if len(s.policy.AllowedCommands) > 0 && report.Decision == DecisionAllow && cmdName != "" {
		if !isAllowed(cmdName, s.policy.AllowedCommands) {
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
				"Command %q is not in the allowed commands list", cmdName,
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
	command := extractCommandFromArgs(req.Arguments, req.ToolName)

	scanReq := ScanRequest{
		ToolName: req.ToolName,
		Command:  command,
		Backend:  "permission_check",
	}
	report := s.Scan(ctx, scanReq)

	decision := tool.PermissionDecision{
		Action: tool.PermissionAction(report.Decision),
		Reason: report.Recommendation,
	}
	return decision, nil
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
func extractHostTarget(fullCommand string) string {
	// Look for common URL/host patterns: https://host/path, host:port
	// Simple heuristic: find the first token after curl/wget that
	// looks like a URL or hostname.
	parts := strings.Fields(fullCommand)
	for i, p := range parts {
		if i == 0 {
			continue // skip the command itself.
		}
		// Skip flags.
		if strings.HasPrefix(p, "-") {
			continue
		}
		// Strip URL scheme.
		p = strings.TrimPrefix(p, "https://")
		p = strings.TrimPrefix(p, "http://")
		// Extract host (before first / or :).
		if idx := strings.IndexAny(p, "/:"); idx >= 0 {
			p = p[:idx]
		}
		if p != "" {
			return p
		}
	}
	return ""
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

// extractCommandFromArgs parses JSON arguments to find a command string.
func extractCommandFromArgs(args []byte, fallback string) string {
	if len(args) == 0 {
		return fallback
	}

	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return fallback
	}

	cmdKeys := []string{"command", "cmd", "script", "code", "shell_command"}
	for _, key := range cmdKeys {
		if val, ok := m[key].(string); ok && val != "" {
			return val
		}
	}

	return fallback
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
