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
	// ParseError is set when the request arguments could not be parsed
	// (e.g. malformed JSON), meaning Command/Args may not reflect the
	// real command. Checkers ignore it; SafetyPermissionPolicy uses it
	// to fail closed (Ask) instead of scanning an empty command.
	ParseError string
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
//
// Audit logging is handled by the caller (typically SafetyPermissionPolicy),
// not by Scan itself. This keeps Scan pure and testable.
type Scanner struct {
	checkers []Checker
	policy   *Policy
}

// allCheckers builds the full list of available checkers.
func allCheckers(policy *Policy) []Checker {
	return []Checker{
		&commandChecker{policy: policy},
		&secretCmdChecker{policy: policy},
		&envChecker{policy: policy},
		&networkChecker{policy: policy},
		&pathChecker{policy: policy},
		&hostChecker{policy: policy},
		&resourceChecker{policy: policy},
	}
}

// NewScanner creates a Scanner with safety checkers drawn from the
// supplied Policy. When policy is nil, a default Policy with safe
// built-in rules is used. The Policy is retained by the Scanner and
// should not be modified concurrently after this call.
//
// Non-nil programmatic Policies are normalized and compiled at this
// boundary so that every checker receives a fully-prepared policy.
// Construction fails closed if compilation fails or no checkers are
// active.
//
// Audit logging is handled externally (typically by
// SafetyPermissionPolicy); Scan itself is a pure function that returns
// a report without side effects.
func NewScanner(policy *Policy) *Scanner {
	if policy == nil {
		policy = defaultPolicy()
	} else {
		// Non-nil programmatic Policy may not have been through
		// parsePolicy; ensure defaults and compilation run.
		policy.applyDefaults()
		if err := policy.compile(); err != nil {
			// Fail closed: a policy that can't compile its patterns
			// should not produce a scanner.
			panic("safety: NewScanner: policy compile failed: " + err.Error())
		}
	}
	s := &Scanner{policy: policy}
	s.checkers = filterCheckers(allCheckers(policy), policy)
	if len(s.checkers) == 0 {
		panic("safety: NewScanner: no active checkers after filtering")
	}
	return s
}

// defaultPolicy returns a Policy with safe defaults for all fields.
func defaultPolicy() *Policy {
	p := &Policy{}
	p.applyDefaults()
	// Best-effort compile; default patterns are well-known so this
	// should never fail.
	_ = p.compile()
	return p
}

// filterCheckers returns the subset of checkers whose Name() appears in
// policy.Checkers. When policy is nil or policy.Checkers is empty, all
// checkers are returned.
func filterCheckers(all []Checker, policy *Policy) []Checker {
	if policy == nil || len(policy.Checkers) == 0 {
		return all
	}
	enabled := make(map[string]bool, len(policy.Checkers))
	for _, name := range policy.Checkers {
		enabled[name] = true
	}
	out := make([]Checker, 0, len(all))
	for _, c := range all {
		if enabled[c.Name()] {
			out = append(out, c)
		}
	}
	return out
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
	return NewScanner(policy)
}

// ScanCtx is a convenience wrapper for Scan.
func (s *Scanner) ScanCtx(ctx context.Context, req *ScanRequest) *SafetyReport {
	return s.Scan(ctx, req)
}
