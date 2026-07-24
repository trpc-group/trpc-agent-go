//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Guard scans execution requests using an immutable policy copy.
type Guard struct {
	policy Policy
}

var scanSequence uint64

// NewGuard returns a Guard configured with a copy of policy. A zero policy
// uses DefaultPolicy.
func NewGuard(policy Policy) (*Guard, error) {
	if isZeroPolicy(policy) {
		policy = DefaultPolicy()
	}
	if err := validatePolicy(policy); err != nil {
		return nil, err
	}
	return &Guard{policy: clonePolicy(policy)}, nil
}

// Scan evaluates req and returns a complete safety report.
func (g *Guard) Scan(req Request) Report {
	started := time.Now()
	if g == nil {
		report := newReport(req)
		report.DurationMillis = time.Since(started).Milliseconds()
		return report
	}
	findings := scanExecution(g.policy, req)
	findings = append(findings, scanCodeBlocks(g.policy, req.CodeBlocks)...)
	findings = append(findings, scanRawArguments(g.policy, req.RawArguments)...)
	report := aggregateReport(req, findings)
	report.DurationMillis = time.Since(started).Milliseconds()
	redactReport(&report)
	return report
}

func scanExecution(policy Policy, req Request) []Finding {
	segments, findings := scanCommand(policy, req)
	findings = append(findings, scanPaths(policy, req.Cwd, segments)...)
	findings = append(findings, scanNetwork(policy, segments)...)
	findings = append(findings, scanResources(policy, req, segments)...)
	findings = append(findings, scanInlineInterpreters(policy, segments)...)
	findings = append(findings, scanSensitiveContent(req.Command)...)
	return findings
}

func aggregateReport(req Request, findings []Finding) Report {
	report := newReport(req)
	if len(findings) == 0 {
		return report
	}

	primary := 0
	for i := 1; i < len(findings); i++ {
		if findingRank(findings[i]) > findingRank(findings[primary]) {
			primary = i
		}
	}
	selected := findings[primary]
	if selected.Decision == DecisionAllow {
		return report
	}
	report.Decision = selected.Decision
	report.RiskLevel = selected.RiskLevel
	if !validDecision(report.Decision) {
		report.Decision = DecisionDeny
		report.RiskLevel = RiskCritical
	}
	report.RuleID = selected.RuleID
	report.Evidence = append([]string(nil), selected.Evidence...)
	report.Recommendation = selected.Recommendation
	report.Blocked = report.Decision == DecisionDeny
	report.Findings = append([]Finding(nil), findings...)
	report.SafeSummary = "request requires safety policy action"
	return report
}

func findingRank(finding Finding) int {
	return decisionRank(finding.Decision)*10 + riskRank(finding.RiskLevel)
}

func riskRank(risk RiskLevel) int {
	switch risk {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

func newReport(req Request) Report {
	return Report{
		SchemaVersion:  1,
		ScanID:         fmt.Sprintf("scan-%d", atomic.AddUint64(&scanSequence, 1)),
		Decision:       DecisionAllow,
		RiskLevel:      RiskLow,
		RuleID:         "safety.no_findings",
		Evidence:       []string{"no safety policy findings"},
		Recommendation: "execution is permitted",
		ToolName:       req.ToolName,
		Command:        req.Command,
		Backend:        req.Backend,
		SafeSummary:    "request is permitted",
	}
}

func newFinding(decision Decision, risk RiskLevel, ruleID, evidence, recommendation string) Finding {
	return Finding{
		Decision: decision, RiskLevel: risk, RuleID: ruleID,
		Evidence: []string{evidence}, Recommendation: recommendation,
	}
}

func decisionRank(decision Decision) int {
	switch decision {
	case DecisionDeny:
		return 4
	case DecisionNeedsHumanReview:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	default:
		return 5
	}
}
