// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

// DangerousCmdChecker checks for dangerous commands, destructive paths,
// and sensitive file access.
type DangerousCmdChecker struct {
	deniedCommands   []string
	sensitivePaths   []string
	compiledPatterns []compiledPattern
}

type compiledPattern struct {
	rule toolsafety.PatternRule
	re   *regexp.Regexp
}

// NewDangerousCmdChecker creates a checker from the given policy.
func NewDangerousCmdChecker(policy *toolsafety.SafetyPolicy) *DangerousCmdChecker {
	c := &DangerousCmdChecker{}
	if policy != nil {
		c.deniedCommands = policy.DeniedCommands
		if policy.PathPolicy != nil {
			c.sensitivePaths = policy.PathPolicy.SensitivePaths
		}
		for _, p := range policy.DangerousPatterns {
			if re, err := regexp.Compile(p.Pattern); err == nil {
				c.compiledPatterns = append(c.compiledPatterns, compiledPattern{rule: p, re: re})
			}
		}
	}
	return c
}

// ID returns the checker identifier.
func (c *DangerousCmdChecker) ID() string { return "dangerous_cmd" }

// IsEnabled reports whether this checker is active.
func (c *DangerousCmdChecker) IsEnabled(policy *toolsafety.SafetyPolicy) bool {
	return policy != nil && (len(policy.DeniedCommands) > 0 ||
		len(policy.DangerousPatterns) > 0 ||
		len(policy.PathPolicy.SensitivePaths) > 0 ||
		len(policy.PathPolicy.DeniedPaths) > 0)
}

// Check runs the dangerous command check.
func (c *DangerousCmdChecker) Check(ctx context.Context, req *toolsafety.ScanRequest) ([]toolsafety.RiskFinding, error) {
	var findings []toolsafety.RiskFinding

	if req == nil || req.Command == "" {
		return findings, nil
	}

	cmd := strings.TrimSpace(req.Command)

	// Check denied commands.
	for _, denied := range c.deniedCommands {
		if isCommandMatch(cmd, denied) {
			findings = append(findings, toolsafety.RiskFinding{
				RuleID:         toolsafety.RuleDangerousCommand,
				RiskLevel:      toolsafety.RiskLevelHigh,
				Evidence:       cmd,
				Recommendation: "Command " + denied + " is denied by policy",
				SeverityScore:  8,
				MatchedPattern: "denied_command:" + denied,
			})
		}
	}

	// Check sensitive paths.
	for _, sp := range c.sensitivePaths {
		if matchGlob(sp, cmd) {
			findings = append(findings, toolsafety.RiskFinding{
				RuleID:         toolsafety.RuleSensitivePath,
				RiskLevel:      toolsafety.RiskLevelCritical,
				Evidence:       cmd,
				Recommendation: "Access to sensitive path " + sp + " is not allowed",
				SeverityScore:  10,
				MatchedPattern: "sensitive_path:" + sp,
			})
		}
	}

	// Check dangerous patterns.
	for _, cp := range c.compiledPatterns {
		if cp.re.MatchString(cmd) {
			rl := cp.rule.RiskLevel
			if rl == "" {
				rl = toolsafety.RiskLevelHigh
			}
			rid := cp.rule.RuleID
			if rid == "" {
				rid = toolsafety.RuleDangerousCommand
			}
			findings = append(findings, toolsafety.RiskFinding{
				RuleID:         rid,
				RiskLevel:      rl,
				Evidence:       cmd,
				Recommendation: cp.rule.Description,
				SeverityScore:  9,
				MatchedPattern: "pattern:" + cp.rule.Pattern,
			})
		}
	}

	return findings, nil
}

func isCommandMatch(cmd, denied string) bool {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	base := filepath.Base(fields[0])
	return strings.EqualFold(base, denied) || strings.EqualFold(fields[0], denied)
}

func matchGlob(pattern, cmd string) bool {
	fields := strings.Fields(cmd)
	for _, f := range fields {
		matched, _ := filepath.Match(pattern, f)
		if matched {
			return true
		}
		// Also try matching after resolving relative prefixes.
		base := filepath.Base(f)
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
	}
	// Check if any field contains the pattern as substring (for ** patterns).
	if strings.Contains(pattern, "**") {
		for _, f := range fields {
			if strings.Contains(f, strings.TrimPrefix(pattern, "**")) {
				return true
			}
		}
	}
	return false
}
