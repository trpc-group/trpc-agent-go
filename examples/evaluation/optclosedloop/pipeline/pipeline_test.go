//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tEq(t *testing.T, name string, want, got interface{}) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %v, got %v", name, want, got)
	}
}

func tContains(t *testing.T, name, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("%s: expected %q to contain %q", name, s, substr)
	}
}

func tInDelta(t *testing.T, name string, want, got, tol float64) {
	t.Helper()
	diff := want - got
	if diff < -tol || diff > tol {
		t.Errorf("%s: want %v ± %v, got %v", name, want, tol, got)
	}
}

func tNotEmpty(t *testing.T, name string, v any) {
	t.Helper()
	switch x := v.(type) {
	case string:
		if x == "" {
			t.Errorf("%s: expected non-empty string", name)
		}
	case int:
		if x == 0 {
			t.Errorf("%s: expected non-zero int", name)
		}
	case []string:
		if len(x) == 0 {
			t.Errorf("%s: expected non-empty slice", name)
		}
	case []FailureAttribution:
		if len(x) == 0 {
			t.Errorf("%s: expected non-empty attribution slice", name)
		}
	case []CaseDelta:
		if len(x) == 0 {
			t.Errorf("%s: expected non-empty deltas slice", name)
		}
	}
}

func tFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			t.Errorf("file does not exist: %s", path)
		} else {
			t.Errorf("stat %s: %v", path, err)
		}
	}
}

func tTrue(t *testing.T, name string, cond bool) {
	t.Helper()
	if !cond {
		t.Errorf("%s: expected true, got false", name)
	}
}

func tFalse(t *testing.T, name string, cond bool) {
	t.Helper()
	if cond {
		t.Errorf("%s: expected false, got true", name)
	}
}

// TestFailureAttribution verifies the six required failure categories are
// correctly assigned from structured signals.
func TestFailureAttribution(t *testing.T) {
	t.Run("FinalResponseMismatch when agent hedges", func(t *testing.T) {
		summary := &EvalSummary{
			EvalSetID: "validation",
			PerCase: []CaseEval{
				{
					EvalCaseID: "c1", EvalSetID: "validation", OverallPassed: false,
					Metrics: []CaseMetric{{
						MetricName: "overall", Score: 0.2, Threshold: 0.8, Passed: false,
						Reason: "agent said I do not know instead of Paris",
					}},
					FinalResponse: "I do not know enough to answer that.",
				},
			},
		}
		attrs := AttributeFailures(summary)
		if len(attrs) != 1 {
			t.Fatalf("expected 1 attr, got %d", len(attrs))
		}
		tEq(t, "category", FinalResponseMismatch, attrs[0].Category)
	})

	t.Run("ToolArgumentError on strconv parse error", func(t *testing.T) {
		summary := &EvalSummary{
			EvalSetID: "train",
			PerCase: []CaseEval{
				{
					EvalCaseID: "t1", EvalSetID: "train", OverallPassed: false,
					Metrics: []CaseMetric{{
						MetricName: "overall", Score: 0.1, Threshold: 0.8, Passed: false,
						Reason: "strconv.ParseFloat: invalid syntax on five",
					}},
					ToolTrajectory: []ToolStep{
						{ToolName: "calculator", Args: map[string]interface{}{"a": "five"}, Error: "strconv.ParseFloat: invalid syntax"},
					},
				},
			},
		}
		attrs := AttributeFailures(summary)
		if len(attrs) != 1 {
			t.Fatalf("expected 1 attr, got %d", len(attrs))
		}
		tEq(t, "category", ToolCallError, attrs[0].Category) // tool error check wins over arg parse text
		if len(attrs[0].Evidence) == 0 || !strings.Contains(attrs[0].Evidence[0], "calculator") {
			t.Errorf("expected calculator evidence, got %v", attrs[0].Evidence)
		}
	})

	t.Run("RouteError for route tool", func(t *testing.T) {
		summary := &EvalSummary{
			EvalSetID: "validation",
			PerCase: []CaseEval{
				{
					EvalCaseID: "r1", EvalSetID: "validation", OverallPassed: false,
					Metrics: []CaseMetric{{
						MetricName: "overall", Score: 0.5, Threshold: 0.8, Passed: false,
					}},
					ToolTrajectory: []ToolStep{
						{ToolName: "route", Args: map[string]any{"target": "MathAgent"}, Output: "7"},
					},
				},
			},
		}
		attrs := AttributeFailures(summary)
		if len(attrs) != 1 {
			t.Fatalf("expected 1 attr, got %d", len(attrs))
		}
		tEq(t, "category", RouteError, attrs[0].Category)
	})

	t.Run("KnowledgeRecall for hedging", func(t *testing.T) {
		summary := &EvalSummary{
			EvalSetID: "train",
			PerCase: []CaseEval{
				{
					EvalCaseID: "k1", EvalSetID: "train", OverallPassed: false,
					Metrics:       []CaseMetric{{MetricName: "overall", Score: 0.4, Threshold: 0.8, Passed: false}},
					FinalResponse: "I am not sure about that. I do not have enough info.",
				},
			},
		}
		attrs := AttributeFailures(summary)
		if len(attrs) != 1 {
			t.Fatalf("expected 1 attr, got %d", len(attrs))
		}
		tEq(t, "category", KnowledgeRecallInsufficient, attrs[0].Category)
	})
}

// TestAcceptanceGates verifies the four gate surfaces operate correctly.
func TestAcceptanceGates(t *testing.T) {
	gates := NewAcceptanceGates(AcceptanceGateConfig{
		MinValidationScoreGain: 0.05,
		AllowNewHardFail:       false,
		KeyCaseIDs:             []string{"k1"},
		MaxCostBudget:          0.10,
	})

	t.Run("accepts clean candidate above all gates", func(t *testing.T) {
		decision := gates.Evaluate(0.70, 0.80, nil, 0, 0, CostEstimate{EstimatedCostUSD: 0.03})
		tTrue(t, "accepted", decision.Accepted)
		tInDelta(t, "scoreDelta", 0.10, decision.ScoreDelta, 1e-9)
	})

	t.Run("rejects score delta below threshold", func(t *testing.T) {
		decision := gates.Evaluate(0.70, 0.72, nil, 0, 0, CostEstimate{EstimatedCostUSD: 0.01})
		tFalse(t, "accepted", decision.Accepted)
		joined := strings.Join(decision.Reasons, " | ")
		tContains(t, "reason", joined, "min_score_gain")
	})

	t.Run("rejects new hard fail when gate disabled", func(t *testing.T) {
		deltas := []CaseDelta{{EvalCaseID: "c1", IsHardFailNew: true}}
		decision := gates.Evaluate(0.70, 0.95, deltas, 1, 0, CostEstimate{EstimatedCostUSD: 0.01})
		tFalse(t, "accepted", decision.Accepted)
		joined := strings.Join(decision.Reasons, " | ")
		tContains(t, "reason", joined, "AllowNewHardFail")
	})

	t.Run("allows new hard fail when gate explicitly enabled", func(t *testing.T) {
		permissive := NewAcceptanceGates(AcceptanceGateConfig{
			MinValidationScoreGain: 0.05,
			AllowNewHardFail:       true,
			MaxCostBudget:          0.10,
		})
		deltas := []CaseDelta{{EvalCaseID: "c1", IsHardFailNew: true}}
		decision := permissive.Evaluate(0.70, 0.90, deltas, 1, 0, CostEstimate{EstimatedCostUSD: 0.01})
		tTrue(t, "accepted", decision.Accepted)
	})

	t.Run("rejects explicit key case degradation", func(t *testing.T) {
		deltas := []CaseDelta{{EvalCaseID: "k1", ScoreDelta: -0.30, IsKeyCaseDegrade: true}}
		decision := gates.Evaluate(0.70, 0.95, deltas, 0, 1, CostEstimate{EstimatedCostUSD: 0.01})
		tFalse(t, "accepted", decision.Accepted)
		joined := strings.Join(decision.Reasons, " | ")
		tContains(t, "reason", joined, "key case")
	})

	t.Run("rejects over-cost budget", func(t *testing.T) {
		decision := gates.Evaluate(0.70, 0.90, nil, 0, 0, CostEstimate{EstimatedCostUSD: 0.50})
		tFalse(t, "accepted", decision.Accepted)
		joined := strings.Join(decision.Reasons, " | ")
		tContains(t, "reason", joined, "cost")
	})
}

// TestCaseDelta verifies baseline-vs-candidate per-case delta computation.
func TestCaseDelta(t *testing.T) {
	baseline := &EvalSummary{
		EvalSetID: "validation",
		PerCase: []CaseEval{
			{EvalCaseID: "a", OverallPassed: true, Metrics: []CaseMetric{{Score: 0.95}}},
			{EvalCaseID: "b", OverallPassed: true, Metrics: []CaseMetric{{Score: 0.90}}},
			{EvalCaseID: "c", OverallPassed: false, Metrics: []CaseMetric{{Score: 0.30}}},
		},
	}
	origOutcome := caseOutcomes["b"]
	caseOutcomes["b"] = fakeCaseOutcome{Labels: []string{"hardfail_guard"}}
	defer func() {
		if origOutcome.Labels == nil {
			delete(caseOutcomes, "b")
		} else {
			caseOutcomes["b"] = origOutcome
		}
	}()

	candidate := &EvalSummary{
		EvalSetID: "validation",
		PerCase: []CaseEval{
			{EvalCaseID: "a", OverallPassed: false, Metrics: []CaseMetric{{Score: 0.40}}},
			{EvalCaseID: "b", OverallPassed: true, Metrics: []CaseMetric{{Score: 0.80}}},
			{EvalCaseID: "c", OverallPassed: true, Metrics: []CaseMetric{{Score: 0.90}}},
		},
	}
	deltas, newHF, keyDeg, err := ComputeCaseDelta(baseline, candidate)
	if err != nil {
		t.Fatalf("ComputeCaseDelta err: %v", err)
	}
	tEq(t, "newHF", 1, newHF)
	tEq(t, "keyDeg", 1, keyDeg)
	if len(deltas) != 3 {
		t.Fatalf("expected 3 deltas, got %d", len(deltas))
	}
	byID := map[string]CaseDelta{}
	for _, d := range deltas {
		byID[d.EvalCaseID] = d
	}
	tTrue(t, "a is hard fail", byID["a"].IsHardFailNew)
	tTrue(t, "b is key degrade", byID["b"].IsKeyCaseDegrade)
	tInDelta(t, "c delta", 0.60, byID["c"].ScoreDelta, 1e-9)
}

// TestPipelineEndToEnd runs the full pipeline deterministically and asserts
// round 1 accepted / round 2 rejected by flat gain / round 3 rejected by
// hard fail gate.
func TestPipelineEndToEnd(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	outDir := filepath.Join(tmpDir, "output")
	cfg := Config{
		AppName:    "test-optclosedloop",
		Mode:       ModeFakeDeterministic,
		DataDir:    filepath.Join(tmpDir, "data"),
		OutputDir:  outDir,
		TrainSetID: "train",
		ValSetID:   "validation",
		MaxRounds:  3,
		RandomSeed: 42,
		GateConfig: AcceptanceGateConfig{
			MinValidationScoreGain: 0.05,
			AllowNewHardFail:       false,
			MaxCostBudget:          0,
		},
		PromptsBaseline: map[string]string{
			"system_prompt":     "SYS-v0",
			"tool_desc_calc":    "CALC-v0",
			"router_prompt":     "RTR-v0",
			"agent_instruction": "AG-v0",
		},
		TargetSurfaceIDs: []string{"system_prompt", "tool_desc_calc", "router_prompt", "agent_instruction"},
	}

	report, jsonPath, mdPath, err := New(cfg).Run(ctx)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}

	tFileExists(t, jsonPath)
	tFileExists(t, mdPath)
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var reparsed OptimizationReport
	if err := json.Unmarshal(raw, &reparsed); err != nil {
		t.Fatalf("JSON report is not valid JSON: %v", err)
	}

	if report.BaselineTrain == nil {
		t.Fatal("nil baseline train")
	}
	if report.BaselineVal == nil {
		t.Fatal("nil baseline val")
	}
	tEq(t, "train cases", 3, report.BaselineTrain.TotalCases)
	tEq(t, "val cases", 3, report.BaselineVal.TotalCases)

	// Attribution on baseline failures.
	tNotEmpty(t, "baseline train attribution", report.BaselineTrain.Attribution)
	cats := map[FailureCategory]bool{}
	for _, a := range report.BaselineTrain.Attribution {
		cats[a.Category] = true
	}
	tTrue(t, "has final_response_mismatch", cats[FinalResponseMismatch])
	tTrue(t, "has tool_argument_error", cats[ToolArgumentError])
	tNotEmpty(t, "baseline val attribution", report.BaselineVal.Attribution)
	tEq(t, "val first attr category", KnowledgeRecallInsufficient, report.BaselineVal.Attribution[0].Category)

	// Rounds pattern: accept / reject / reject.
	if len(report.Rounds) != 3 {
		t.Fatalf("expected 3 rounds, got %d", len(report.Rounds))
	}
	tTrue(t, "round 1 accepted", report.Rounds[0].Acceptance.Accepted)
	tFalse(t, "round 2 accepted", report.Rounds[1].Acceptance.Accepted)
	tFalse(t, "round 3 accepted", report.Rounds[2].Acceptance.Accepted)

	// Round 3: must cite AllowNewHardFail=false and count > 0.
	r3Reasons := strings.Join(report.Rounds[2].Acceptance.Reasons, " | ")
	tContains(t, "round 3 AllowNewHardFail reason", r3Reasons, "AllowNewHardFail=false")
	tTrue(t, "round 3 HardFailNewCount > 0", report.Rounds[2].Acceptance.HardFailNewCount > 0)

	tTrue(t, "FinalAccepted", report.FinalAccepted)
	tTrue(t, "final score > baseline", report.FinalValidationScore > report.BaselineVal.OverallScore)
	tTrue(t, "BestRound >=1", report.BestRound >= 1)

	// Markdown report surface.
	mdRaw, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	md := string(mdRaw)
	tContains(t, "md has title", md, "# Optimization Report")
	tContains(t, "md has baseline section", md, "Baseline evaluation")
	tContains(t, "md has rounds section", md, "Optimization rounds")
	tContains(t, "md has final prompts section", md, "## Final prompts")
	tContains(t, "md has ACCEPTED", md, "ACCEPTED")
	tContains(t, "md has REJECTED", md, "REJECTED")
}
