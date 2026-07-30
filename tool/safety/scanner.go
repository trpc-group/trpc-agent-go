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
	// Name returns the checker's name for reporting (e.g. "command", "network").
	Name() string
	// Check inspects the request and returns a finding or nil.
	Check(ctx context.Context, req *ScanRequest) (*CheckResult, error)
}

// ScanRequest is the normalized input to all checkers.
// It abstracts over workspaceexec, hostexec, and codeexec backends.
type ScanRequest struct {
	// Command is the raw command or script to execute.
	Command string
	// Args are additional CLI arguments appended to Command.
	Args []string
	// Cwd is the working directory for the execution.
	Cwd string
	// Env contains environment variables (may be scrubbed by the policy layer).
	Env map[string]string
	// ToolName is the calling tool's name (e.g. "workspace_exec", "exec_command").
	ToolName string
	// Backend identifies the execution backend: "workspaceexec", "hostexec", or "codeexec".
	Backend string
	// TimeoutSec is the requested execution timeout (0 means no limit).
	TimeoutSec int
	// ConcurrentCount is the number of currently-executing tools.
	// Set by the caller before calling Scan. A non-zero value exceeding
	// the policy's MaxConcurrent triggers RESOURCE_CONCURRENCY.
	ConcurrentCount int
}

// CheckResult is returned by each checker.
// A nil CheckResult means "no issue found" (effectively allow).
type CheckResult struct {
	Decision       Decision
	RiskLevel      RiskLevel
	RuleID         string
	Evidence       string
	Recommendation string
}

// Scanner runs all registered checkers against a ScanRequest and
// produces an aggregated SafetyReport.
//
// Aggregation strategy (Deny > Ask > Allow):
//   - The first Deny finding is final; the report is immediately
//     set to denied with that finding's details.
//   - If no Deny is found but one or more Ask findings exist, the
//     report is set to ask. The finding with the highest RiskLevel
//     among the Ask results provides the details.
//   - If all checkers return nil, the report is DecisionAllow
//     with RiskLevel "none".
//
// Checker errors are logged but do not block the tool call;
// in that case the decision becomes Ask so the operator can
// decide manually.
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
// Serial execution is sufficient for the performance target of ≤1 second
// for 500-line scripts; the Checker interface supports future parallelism
// without changing the public API.
func (s *Scanner) Scan(ctx context.Context, req *ScanRequest) *SafetyReport {
	start := time.Now()
	report := &SafetyReport{
		ToolName:  req.ToolName,
		Command:   req.Command,
		Backend:   req.Backend,
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		Checkers:  make([]string, 0),
	}

	for _, c := range s.checkers {
		result, err := c.Check(ctx, req)
		if err != nil {
			// Checker errors are logged but don't block the tool call.
			// The decision degrades to Ask so the operator can decide.
			if report.Decision == DecisionAllow {
				report.Decision = DecisionAsk
				report.RiskLevel = RiskLow
			}
			report.Checkers = append(report.Checkers, c.Name()+":err:"+err.Error())
			continue
		}
		if result == nil {
			report.Checkers = append(report.Checkers, c.Name()+":pass")
			continue
		}
		report.Checkers = append(report.Checkers, c.Name()+":"+string(result.Decision))

		switch result.Decision {
		case DecisionDeny:
			// Deny is final — first denial wins, do not overwrite.
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
				// Keep the highest-risk Ask finding's details.
				// Use numeric risk level for comparison (not string).
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

// DesensitizeEvidence applies the policy's secret-detection patterns to mask
// secrets in the evidence string before it is returned to the model.
func (sc *Scanner) DesensitizeEvidence(evidence string) string {
	if evidence == "" || sc.policy == nil {
		return evidence
	}
	return Desensitize(evidence, sc.policy.SecretRegexps())
}

// NewTestScanner creates a Scanner without an audit logger for use in tests.
func NewTestScanner(policy *Policy) *Scanner {
	s := NewScanner(policy, nil)
	return s
}

// ScanCtx is a convenience wrapper that passes the context through to Scan.
func (s *Scanner) ScanCtx(ctx context.Context, req *ScanRequest) *SafetyReport {
	return s.Scan(ctx, req)
}
