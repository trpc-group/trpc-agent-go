//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// resourceChecker validates resource limits on command execution:
// timeout, output size, and infinite-loop patterns.
type resourceChecker struct {
	policy *Policy
}

func (c *resourceChecker) Name() string { return "resource" }

var sleepPattern = regexp.MustCompile(`(?i)\bsleep\s+(\d+)([smhd]?)\b`)
var loopPattern = regexp.MustCompile(`(?i)\b(while\s+true|while\s*:\s*|for\s*\(\s*;\s*;\s*\)|while\s*\(\s*1\s*\)|loop\b)`)

var heavyOutputCmds = map[string]bool{
	"yes":              true,
	"cat /dev/urandom": true,
	"cat /dev/zero":    true,
	"dd":               true,
}

func (c *resourceChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	// Concurrent execution check — issue 4: resource abuse detection.
	if req.ConcurrentCount > 0 && req.ConcurrentCount > c.policy.Resources.MaxConcurrent {
		return &CheckResult{
			Decision:       DecisionAsk,
			RiskLevel:      RiskMedium,
			RuleID:         "RESOURCE_CONCURRENCY",
			Evidence:       fmt.Sprintf("Concurrent executions %d exceeds max %d", req.ConcurrentCount, c.policy.Resources.MaxConcurrent),
			Recommendation: fmt.Sprintf("Too many concurrent tool executions (%d > %d). Wait for running tools to complete or increase max_concurrent in policy.", req.ConcurrentCount, c.policy.Resources.MaxConcurrent),
		}, nil
	}

	text := req.Command
	if len(req.Args) > 0 {
		text += " " + strings.Join(req.Args, " ")
	}

	if req.TimeoutSec > 0 && req.TimeoutSec > c.policy.Resources.MaxTimeoutSec {
		return &CheckResult{
			Decision:       DecisionAsk,
			RiskLevel:      RiskMedium,
			RuleID:         "RESOURCE_TIMEOUT",
			Evidence:       fmt.Sprintf("Requested timeout %ds exceeds max %ds", req.TimeoutSec, c.policy.Resources.MaxTimeoutSec),
			Recommendation: fmt.Sprintf("Reduce timeout to <= %d seconds or split the work into smaller tasks.", c.policy.Resources.MaxTimeoutSec),
		}, nil
	}

	if result := c.checkSleep(text); result != nil {
		return result, nil
	}

	if loopPattern.MatchString(text) {
		return &CheckResult{
			Decision:       DecisionAsk,
			RiskLevel:      RiskHigh,
			RuleID:         "RESOURCE_INFINITE_LOOP",
			Evidence:       text,
			Recommendation: "Command appears to contain an infinite loop. Add an explicit timeout and iteration limit.",
		}, nil
	}

	if result := c.checkHeavyOutput(text); result != nil {
		return result, nil
	}

	return nil, nil
}

func (c *resourceChecker) checkSleep(text string) *CheckResult {
	matches := sleepPattern.FindStringSubmatch(text)
	if len(matches) < 3 {
		return nil
	}
	val, err := strconv.Atoi(matches[1])
	if err != nil {
		return nil
	}
	unit := strings.ToLower(matches[2])
	seconds := val
	switch unit {
	case "m":
		seconds = val * 60
	case "h":
		seconds = val * 3600
	case "d":
		seconds = val * 86400
	}
	if seconds > c.policy.Resources.MaxTimeoutSec {
		return &CheckResult{
			Decision:       DecisionAsk,
			RiskLevel:      RiskMedium,
			RuleID:         "RESOURCE_TIMEOUT",
			Evidence:       fmt.Sprintf("sleep %s (%ds exceeds max %ds)", matches[0], seconds, c.policy.Resources.MaxTimeoutSec),
			Recommendation: fmt.Sprintf("Sleep duration of %ds exceeds the maximum allowed %ds.", seconds, c.policy.Resources.MaxTimeoutSec),
		}
	}
	return nil
}

func (c *resourceChecker) checkHeavyOutput(text string) *CheckResult {
	textLower := strings.ToLower(text)
	tokens := strings.Fields(textLower)
	for cmd := range heavyOutputCmds {
		// Match the first token (the executable name) against heavy-output
		// commands or match multi-word commands as a whole.
		if len(tokens) > 0 && tokens[0] == cmd {
			return &CheckResult{
				Decision:       DecisionAsk,
				RiskLevel:      RiskMedium,
				RuleID:         "RESOURCE_OUTPUT_LIMIT",
				Evidence:       cmd,
				Recommendation: fmt.Sprintf("Command '%s' may produce unbounded output. Pipe through 'head' or set an explicit output limit.", cmd),
			}
		}
		// Match multi-word heavy commands (e.g. "cat /dev/urandom").
		if strings.Contains(cmd, " ") && strings.Contains(textLower, cmd) {
			return &CheckResult{
				Decision:       DecisionAsk,
				RiskLevel:      RiskMedium,
				RuleID:         "RESOURCE_OUTPUT_LIMIT",
				Evidence:       cmd,
				Recommendation: fmt.Sprintf("Command '%s' may produce unbounded output. Pipe through 'head' or set an explicit output limit.", cmd),
			}
		}
	}
	return nil
}
