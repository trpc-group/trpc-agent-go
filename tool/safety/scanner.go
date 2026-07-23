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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// Scanner applies a set of Rules to incoming scan requests and
// produces a ScanReport with a final decision.
type Scanner struct {
	policy *Policy
	rules  []Rule
}

// NewScanner creates a Scanner from a policy, wiring up the default
// set of rules covering all seven risk categories.
func NewScanner(policy *Policy) (*Scanner, error) {
	if policy == nil {
		policy = DefaultPolicy()
	}
	rules, err := defaultRules(policy)
	if err != nil {
		return nil, fmt.Errorf("safety: build rules: %w", err)
	}
	return &Scanner{policy: policy, rules: rules}, nil
}

// ScanCommand runs all rules against req and returns a structured
// report.  Commands that cannot be parsed by shellsafe are denied
// immediately (conservative default).
func (s *Scanner) ScanCommand(ctx context.Context, req *ScanRequest) (*ScanReport, error) {
	report := &ScanReport{
		Timestamp: time.Now(),
		ToolName:  req.ToolName,
		Command:   req.Command,
		Backend:   req.Backend,
		Language:  req.Language,
		Risks:     []Risk{},
	}

	// --- Stage 0: empty command ---
	if strings.TrimSpace(req.Command) == "" {
		report.Verdict = VerdictDeny
		report.RiskLevel = RiskHigh
		report.Risks = append(report.Risks, Risk{
			RuleID:      "empty_command",
			RuleName:    "Empty Command",
			Level:       RiskHigh,
			Evidence:    "command is empty",
			Suggestion:  "provide a non-empty command",
			ShouldBlock: true,
		})
		report.Recommendation = report.buildRecommendation()
		return report, nil
	}

	// --- Stage 1: shellsafe parse (conservative) ---
	// Only apply shellsafe to shell-command backends, not to codeexec
	// (which receives source code, not shell commands).
	if req.Backend != BackendCodeExec {
		pipe, err := shellsafe.Parse(req.Command)
		if err != nil {
			// Parse error → deny immediately (conservative default).
			// We still run semantic rules so the report captures all
			// risk categories, but the parse error alone is blocking.
			report.Risks = append(report.Risks, Risk{
				RuleID:      "shellsafe_parse_error",
				RuleName:    "Shell Parse Error",
				Level:       RiskHigh,
				Evidence:    err.Error(),
				Suggestion:  "simplify the command to avoid shell features ($(), backticks, redirections, etc.)",
				ShouldBlock: true,
			})
		} else {
			// Apply shellsafe allow/deny policy if active.  A policy
			// rejection adds a dangerous_command risk but does NOT
			// short-circuit — semantic rules still run so the report
			// can include network, dependency, and other risks.
			shellPolicy := shellsafe.PolicyFromLists(s.policy.Commands.Allowed, s.policy.Commands.Denied)
			if shellPolicy.Active() {
				if err := shellPolicy.Check(pipe); err != nil {
					report.Risks = append(report.Risks, Risk{
						RuleID:      "dangerous_command",
						RuleName:    "Dangerous Command",
						Level:       RiskHigh,
						Evidence:    err.Error(),
						Suggestion:  "use a command from the allowed list or wrap it in an auditable script",
						ShouldBlock: true,
					})
				}
			}
		}
	}

	// --- Stage 2: semantic rules ---
	for _, rule := range s.rules {
		if risk := rule.Check(ctx, req); risk != nil {
			if !hasRuleID(report.Risks, risk.RuleID) {
				report.Risks = append(report.Risks, *risk)
			}
		}
	}

	// --- Stage 3: compute verdict ---
	report.Verdict = s.computeVerdict(report.Risks)
	report.RiskLevel = computeMaxLevel(report.Risks)
	report.Recommendation = report.buildRecommendation()

	return report, nil
}

func (s *Scanner) computeVerdict(risks []Risk) Verdict {
	for _, r := range risks {
		if r.ShouldBlock {
			return VerdictDeny
		}
	}
	for _, r := range risks {
		if r.Level == RiskHigh || r.Level == RiskCritical {
			return VerdictDeny
		}
	}
	for _, r := range risks {
		if r.Level == RiskMedium {
			return VerdictAsk
		}
	}
	// No blocking risks, no high/critical, no medium risks.
	// Fall back to the policy's DefaultVerdict.
	if s.policy.DefaultVerdict != "" {
		return s.policy.DefaultVerdict
	}
	return VerdictAllow
}

func computeMaxLevel(risks []Risk) RiskLevel {
	if len(risks) == 0 {
		return RiskLow
	}
	max := RiskLow
	rank := map[RiskLevel]int{
		RiskLow: 0, RiskMedium: 1, RiskHigh: 2, RiskCritical: 3,
	}
	for _, r := range risks {
		if rank[r.Level] > rank[max] {
			max = r.Level
		}
	}
	return max
}

// hasRuleID reports whether any risk in the slice has the given RuleID.
func hasRuleID(risks []Risk, ruleID string) bool {
	for _, r := range risks {
		if r.RuleID == ruleID {
			return true
		}
	}
	return false
}

func (r *ScanReport) buildRecommendation() string {
	if len(r.Risks) == 0 {
		return "No security risks detected."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Security scan detected %d risk(s):", len(r.Risks))
	for _, risk := range r.Risks {
		fmt.Fprintf(&b, "\n- [%s] %s: %s → %s",
			risk.Level, risk.RuleName, risk.Evidence, risk.Suggestion)
	}
	return b.String()
}
