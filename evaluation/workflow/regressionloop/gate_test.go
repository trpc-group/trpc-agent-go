//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regressionloop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Gate tests
// ---------------------------------------------------------------------------

func TestEvaluateGate_Accept_ScoreGain(t *testing.T) {
	cfg := GateConfig{
		MinScoreGain:      0.05,
		NoNewHardFailures: false,
		OverfitThreshold:  0.05,
	}
	baseline := EvalRunSummary{OverallScore: 0.70, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	candidate := EvalRunSummary{OverallScore: 0.80, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}

	decision := EvaluateGate(cfg, baseline, candidate, nil, nil)
	if !decision.Accepted {
		t.Errorf("expected accepted, got rejected: %s", decision.Summary)
	}
	if decision.ScoreDelta() < 0.05 {
		t.Errorf("expected score delta >= 0.05, got %f", decision.ScoreDelta())
	}
}

func TestEvaluateGate_Reject_InsufficientGain(t *testing.T) {
	cfg := GateConfig{
		MinScoreGain:      0.05,
		NoNewHardFailures: false,
		OverfitThreshold:  0.05,
	}
	baseline := EvalRunSummary{OverallScore: 0.70, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	candidate := EvalRunSummary{OverallScore: 0.72, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}

	decision := EvaluateGate(cfg, baseline, candidate, nil, nil)
	if decision.Accepted {
		t.Error("expected rejected due to insufficient gain")
	}
}

func TestEvaluateGate_Reject_NewHardFailure(t *testing.T) {
	cfg := GateConfig{
		MinScoreGain:      0.01,
		NoNewHardFailures: true,
		OverfitThreshold:  0.05,
	}
	baseline := EvalRunSummary{
		OverallScore: 0.70,
		CaseScores:   map[string]float64{"c1": 0.8, "c2": 0.6},
		CaseStatuses: map[string]string{"c1": "passed", "c2": "passed"},
	}
	candidate := EvalRunSummary{
		OverallScore: 0.80,
		CaseScores:   map[string]float64{"c1": 0.9, "c2": 0.3},
		CaseStatuses: map[string]string{"c1": "passed", "c2": "failed"},
	}

	decision := EvaluateGate(cfg, baseline, candidate, nil, nil)
	if decision.Accepted {
		t.Error("expected rejected due to new hard failure")
	}
	hasNewFailRule := false
	for _, r := range decision.Reasons {
		if r.Rule == "no_new_hard_failures" && !r.Passed {
			hasNewFailRule = true
		}
	}
	if !hasNewFailRule {
		t.Error("expected no_new_hard_failures rule to fail")
	}
}

func TestEvaluateGate_Reject_CriticalCaseRegression(t *testing.T) {
	cfg := GateConfig{
		MinScoreGain:      0.01,
		NoNewHardFailures: false,
		CriticalCaseIDs:   []string{"critical-1"},
		OverfitThreshold:  0.05,
	}
	baseline := EvalRunSummary{
		OverallScore: 0.70,
		CaseScores:   map[string]float64{"critical-1": 0.9, "c2": 0.5},
		CaseStatuses: map[string]string{"critical-1": "passed", "c2": "failed"},
	}
	candidate := EvalRunSummary{
		OverallScore: 0.75,
		CaseScores:   map[string]float64{"critical-1": 0.7, "c2": 0.8},
		CaseStatuses: map[string]string{"critical-1": "passed", "c2": "passed"},
	}

	decision := EvaluateGate(cfg, baseline, candidate, nil, nil)
	if decision.Accepted {
		t.Error("expected rejected due to critical case regression")
	}
}

func TestEvaluateGate_Reject_CostBudget(t *testing.T) {
	cfg := GateConfig{
		MinScoreGain:      0.01,
		NoNewHardFailures: false,
		OverfitThreshold:  0.05,
		MaxCostBudget:     1.0,
	}
	baseline := EvalRunSummary{OverallScore: 0.70, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	candidate := EvalRunSummary{OverallScore: 0.80, TotalCost: 1.5, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}

	decision := EvaluateGate(cfg, baseline, candidate, nil, nil)
	if decision.Accepted {
		t.Error("expected rejected due to cost budget exceeded")
	}
}

// ---------------------------------------------------------------------------
// Overfitting detection tests
// ---------------------------------------------------------------------------

func TestEvaluateGate_OverfittingDetected(t *testing.T) {
	cfg := GateConfig{
		MinScoreGain:      0.0,
		NoNewHardFailures: false,
		OverfitThreshold:  0.05,
	}

	// Train improves significantly, validation degrades.
	trainBaseline := &EvalRunSummary{OverallScore: 0.60, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	trainCandidate := &EvalRunSummary{OverallScore: 0.85, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	valBaseline := EvalRunSummary{OverallScore: 0.70, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	valCandidate := EvalRunSummary{OverallScore: 0.65, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}

	decision := EvaluateGate(cfg, valBaseline, valCandidate, trainBaseline, trainCandidate)
	if !decision.OverfittingDetected {
		t.Error("expected overfitting to be detected")
	}
	if decision.Accepted {
		t.Error("expected rejection when overfitting detected")
	}
}

func TestEvaluateGate_NoOverfitting_TrainAndValBothImprove(t *testing.T) {
	cfg := GateConfig{
		MinScoreGain:      0.01,
		NoNewHardFailures: false,
		OverfitThreshold:  0.05,
	}

	trainBaseline := &EvalRunSummary{OverallScore: 0.60, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	trainCandidate := &EvalRunSummary{OverallScore: 0.80, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	valBaseline := EvalRunSummary{OverallScore: 0.70, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}
	valCandidate := EvalRunSummary{OverallScore: 0.75, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}}

	decision := EvaluateGate(cfg, valBaseline, valCandidate, trainBaseline, trainCandidate)
	if decision.OverfittingDetected {
		t.Error("should not detect overfitting when both train and val improve")
	}
	if !decision.Accepted {
		t.Errorf("expected accepted, got: %s", decision.Summary)
	}
}

// ---------------------------------------------------------------------------
// CaseDelta tests
// ---------------------------------------------------------------------------

func TestComputeCaseDeltas(t *testing.T) {
	baseline := EvalRunSummary{
		CaseScores:   map[string]float64{"c1": 0.8, "c2": 0.3, "c3": 0.9},
		CaseStatuses: map[string]string{"c1": "passed", "c2": "failed", "c3": "passed"},
	}
	candidate := EvalRunSummary{
		CaseScores:   map[string]float64{"c1": 0.6, "c2": 0.7, "c3": 0.95},
		CaseStatuses: map[string]string{"c1": "passed", "c2": "passed", "c3": "passed"},
	}

	deltas := ComputeCaseDeltas(baseline, candidate)

	deltaMap := make(map[string]CaseDelta)
	for _, d := range deltas {
		deltaMap[d.EvalCaseID] = d
	}

	// c1: passed->passed, score decreased → regression in score (but still passed).
	if d, ok := deltaMap["c1"]; !ok {
		t.Error("missing c1")
	} else {
		if d.IsNewFailure {
			t.Error("c1 should not be a new failure")
		}
	}

	// c2: failed->passed → new pass.
	if d, ok := deltaMap["c2"]; !ok {
		t.Error("missing c2")
	} else if !d.IsNewPass {
		t.Error("c2 should be a new pass")
	}

	// c3: passed->passed, score increased → improvement.
	if d, ok := deltaMap["c3"]; !ok {
		t.Error("missing c3")
	} else if !d.IsImprovement {
		t.Error("c3 should be an improvement")
	}
}

func TestComputeCaseDeltas_NewFailure(t *testing.T) {
	baseline := EvalRunSummary{
		CaseScores:   map[string]float64{"c1": 0.9},
		CaseStatuses: map[string]string{"c1": "passed"},
	}
	candidate := EvalRunSummary{
		CaseScores:   map[string]float64{"c1": 0.2},
		CaseStatuses: map[string]string{"c1": "failed"},
	}
	deltas := ComputeCaseDeltas(baseline, candidate)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	if !deltas[0].IsNewFailure {
		t.Error("expected new failure")
	}
}

// ---------------------------------------------------------------------------
// Report generation tests
// ---------------------------------------------------------------------------

func TestWriteJSONReport(t *testing.T) {
	report := &OptimizationReport{
		Baseline:  &RoundSummary{OverallScore: 0.7, PassCount: 5, FailCount: 1, TotalCases: 6},
		Candidate: &RoundSummary{OverallScore: 0.85, PassCount: 6, FailCount: 0, TotalCases: 6},
		GateDecision: &GateDecision{
			Accepted: true,
			Summary:  "accepted: score improved by 0.15",
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	err := WriteJSONReport(path, report)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}

	var decoded OptimizationReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if decoded.Baseline.OverallScore != 0.7 {
		t.Errorf("baseline score mismatch: %f", decoded.Baseline.OverallScore)
	}
	if !decoded.GateDecision.Accepted {
		t.Error("expected accepted in decoded report")
	}
}

func TestWriteMarkdownReport(t *testing.T) {
	report := &OptimizationReport{
		Baseline:  &RoundSummary{OverallScore: 0.7, PassCount: 5, FailCount: 1, TotalCases: 6},
		Candidate: &RoundSummary{OverallScore: 0.85, PassCount: 6, FailCount: 0, TotalCases: 6},
		GateDecision: &GateDecision{
			Accepted: true,
			Summary:  "accepted: score improved by 0.15",
			Reasons: []GateRuleResult{
				{Rule: "min_score_gain", Passed: true, Detail: "score delta 0.15 (threshold 0.01)"},
				{Rule: "no_overfitting", Passed: true, Detail: "no overfitting"},
			},
		},
		FailureAttributionSummary: []AttributionSummary{
			{Category: FailureToolCallError, Count: 2, Cases: []string{"s1/c1", "s1/c2"}},
		},
		CaseDeltas: []CaseDelta{
			{EvalCaseID: "c1", BaselineScore: 0.5, CandidateScore: 0.8, ScoreDelta: 0.3, IsImprovement: true},
			{EvalCaseID: "c2", BaselineScore: 0.3, CandidateScore: 0.9, ScoreDelta: 0.6, IsNewPass: true},
		},
		CostSummary: CostSummary{TotalTokens: 5000, EstimatedCost: 0.05, RoundsRun: 2},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	err := WriteMarkdownReport(path, report)
	if err != nil {
		t.Fatalf("write markdown report: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	content := string(data)

	// Check key sections are present.
	for _, section := range []string{
		"# Optimization Audit Report",
		"## Score Summary",
		"## Gate Decision",
		"## Failure Attribution",
		"## Per-Case Deltas",
		"## Cost & Latency",
		"## Recommendation",
		"ACCEPTED",
	} {
		if !contains(content, section) {
			t.Errorf("markdown report missing section %q", section)
		}
	}
}

// ScoreDelta is a helper method on GateDecision for testing.
func (d *GateDecision) ScoreDelta() float64 {
	for _, r := range d.Reasons {
		if r.Rule == "min_score_gain" {
			// Parse the delta from the detail string.
			var delta float64
			fmt.Sscanf(r.Detail, "score delta %f", &delta)
			return delta
		}
	}
	return 0
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
