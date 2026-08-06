// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"fmt"
	"math"
	"strconv"
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
	if len(argv) == 0 {
		return findings
	}
	cmd := normalizeCommandName(argv[0])
	if cmd == "sleep" && len(argv) > 1 {
		seconds, state := parseSleepDuration(argv[1:])
		switch state {
		case sleepDurationUnbounded:
			findings = append(findings, finding(
				RuleResourceLongRunning, CategoryResource, RiskHigh, DecisionDeny,
				"unbounded sleep duration detected",
				loc,
				"Use a finite sleep duration within the configured limit.",
			))
		case sleepDurationInvalid:
			findings = append(findings, finding(
				RuleResourceLongRunning, CategoryResource, RiskMedium, DecisionAsk,
				"sleep duration could not be parsed safely: "+strings.Join(argv[1:], " "),
				loc,
				"Use numeric sleep operands with optional s, m, h, or d suffixes.",
			))
		case sleepDurationValid:
			if s.policy.ResourceLimits.MaxSleepSeconds <= 0 ||
				seconds <= float64(s.policy.ResourceLimits.MaxSleepSeconds) {
				break
			}
			findings = append(findings, finding(
				RuleResourceLongRunning, CategoryResource, RiskMedium, DecisionAsk,
				fmt.Sprintf("sleep duration %gs exceeds policy max %ds", seconds, s.policy.ResourceLimits.MaxSleepSeconds),
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

type sleepDurationState int

const (
	sleepDurationValid sleepDurationState = iota
	sleepDurationInvalid
	sleepDurationUnbounded
)

func parseSleepDuration(args []string) (float64, sleepDurationState) {
	var total float64
	for _, raw := range args {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "infinity" || value == "inf" {
			return 0, sleepDurationUnbounded
		}
		if value == "--help" || value == "--version" {
			continue
		}
		multiplier := float64(1)
		if len(value) > 0 {
			switch value[len(value)-1] {
			case 's':
				value = value[:len(value)-1]
			case 'm':
				value = value[:len(value)-1]
				multiplier = 60
			case 'h':
				value = value[:len(value)-1]
				multiplier = 60 * 60
			case 'd':
				value = value[:len(value)-1]
				multiplier = 24 * 60 * 60
			}
		}
		n, err := strconv.ParseFloat(value, 64)
		if math.IsInf(n, 0) {
			return 0, sleepDurationUnbounded
		}
		if err != nil || n < 0 || math.IsNaN(n) {
			return 0, sleepDurationInvalid
		}
		total += n * multiplier
		if math.IsInf(total, 0) || total > math.MaxInt64 {
			return 0, sleepDurationUnbounded
		}
	}
	return total, sleepDurationValid
}
