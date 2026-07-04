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
func NewScanner(policy *Policy) *Scanner {
	s := &Scanner{
		policy:  policy,
		auditor: NewAuditor(),
	}
	s.compileRules()
	return s
}

// compileRules pre-compiles all regex patterns for performance.
func (s *Scanner) compileRules() {
	s.compiledRe = make([]compiledRule, 0, len(s.policy.Rules))
	for _, rule := range s.policy.Rules {
		cr := compiledRule{Rule: rule}
		for _, pattern := range rule.Patterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				// Skip invalid patterns; they are logged at policy-load time.
				continue
			}
			cr.Patterns = append(cr.Patterns, re)
		}
		s.compiledRe = append(s.compiledRe, cr)
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

	// Check against all rules. Worst risk level and most restrictive
	// action win.
	for _, cr := range s.compiledRe {
		for _, re := range cr.Patterns {
			if matches := re.FindStringSubmatch(fullCommand); matches != nil {
				// Upgrade risk level if this rule is worse.
				if riskOrder(cr.Rule.RiskLevel) > riskOrder(report.RiskLevel) {
					report.RiskLevel = cr.Rule.RiskLevel
				}
				// Upgrade action if this rule is more restrictive.
				if actionOrder(cr.Rule.Action) > actionOrder(report.Decision) {
					report.Decision = cr.Rule.Action
				}
				report.RuleID = cr.Rule.ID
				report.Evidence = matches[0]
				report.Category = cr.Rule.Category
				report.Recommendation = fmt.Sprintf(
					"Rule %s (%s): %s",
					cr.Rule.ID, cr.Rule.Category, cr.Rule.Description,
				)
			}
		}
	}

	// Additional checks beyond regex patterns.

	// Check for dangerous paths in command.
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

	// Check excessive command length (potential abuse).
	if len(fullCommand) > 10000 {
		report.RiskLevel = RiskMedium
		if actionOrder(DecisionAsk) > actionOrder(report.Decision) {
			report.Decision = DecisionAsk
		}
		report.RuleID = "excessive_length"
		report.Category = "resource_abuse"
		report.Recommendation = "Command exceeds 10000 characters; review required."
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
// It extracts command/script/code fields from req.Arguments for
// scanning, falling back to the tool name if no command is found.
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

// extractCommandFromArgs parses JSON arguments to find a command
// string. It checks common field names used by exec tools.
func extractCommandFromArgs(args []byte, fallback string) string {
	if len(args) == 0 {
		return fallback
	}

	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return fallback
	}

	// Check common command field names in priority order.
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
