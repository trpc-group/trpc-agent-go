//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"math"
	"strings"
	"testing"
)

func TestCompareCases(t *testing.T) {
	baseline := []CaseEvaluation{
		{ID: "regular", Runs: []CaseRun{
			{Score: 0.4, Passed: true, Usage: Usage{Calls: 1, InputTokens: 10, OutputTokens: 2, CostCNY: 0.1}},
			{Score: 0.6, Passed: true},
			{Score: 0.5, Passed: true},
		}},
		{ID: "critical", Critical: true, Runs: []CaseRun{
			{Score: 1, Passed: true}, {Score: 1, Passed: true}, {Score: 1, Passed: true},
		}},
	}
	candidate := []CaseEvaluation{
		{ID: "critical", Critical: true, Runs: []CaseRun{
			{Score: 0.9, Passed: true}, {Score: 0.9, Passed: true}, {Score: 0.9, Passed: false},
		}},
		{ID: "regular", Runs: []CaseRun{
			{Score: 0.8, Passed: true, Usage: Usage{Calls: 1, InputTokens: 11, OutputTokens: 3, CostCNY: 0.2}},
			{Score: 0.8, Passed: true},
			{Score: 0.8, Passed: true, HardFailure: true},
		}},
	}

	got, err := CompareCases(baseline, candidate, 3)
	if err != nil {
		t.Fatalf("CompareCases() error = %v", err)
	}
	if len(got.Deltas) != 2 || got.Deltas[0].ID != "critical" || got.Deltas[1].ID != "regular" {
		t.Fatalf("CompareCases() did not produce sorted deltas: %+v", got.Deltas)
	}
	if !got.Deltas[0].CriticalRegression {
		t.Fatal("critical score/Pass^k regression was not detected")
	}
	if !got.Deltas[1].NewHardFailure {
		t.Fatal("new hard failure was not detected")
	}
	if got.Deltas[1].CandidatePassPowerK {
		t.Fatal("hard failure must prevent Pass^k")
	}
	if math.Abs(got.Deltas[1].ScoreDelta-0.3) > 1e-12 {
		t.Fatalf("regular score delta = %v, want 0.3", got.Deltas[1].ScoreDelta)
	}
	if got.Usage.Calls != 2 || got.Usage.Tokens() != 26 || math.Abs(got.Usage.CostCNY-0.3) > 1e-12 {
		t.Fatalf("usage = %+v, want calls=2 tokens=26 cost=0.3", got.Usage)
	}
	if got.BaselinePassPowerKRate != 1 || got.CandidatePassPowerKRate != 0 {
		t.Fatalf("Pass^k rates = (%v, %v), want (1, 0)", got.BaselinePassPowerKRate, got.CandidatePassPowerKRate)
	}
}

func TestCompareCasesRejectsInvalidInput(t *testing.T) {
	valid := []CaseEvaluation{{ID: "a", Runs: []CaseRun{{Score: 1, Passed: true}}}}
	tests := []struct {
		name      string
		baseline  []CaseEvaluation
		candidate []CaseEvaluation
		passK     int
		contains  string
	}{
		{name: "invalid k", baseline: valid, candidate: valid, passK: 0, contains: "positive"},
		{name: "empty", baseline: nil, candidate: valid, passK: 1, contains: "empty"},
		{name: "duplicate", baseline: append(valid, valid...), candidate: valid, passK: 1, contains: "duplicate"},
		{name: "missing ID", baseline: valid, candidate: []CaseEvaluation{{ID: "b", Runs: valid[0].Runs}}, passK: 1, contains: "missing"},
		{name: "too few runs", baseline: valid, candidate: valid, passK: 2, contains: "need at least"},
		{name: "non-finite score", baseline: valid, candidate: []CaseEvaluation{{ID: "a", Runs: []CaseRun{{Score: math.NaN()}}}}, passK: 1, contains: "non-finite"},
		{name: "negative usage", baseline: valid, candidate: []CaseEvaluation{{ID: "a", Runs: []CaseRun{{Usage: Usage{Calls: -1}}}}}, passK: 1, contains: "invalid usage"},
		{name: "metadata mismatch", baseline: valid, candidate: []CaseEvaluation{{ID: "a", Critical: true, Runs: valid[0].Runs}}, passK: 1, contains: "critical flag"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompareCases(test.baseline, test.candidate, test.passK)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("CompareCases() error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestCompareCasesDetectsIncreasedHardFailureCount(t *testing.T) {
	baseline := []CaseEvaluation{{ID: "safety", Runs: []CaseRun{
		{HardFailure: true}, {Passed: true}, {Passed: true},
	}}}
	candidate := []CaseEvaluation{{ID: "safety", Runs: []CaseRun{
		{HardFailure: true}, {HardFailure: true}, {Passed: true},
	}}}

	got, err := CompareCases(baseline, candidate, 3)
	if err != nil {
		t.Fatalf("CompareCases() error = %v", err)
	}
	if !got.Deltas[0].NewHardFailure {
		t.Fatal("hard-failure count increase was not detected")
	}
}
