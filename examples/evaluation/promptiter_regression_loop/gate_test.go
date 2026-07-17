//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"slices"
	"testing"
)

func TestEvaluateGateAcceptsSafeImprovement(t *testing.T) {
	comparison := Comparison{
		PassK: 3,
		Deltas: []CaseDelta{
			{ID: "a", ScoreDelta: 0.1},
			{ID: "b", ScoreDelta: 0.2, Critical: true},
			{ID: "c", ScoreDelta: 0.15},
		},
		MeanScoreGain: 0.15, BaselinePassPowerKRate: 0.5, CandidatePassPowerKRate: 0.75,
		Usage: Usage{Calls: 4, InputTokens: 60, OutputTokens: 40, CostCNY: 1.5},
	}
	config := GateConfig{
		MinScoreGain: 0.02, PassK: 3, BootstrapSeed: 42, BootstrapResamples: 1000,
		MaxCalls: 5, MaxTokens: 100, MaxCostCNY: 2,
	}

	got, err := EvaluateGate(comparison, config)
	if err != nil {
		t.Fatalf("EvaluateGate() error = %v", err)
	}
	if !got.Accepted || len(got.FailedChecks) != 0 {
		t.Fatalf("EvaluateGate() = %+v, want accepted", got)
	}
	if len(got.Checks) != 8 {
		t.Fatalf("EvaluateGate() check count = %d, want 8", len(got.Checks))
	}
}

func TestEvaluateGateReportsEveryFailure(t *testing.T) {
	comparison := Comparison{
		PassK: 3,
		Deltas: []CaseDelta{
			{ID: "a", ScoreDelta: -0.3, NewHardFailure: true},
			{ID: "b", ScoreDelta: -0.1, Critical: true, CriticalRegression: true},
		},
		MeanScoreGain: -0.2, BaselinePassPowerKRate: 1, CandidatePassPowerKRate: 0.5,
		Usage: Usage{Calls: 11, InputTokens: 80, OutputTokens: 30, CostCNY: 2.1},
	}
	config := GateConfig{
		MinScoreGain: 0.02, PassK: 3, BootstrapSeed: 42, BootstrapResamples: 200,
		MaxCalls: 10, MaxTokens: 100, MaxCostCNY: 2,
	}

	got, err := EvaluateGate(comparison, config)
	if err != nil {
		t.Fatalf("EvaluateGate() error = %v", err)
	}
	if got.Accepted {
		t.Fatal("EvaluateGate() accepted a regressing candidate")
	}
	want := []string{
		"minimum_score_gain", "no_new_hard_failure", "critical_cases_do_not_regress",
		"pass_power_k_does_not_regress", "bootstrap_ci_lower_bound", "calls_budget",
		"tokens_budget", "cost_budget_cny",
	}
	for _, name := range want {
		if !slices.Contains(got.FailedChecks, name) {
			t.Errorf("failed checks %v do not contain %q", got.FailedChecks, name)
		}
	}
}

func TestEvaluateGateAllowsDisabledBudgets(t *testing.T) {
	comparison := Comparison{
		PassK: 1, Deltas: []CaseDelta{{ID: "a", ScoreDelta: 1}}, MeanScoreGain: 1,
		BaselinePassPowerKRate: 1, CandidatePassPowerKRate: 1,
		Usage: Usage{Calls: 999, InputTokens: 999, OutputTokens: 999, CostCNY: 999},
	}
	got, err := EvaluateGate(comparison, GateConfig{PassK: 1, BootstrapResamples: 10})
	if err != nil {
		t.Fatalf("EvaluateGate() error = %v", err)
	}
	if !got.Accepted {
		t.Fatalf("EvaluateGate() rejected disabled budgets: %+v", got)
	}
}

func TestEvaluateGateRejectsInvalidConfig(t *testing.T) {
	comparison := Comparison{PassK: 3, Deltas: []CaseDelta{{ID: "a", ScoreDelta: 1}}}
	tests := []GateConfig{
		{},
		{PassK: 2},
		{PassK: 3, BootstrapResamples: -1},
		{PassK: 3, MaxCalls: -1},
	}
	for _, config := range tests {
		if _, err := EvaluateGate(comparison, config); err == nil {
			t.Errorf("EvaluateGate() accepted invalid config %+v", config)
		}
	}
}
