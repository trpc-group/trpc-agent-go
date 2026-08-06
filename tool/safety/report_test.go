//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

func TestAggregatePrecedence(t *testing.T) {
	r := ScanReport{Findings: []Finding{
		{RuleID: "a", Decision: DecisionAsk, RiskLevel: RiskLow},
		{RuleID: "b", Decision: DecisionDeny, RiskLevel: RiskCritical},
		{RuleID: "c", Decision: DecisionAllow, RiskLevel: RiskNone},
	}}
	r.aggregate()
	if r.Decision != DecisionDeny {
		t.Errorf("decision=%s want deny", r.Decision)
	}
	if r.RiskLevel != RiskCritical {
		t.Errorf("risk=%s want critical", r.RiskLevel)
	}
	if !r.Blocked {
		t.Errorf("expected Blocked=true")
	}
	if r.PrimaryRuleID() != "b" {
		t.Errorf("primary=%s want b (most restrictive first)", r.PrimaryRuleID())
	}
}

func TestAggregateAllAllow(t *testing.T) {
	r := ScanReport{Findings: []Finding{
		{RuleID: "net.allowed_domain", Decision: DecisionAllow, RiskLevel: RiskLow},
	}}
	r.aggregate()
	if r.Decision != DecisionAllow || r.Blocked {
		t.Errorf("expected allow/not-blocked, got %s blocked=%v", r.Decision, r.Blocked)
	}
	if r.Reason() != "" {
		t.Errorf("allow report should have empty reason, got %q", r.Reason())
	}
}

func TestAggregateEmpty(t *testing.T) {
	r := ScanReport{}
	r.aggregate()
	if r.Decision != DecisionAllow || r.RiskLevel != RiskNone {
		t.Errorf("empty report should be allow/none, got %s/%s", r.Decision, r.RiskLevel)
	}
}

func TestReasonNonEmptyOnBlock(t *testing.T) {
	r := ScanReport{Findings: []Finding{
		{RuleID: "cmd.dangerous_delete", Decision: DecisionDeny, RiskLevel: RiskCritical, Recommendation: "blocked"},
	}}
	r.aggregate()
	if r.Reason() == "" {
		t.Errorf("blocking report should have a non-empty reason")
	}
}
