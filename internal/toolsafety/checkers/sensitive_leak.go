// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

// defaultSensitivePatterns covers common credential formats.
var defaultSensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*['\"][^'\"]{8,}['\"]`),
	regexp.MustCompile(`(?i)(secret|token|password|passwd)\s*[:=]\s*['\"][^'\"]{8,}['\"]`),
	regexp.MustCompile(`-----BEGIN (RSA |EC )?PRIVATE KEY-----`),
	regexp.MustCompile(`gh[ps]_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{32,}`),
}

// SensitiveLeakChecker detects sensitive information in command output.
type SensitiveLeakChecker struct {
	patterns []*regexp.Regexp
}

// NewSensitiveLeakChecker creates a checker from the given policy.
func NewSensitiveLeakChecker(policy *toolsafety.SafetyPolicy) *SensitiveLeakChecker {
	c := &SensitiveLeakChecker{patterns: defaultSensitivePatterns}
	if policy != nil && len(policy.SensitivePatterns) > 0 {
		for _, sp := range policy.SensitivePatterns {
			if re, err := regexp.Compile(sp); err == nil {
				c.patterns = append(c.patterns, re)
			}
		}
	}
	return c
}

// ID returns the checker identifier.
func (c *SensitiveLeakChecker) ID() string { return "sensitive_leak" }

// IsEnabled reports whether this checker is active.
func (c *SensitiveLeakChecker) IsEnabled(policy *toolsafety.SafetyPolicy) bool {
	return true
}

// Check runs the sensitive leak check against the command's output context.
// Note: this checker primarily operates on the command itself; output-level
// scanning is done by the runner post-execution via Sanitize methods.
func (c *SensitiveLeakChecker) Check(ctx context.Context, req *toolsafety.ScanRequest) ([]toolsafety.RiskFinding, error) {
	var findings []toolsafety.RiskFinding

	if req == nil || req.Command == "" {
		return findings, nil
	}

	cmd := strings.TrimSpace(req.Command)
	for _, re := range c.patterns {
		if m := re.FindString(cmd); m != "" {
			findings = append(findings, toolsafety.RiskFinding{
				RuleID:         toolsafety.RuleSensitiveLeak,
				RiskLevel:      toolsafety.RiskLevelHigh,
				Evidence:       "sensitive pattern matched in command",
				Recommendation: "Command may contain or produce sensitive credentials; review before execution",
				SeverityScore:  8,
				MatchedPattern: re.String(),
			})
			break
		}
	}

	return findings, nil
}

// SanitizeOutput replaces sensitive patterns in the given text with a placeholder.
func (c *SensitiveLeakChecker) SanitizeOutput(output string) string {
	for _, re := range c.patterns {
		output = re.ReplaceAllString(output, "***REDACTED***")
	}
	return output
}
