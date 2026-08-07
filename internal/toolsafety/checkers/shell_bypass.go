// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

// wrapperCommands are shell commands that can launch arbitrary code
// under their own argv[0], bypassing argv-based allow/deny policies.
var wrapperCommands = map[string]string{
	"sh":      "shell",
	"bash":    "shell",
	"zsh":     "shell",
	"dash":    "shell",
	"eval":    "eval",
	"exec":    "exec",
	"sudo":    "privilege",
	"su":      "privilege",
	"env":     "env-wrapper",
	"xargs":   "xargs",
	"nohup":   "nohup",
	"timeout": "timeout",
	"busybox": "busybox",
}

// ShellBypassChecker checks for shell wrapper and injection patterns
// that could bypass command policies.
type ShellBypassChecker struct {
}

// NewShellBypassChecker creates a ShellBypassChecker.
func NewShellBypassChecker() *ShellBypassChecker {
	return &ShellBypassChecker{}
}

// ID returns the checker identifier.
func (c *ShellBypassChecker) ID() string { return "shell_bypass" }

// IsEnabled reports whether this checker is active.
func (c *ShellBypassChecker) IsEnabled(policy *toolsafety.SafetyPolicy) bool {
	return true
}

// Check runs the shell bypass check.
func (c *ShellBypassChecker) Check(ctx context.Context, req *toolsafety.ScanRequest) ([]toolsafety.RiskFinding, error) {
	var findings []toolsafety.RiskFinding

	if req == nil || req.Command == "" {
		return findings, nil
	}

	cmd := strings.TrimSpace(req.Command)
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return findings, nil
	}

	first := fields[0]

	// Check if the command is a shell wrapper.
	if kind, ok := wrapperCommands[strings.ToLower(first)]; ok {
		evidence := cmd
		if len(cmd) > 120 {
			evidence = cmd[:120] + "..."
		}

		ruleID := toolsafety.RuleShellWrapper
		riskLevel := toolsafety.RiskLevelHigh
		score := 8

		if kind == "privilege" {
			ruleID = toolsafety.RulePrivilegeEscalation
			riskLevel = toolsafety.RiskLevelCritical
			score = 10
		}

		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         ruleID,
			RiskLevel:      riskLevel,
			Evidence:       evidence,
			Recommendation: first + " is a shell wrapper that can bypass command policy; use a direct command instead",
			SeverityScore:  score,
			MatchedPattern: kind + ":" + first,
		})
	}

	// Try shellsafe.Parse to detect injection patterns.
	_, err := shellsafe.Parse(cmd)
	if err != nil {
		// shellsafe rejected this command — it contains unsafe constructs.
		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         toolsafety.RuleCommandInjection,
			RiskLevel:      toolsafety.RiskLevelCritical,
			Evidence:       err.Error(),
			Recommendation: "Command contains unsafe shell constructs: " + err.Error(),
			SeverityScore:  9,
			MatchedPattern: "shellsafe_reject",
		})
	}

	return findings, nil
}
