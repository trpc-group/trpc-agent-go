//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"
	"testing"
)

func TestDecideGate(t *testing.T) {
	baseConfig := gateConfig{
		MinValidationScoreGain: 0.05,
		AllowNewHardFails:      false,
		CriticalCaseIDs:        []string{"critical"},
		MaxCriticalScoreDrop:   0,
		MaxEstimatedCostUSD:    0.02,
		MaxToolCalls:           2,
	}
	baseline := evaluationSummary{
		Score: 0.60,
		Cases: []caseEvaluation{
			{CaseID: "regular", Score: 0.50, Passed: false},
			{CaseID: "critical", Score: 1.00, Passed: true},
		},
	}

	tests := []struct {
		name          string
		candidate     evaluationSummary
		wantAccepted  bool
		reasonContain string
	}{
		{
			name: "accept",
			candidate: evaluationSummary{
				Score: 0.75,
				Cases: []caseEvaluation{
					{CaseID: "regular", Score: 0.75, Passed: false},
					{CaseID: "critical", Score: 1.00, Passed: true},
				},
				Cost: costSummary{EstimatedCostUSD: 0.01, ToolCalls: 1},
			},
			wantAccepted: true,
		},
		{
			name: "insufficient gain",
			candidate: evaluationSummary{
				Score: 0.64,
				Cases: baseline.Cases,
				Cost:  costSummary{EstimatedCostUSD: 0.01, ToolCalls: 1},
			},
			reasonContain: "score gain",
		},
		{
			name: "new hard fail",
			candidate: evaluationSummary{
				Score: 0.80,
				Cases: []caseEvaluation{
					{CaseID: "regular", Score: 1.00, Passed: true},
					{CaseID: "critical", Score: 0.50, Passed: false},
				},
				Cost: costSummary{EstimatedCostUSD: 0.01, ToolCalls: 1},
			},
			reasonContain: "hard fail",
		},
		{
			name: "critical regression",
			candidate: evaluationSummary{
				Score: 0.80,
				Cases: []caseEvaluation{
					{CaseID: "regular", Score: 1.00, Passed: true},
					{CaseID: "critical", Score: 0.90, Passed: true},
				},
				Cost: costSummary{EstimatedCostUSD: 0.01, ToolCalls: 1},
			},
			reasonContain: "critical case",
		},
		{
			name: "cost budget",
			candidate: evaluationSummary{
				Score: 0.80,
				Cases: baseline.Cases,
				Cost:  costSummary{EstimatedCostUSD: 0.03, ToolCalls: 1},
			},
			reasonContain: "cost budget",
		},
		{
			name: "tool budget",
			candidate: evaluationSummary{
				Score: 0.80,
				Cases: baseline.Cases,
				Cost:  costSummary{EstimatedCostUSD: 0.01, ToolCalls: 3},
			},
			reasonContain: "tool-call budget",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delta, err := compareEvaluations(baseline, test.candidate)
			if err != nil {
				t.Fatalf("compareEvaluations returned error: %v", err)
			}
			decision := decideGate(baseConfig, baseline, test.candidate, delta)
			if decision.Accepted != test.wantAccepted {
				t.Fatalf("accepted = %t, want %t; reasons: %v", decision.Accepted, test.wantAccepted, decision.Reasons)
			}
			if test.reasonContain != "" && !containsReason(decision.Reasons, test.reasonContain) {
				t.Fatalf("reasons %v do not contain %q", decision.Reasons, test.reasonContain)
			}
		})
	}
}

func containsReason(reasons []string, part string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, part) {
			return true
		}
	}
	return false
}
