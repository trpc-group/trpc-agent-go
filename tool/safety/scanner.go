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
	"time"
)

// Checker is implemented by every safety check module.
// Scanners aggregate checkers with the priority: Deny > Ask > Allow.
//
// A checker returns nil when no issue is found (the command passes).
// When a finding exists, it returns a CheckResult with the
// appropriate Decision and RiskLevel.
type Checker interface {
	Name() string
	Check(ctx context.Context, req *ScanRequest) (*CheckResult, error)
}

// ScanRequest is the normalized input to all checkers.
type ScanRequest struct {
	Command         string
	Args            []string
	Cwd             string
	Env             map[string]string
	ToolName        string
	Backend         string
	TimeoutSec      int
	ConcurrentCount int
}

// CheckResult is returned by each checker.
type CheckResult struct {
	Decision       Decision
	RiskLevel      RiskLevel
	RuleID         string
	Evidence       string
	Recommendation string
}

// Scanner runs all registered checkers and produces an aggregated SafetyReport.
//
// Aggregation: Deny > Ask > Allow. First Deny wins. Highest-risk Ask
// among multiple Ask findings provides the report details.
type Scanner struct {
	checkers []Checker
	policy   *Policy
	audit    AuditLogger
}

// NewScanner creates a Scanner with all built-in checkers registered.
func NewScanner(policy *Policy, audit AuditLogger) *Scanner {
	s := &Scanner{policy: policy, audit: audit}
	s.checkers = []Checker{
		&commandChecker{policy: policy},
		&secretCmdChecker{policy: policy},
		&envChecker{policy: policy},
		&networkChecker{policy: policy},
		&pathChecker{policy: policy},
		&hostChecker{policy: policy},
		&resourceChecker{policy: policy},
	}
	return s
}

// Scan runs all checkers serially and returns the aggregated SafetyReport.
func (s *Scanner) Scan(ctx context.Context, req *ScanRequest) *SafetyReport {
	start := time.Now()
	report := &SafetyReport{
		ToolName:  req.ToolName,
		Command:   req.Command,
		Backend:   req.Backend,
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		Checkers:  make([]CheckerOutcome, 0),
	}

	for _, c := range s.checkers {
		result, err := c.Check(ctx, req)
		if err != nil {
			if report.Decision == DecisionAllow {
				report.Decision = DecisionAsk
				report.RiskLevel = RiskLow
			}
			report.Checkers = append(report.Checkers, CheckerOutcome{
				Name: c.Name(), Status: "err", Err: err.Error(),
			})
			continue
		}
		if result == nil {
			report.Checkers = append(report.Checkers, CheckerOutcome{
				Name: c.Name(), Status: "pass",
			})
			continue
		}
		report.Checkers = append(report.Checkers, CheckerOutcome{
			Name: c.Name(), Status: string(result.Decision),
		})

		switch result.Decision {
		case DecisionDeny:
			if report.Decision == DecisionDeny {
				continue
			}
			report.Decision = DecisionDeny
			report.RiskLevel = result.RiskLevel
			report.RuleID = result.RuleID
			report.Evidence = result.Evidence
			report.Recommendation = result.Recommendation
			report.Blocked = true
		case DecisionAsk:
			if report.Decision != DecisionDeny {
				report.Decision = DecisionAsk
				if riskPriority(result.RiskLevel) >= riskPriority(report.RiskLevel) {
					report.RiskLevel = result.RiskLevel
					report.RuleID = result.RuleID
					report.Evidence = result.Evidence
					report.Recommendation = result.Recommendation
				}
			}
		}
	}

	report.DurationMs = time.Since(start).Milliseconds()
	return report
}

// SetCheckers replaces the default checker list. Used for testing.
func (s *Scanner) SetCheckers(checkers []Checker) {
	s.checkers = checkers
}

// DesensitizeEvidence applies the policy's secret-detection patterns.
func (sc *Scanner) DesensitizeEvidence(evidence string) string {
	if evidence == "" || sc.policy == nil {
		return evidence
	}
	return Desensitize(evidence, sc.policy.SecretRegexps())
}

// NewTestScanner creates a Scanner without an audit logger for use in tests.
func NewTestScanner(policy *Policy) *Scanner {
	return NewScanner(policy, nil)
}

// ScanCtx is a convenience wrapper for Scan.
func (s *Scanner) ScanCtx(ctx context.Context, req *ScanRequest) *SafetyReport {
	return s.Scan(ctx, req)
}
