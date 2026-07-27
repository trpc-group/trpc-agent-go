// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"context"
	"time"
)

// Scanner is the entry point for tool execution safety scanning.
// It orchestrates multiple Checker instances against a ScanRequest
// and produces a ScanReport with the combined findings and decision.
type Scanner struct {
	policy   *SafetyPolicy
	checkers []Checker
}

// NewScanner creates a Scanner with the given policy.
// The caller can add checkers via AddChecker after construction.
func NewScanner(policy *SafetyPolicy) *Scanner {
	return &Scanner{
		policy:   policy,
		checkers: make([]Checker, 0),
	}
}

// Add registers a Checker with the Scanner.
func (s *Scanner) Add(c Checker) {
	s.checkers = append(s.checkers, c)
}

// Scan runs all enabled checkers against the request and returns a report.
func (s *Scanner) Scan(ctx context.Context, req *ScanRequest) (*ScanReport, error) {
	start := time.Now()

	var allFindings []RiskFinding
	for _, c := range s.checkers {
		if !c.IsEnabled(s.policy) {
			continue
		}
		findings, err := c.Check(ctx, req)
		if err != nil {
			return nil, err
		}
		allFindings = append(allFindings, findings...)
	}

	riskLevel := HighestRiskLevel(allFindings)
	decision := s.decide(allFindings, riskLevel)

	return &ScanReport{
		ToolName:    req.ToolName,
		Command:     req.Command,
		Backend:     req.Backend,
		Decision:    decision,
		RiskLevel:   riskLevel,
		Findings:    allFindings,
		IsShellSafe: true,
		Duration:    time.Since(start),
		Intercepted: decision != DecisionAllow,
		Sanitized:   false,
		Timestamp:   time.Now().UTC(),
	}, nil
}

// decide converts findings and risk level into an execution decision.
//
//   - Critical and high findings always deny execution.
//   - Medium findings deny by default but ask when AskOnRiskLevel is "medium".
//   - Low findings allow.
//   - No findings allow.
func (s *Scanner) decide(findings []RiskFinding, riskLevel RiskLevel) Decision {
	if len(findings) == 0 {
		return DecisionAllow
	}
	switch riskLevel {
	case RiskLevelCritical, RiskLevelHigh:
		return DecisionDeny
	case RiskLevelMedium:
		if s.policy.DecisionPolicy != nil && s.policy.DecisionPolicy.AskOnRiskLevel == "medium" {
			return DecisionAsk
		}
		return DecisionDeny
	case RiskLevelLow:
		return DecisionAllow
	default:
		return DecisionAllow
	}
}

// Policy returns the scanner's current policy.
func (s *Scanner) Policy() *SafetyPolicy {
	return s.policy
}
