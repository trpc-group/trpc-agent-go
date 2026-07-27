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
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

var (
	reSleep      = regexp.MustCompile(`\bsleep\s+(\d+)`)
	reTimeout    = regexp.MustCompile(`(?:--)?timeout(?:=|\s+)(\d+)`)
	reYes        = regexp.MustCompile(`^yes\b`)
	reWhileTrue  = regexp.MustCompile(`\bwhile\s+true\b`)
	reForLoop    = regexp.MustCompile(`\bfor\s+\w+\s+in\b`)
	reHugeOutput = regexp.MustCompile(`\bcat\s+/dev/(?:urandom|zero)\b`)
)

// ResourceAbuseChecker checks for excessive timeout, output size, sleep, and loops.
type ResourceAbuseChecker struct {
	maxSleepS int
	maxOutput int64
}

// NewResourceAbuseChecker creates a checker from the given policy.
func NewResourceAbuseChecker(policy *toolsafety.SafetyPolicy) *ResourceAbuseChecker {
	c := &ResourceAbuseChecker{maxSleepS: 60, maxOutput: 10 << 20}
	if policy != nil && policy.ResourcePolicy != nil {
		if policy.ResourcePolicy.MaxSleepS > 0 {
			c.maxSleepS = policy.ResourcePolicy.MaxSleepS
		}
		if policy.ResourcePolicy.MaxOutputBytes > 0 {
			c.maxOutput = policy.ResourcePolicy.MaxOutputBytes
		}
	}
	return c
}

// ID returns the checker identifier.
func (c *ResourceAbuseChecker) ID() string { return "resource_abuse" }

// IsEnabled reports whether this checker is active.
func (c *ResourceAbuseChecker) IsEnabled(policy *toolsafety.SafetyPolicy) bool {
	return policy != nil && policy.ResourcePolicy != nil
}

// Check runs the resource abuse check.
func (c *ResourceAbuseChecker) Check(ctx context.Context, req *toolsafety.ScanRequest) ([]toolsafety.RiskFinding, error) {
	var findings []toolsafety.RiskFinding

	if req == nil || req.Command == "" {
		return findings, nil
	}

	cmd := strings.TrimSpace(req.Command)

	// Check for huge output commands.
	if reHugeOutput.MatchString(cmd) {
		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         toolsafety.RuleResourceOutputSize,
			RiskLevel:      toolsafety.RiskLevelHigh,
			Evidence:       cmd,
			Recommendation: "Command may produce unbounded output; consider limiting output size",
			SeverityScore:  7,
			MatchedPattern: "huge_output",
		})
	}

	// Check infinite loops.
	if reWhileTrue.MatchString(cmd) || (reForLoop.MatchString(cmd) && strings.Contains(cmd, "in")) {
		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         toolsafety.RuleResourceSleepLoop,
			RiskLevel:      toolsafety.RiskLevelHigh,
			Evidence:       cmd,
			Recommendation: "Command contains an unbounded loop that may run forever",
			SeverityScore:  7,
			MatchedPattern: "infinite_loop",
		})
	}

	// Check sleep durations.
	for _, m := range reSleep.FindAllStringSubmatch(cmd, -1) {
		if len(m) < 2 {
			continue
		}
		sec, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if sec > c.maxSleepS {
			findings = append(findings, toolsafety.RiskFinding{
				RuleID:         toolsafety.RuleResourceSleepLoop,
				RiskLevel:      toolsafety.RiskLevelMedium,
				Evidence:       "sleep " + m[1],
				Recommendation: "Sleep of " + m[1] + "s exceeds the configured maximum of " + strconv.Itoa(c.maxSleepS) + "s",
				SeverityScore:  5,
				MatchedPattern: "long_sleep",
			})
		}
	}

	// Check timeout alignment.
	if req.TimeoutS > 0 && c.maxSleepS > 0 && req.TimeoutS > c.maxSleepS*2 {
		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         toolsafety.RuleResourceTimeout,
			RiskLevel:      toolsafety.RiskLevelLow,
			Evidence:       "timeout " + strconv.Itoa(req.TimeoutS) + "s",
			Recommendation: "Request timeout (" + strconv.Itoa(req.TimeoutS) + "s) is unusually large",
			SeverityScore:  3,
			MatchedPattern: "large_timeout",
		})
	}

	return findings, nil
}
