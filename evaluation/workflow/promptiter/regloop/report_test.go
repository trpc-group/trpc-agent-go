//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// acceptedRunFixture mirrors the fake example: baseline fails, round 1 passes and
// is accepted.
func acceptedRunFixture() *engine.RunResult {
	baseline := evalR(0.0, caseR("c1", metricR("final_response_avg_score", 0.0, status.EvalStatusFailed, "text mismatch")))
	candidate := evalR(1.0, caseR("c1", metricR("final_response_avg_score", 1.0, status.EvalStatusPassed, "")))
	return &engine.RunResult{
		Status:             engine.RunStatusSucceeded,
		BaselineValidation: baseline,
		Rounds:             []engine.RoundResult{lossRound(1, candidate, true, 1.0, promptiter.LossSeverityP1)},
	}
}

func TestAnalyzeAcceptedRun(t *testing.T) {
	result := acceptedRunFixture()
	report, err := Analyze(result, Options{
		App:  "eval-optimization-app",
		Mode: "fake",
		Gate: ReleaseGate{MinTotalGain: 0.5, MaxRounds: 4},
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.Baseline.OverallScore != 0.0 || report.Candidate.OverallScore != 1.0 {
		t.Fatalf("scores baseline=%.2f candidate=%.2f", report.Baseline.OverallScore, report.Candidate.OverallScore)
	}
	if !report.Candidate.ProfileAccepted || report.Candidate.AcceptedRound != 1 {
		t.Fatalf("candidate not accepted at round 1: %+v", report.Candidate)
	}
	if report.Delta.Summary.NewlyPassed != 1 {
		t.Fatalf("newlyPassed=%d want 1", report.Delta.Summary.NewlyPassed)
	}
	if report.FailureAttribution.Baseline[CategoryResponseMismatch] != 1 {
		t.Fatalf("baseline responseMismatch=%d want 1", report.FailureAttribution.Baseline[CategoryResponseMismatch])
	}
	if report.FailureAttribution.BySeverity["P1"] != 1 {
		t.Fatalf("severity P1=%d want 1", report.FailureAttribution.BySeverity["P1"])
	}
	if !report.Gate.Released {
		t.Fatalf("gate not released: %v", report.Gate.Reasons)
	}
	if report.Cost.Rounds != 1 || !report.Cost.Estimated {
		t.Fatalf("cost=%+v", report.Cost)
	}
}

func TestAnalyzeFailsClosedOnNonSucceededStatus(t *testing.T) {
	// A still-running (or failed/canceled) run may already carry an accepted
	// round; it must not be reported as released.
	for _, st := range []engine.RunStatus{engine.RunStatusRunning, engine.RunStatusFailed, engine.RunStatusCanceled} {
		result := acceptedRunFixture()
		result.Status = st
		report, err := Analyze(result, Options{Gate: ReleaseGate{MinTotalGain: 0.5, MaxRounds: 4}})
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		if report.Gate.Released {
			t.Fatalf("status %q must not release, reasons=%v", st, report.Gate.Reasons)
		}
	}
}

func TestAnalyzeFailsClosedOnSlimmedResult(t *testing.T) {
	// A slimmed RunResult keeps aggregate scores but omits per-case data, so
	// regressions cannot be verified; the gate must fail closed rather than
	// release on aggregate gain alone.
	slim := func(score float64) *engine.EvaluationResult {
		return &engine.EvaluationResult{
			OverallScore: score,
			EvalSets:     []engine.EvalSetResult{{EvalSetID: "validation", OverallScore: score}},
		}
	}
	result := &engine.RunResult{
		Status:             engine.RunStatusSucceeded,
		BaselineValidation: slim(0.0),
		Rounds:             []engine.RoundResult{lossRound(1, slim(1.0), true, 1.0)},
	}
	report, err := Analyze(result, Options{Gate: ReleaseGate{MinTotalGain: 0.5, MaxRounds: 4}})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.Gate.Released {
		t.Fatalf("slimmed result must not release, reasons=%v", report.Gate.Reasons)
	}
}

func TestRoundReportDeltaUsesLastAcceptedBaseline(t *testing.T) {
	// round1 improves c1 and is accepted; round2 and round3 are both rejected.
	// The comparison baseline must stay at round1 (last accepted) across both
	// rejected rounds — never advancing to a rejected round's validation.
	baseline := evalR(0.0, caseR("c1", metricR("m", 0, status.EvalStatusFailed, "")))
	v1 := evalR(1.0, caseR("c1", metricR("m", 1, status.EvalStatusPassed, ""))) // accepted
	v2 := evalR(0.0, caseR("c1", metricR("m", 0, status.EvalStatusFailed, ""))) // rejected, c1 regressed
	v3 := evalR(1.0, caseR("c1", metricR("m", 1, status.EvalStatusPassed, ""))) // rejected, c1 back to pass
	result := &engine.RunResult{
		Status:             engine.RunStatusSucceeded,
		BaselineValidation: baseline,
		Rounds: []engine.RoundResult{
			lossRound(1, v1, true, 1.0),
			lossRound(2, v2, false, -1.0),
			lossRound(3, v3, false, 0.0),
		},
	}
	report, err := Analyze(result, Options{Gate: ReleaseGate{MinTotalGain: 0.5}})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.Rounds[0].Delta.Summary.NewlyPassed != 1 {
		t.Fatalf("round1 delta want NewlyPassed=1, got %+v", report.Rounds[0].Delta.Summary)
	}
	if report.Rounds[1].Delta.Summary.NewlyFailed != 1 {
		t.Fatalf("round2 delta must compare vs last accepted round1, want NewlyFailed=1, got %+v", report.Rounds[1].Delta.Summary)
	}
	// round3 (c1 passes) vs last accepted round1 (c1 passes) = unchanged. If the
	// baseline had wrongly advanced to the rejected round2 (c1 fails), this would
	// show NewlyPassed=1 instead.
	if report.Rounds[2].Delta.Summary.NewlyPassed != 0 {
		t.Fatalf("round3 baseline must still be round1 (rejected round2 must not advance it), got %+v", report.Rounds[2].Delta.Summary)
	}
	if len(report.Rounds[1].Validation.EvalSets) == 0 {
		t.Fatalf("round validation per-case must be present")
	}
}

func TestAnalyzeBudgetFailsClosedWithoutCost(t *testing.T) {
	// Budget configured but the caller provided no model-call count: fail closed.
	report, err := Analyze(acceptedRunFixture(), Options{Gate: ReleaseGate{MinTotalGain: 0.5, MaxModelCalls: 100}})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.Gate.Released {
		t.Fatalf("budget + unknown calls must fail closed, reasons=%v", report.Gate.Reasons)
	}
}

func TestAnalyzeBudgetWithKnownCalls(t *testing.T) {
	report, err := Analyze(acceptedRunFixture(), Options{
		Gate: ReleaseGate{MinTotalGain: 0.5, MaxModelCalls: 100},
		Cost: CostInput{ModelCalls: map[string]int{"candidate": 5}},
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !report.Gate.Released {
		t.Fatalf("known calls under budget must release, reasons=%v", report.Gate.Reasons)
	}
}

func TestAnalyzeNil(t *testing.T) {
	if _, err := Analyze(nil, Options{}); err == nil {
		t.Fatalf("expected error for nil result")
	}
}

func TestReportJSONRoundTrips(t *testing.T) {
	report, err := Analyze(acceptedRunFixture(), Options{App: "app", Mode: "fake", Gate: ReleaseGate{MinTotalGain: 0.5}})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	payload, err := report.JSON()
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var round Report
	if err := json.Unmarshal(payload, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.Candidate.OverallScore != 1.0 {
		t.Fatalf("round-trip candidate score=%.2f", round.Candidate.OverallScore)
	}
}

func TestReportMarkdownAndWriteFiles(t *testing.T) {
	report, err := Analyze(acceptedRunFixture(), Options{App: "app", Mode: "fake", Gate: ReleaseGate{MinTotalGain: 0.5}})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	md := report.Markdown()
	if !strings.Contains(md, "RELEASED") {
		t.Fatalf("markdown missing verdict:\n%s", md)
	}
	dir := t.TempDir()
	if err := WriteFiles(dir, report); err != nil {
		t.Fatalf("write files: %v", err)
	}
	for _, name := range []string{"optimization_report.json", "optimization_report.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}
