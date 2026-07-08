// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"fmt"
	"strings"
)

func (s *Scanner) scanResources(req ExecutionRequest) []Finding {
	var findings []Finding
	timeoutMS := req.TimeoutMS
	if timeoutMS == 0 && req.Timeout > 0 {
		timeoutMS = req.Timeout.Milliseconds()
	}
	max := s.policy.ResourceLimits.MaxTimeoutMS
	if req.Backend == BackendHostExec && s.policy.BackendRules.HostExec.MaxTimeoutMS > 0 {
		max = s.policy.BackendRules.HostExec.MaxTimeoutMS
	}
	if max > 0 && timeoutMS > max {
		findings = append(findings, finding(
			RuleResourceTimeout, CategoryResource, RiskMedium, DecisionAsk,
			fmt.Sprintf("timeout %dms exceeds policy max %dms", timeoutMS, max),
			"timeout",
			"Reduce timeout or require human review for long-running execution.",
		))
	}
	if limit := s.policy.ResourceLimits.MaxOutputBytes; limit > 0 && req.MaxOutputBytes > limit {
		findings = append(findings, finding(
			RuleResourceOutput, CategoryResource, RiskMedium, DecisionAsk,
			fmt.Sprintf("max output %d bytes exceeds policy max %d bytes", req.MaxOutputBytes, limit),
			"max_output_bytes",
			"Reduce output cap or stream through reviewed artifact handling.",
		))
	}
	return findings
}

func (s *Scanner) scanResourceArgv(argv []string, loc string) []Finding {
	var findings []Finding
	cmd := normalizeCommandName(argv[0])
	if cmd == "sleep" && len(argv) > 1 {
		if n, ok := parseIntArg(argv[1]); ok && s.policy.ResourceLimits.MaxSleepSeconds > 0 && n > s.policy.ResourceLimits.MaxSleepSeconds {
			findings = append(findings, finding(
				RuleResourceLongRunning, CategoryResource, RiskMedium, DecisionAsk,
				fmt.Sprintf("sleep duration %ds exceeds policy max %ds", n, s.policy.ResourceLimits.MaxSleepSeconds),
				loc,
				"Reduce sleep duration or use an explicit background session with review.",
			))
		}
	}
	joined := strings.ToLower(strings.Join(argv, " "))
	if strings.Contains(joined, "while true") ||
		strings.Contains(joined, "for (( ; ;") ||
		strings.Contains(joined, "yes ") ||
		cmd == "yes" {
		findings = append(findings, finding(
			RuleResourceLongRunning, CategoryResource, RiskHigh, DecisionDeny,
			"infinite-loop or unbounded output pattern detected",
			loc,
			"Bound loops and output before execution.",
		))
	}
	if strings.Contains(joined, "xargs -p") || strings.Contains(joined, "parallel ") {
		findings = append(findings, finding(
			RuleResourceParallelism, CategoryResource, RiskMedium, DecisionAsk,
			"parallel execution pattern detected",
			loc,
			"Limit concurrency explicitly and review resource impact.",
		))
	}
	return findings
}
