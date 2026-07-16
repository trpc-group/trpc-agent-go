//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regloop"
)

// analyzeScenario runs one scenario end to end through the real engine (fake
// mode) and returns the resulting report.
func analyzeScenario(t *testing.T, name string) *regloop.Report {
	t.Helper()
	sc, err := scenarioByName(name)
	if err != nil {
		t.Fatalf("scenario %s: %v", name, err)
	}
	cfg, err := loadLoopConfig("./data")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rt, err := buildRuntime(context.Background(), "./data", t.TempDir(), cfg.BaselineInstruction, sc)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.close()
	result, err := rt.engine.Run(context.Background(), buildRunRequest(rt.targetSurfaceID, cfg, sc))
	if err != nil {
		t.Fatalf("engine run: %v", err)
	}
	report, err := regloop.Analyze(result, regloop.Options{App: appName, Mode: "fake", Gate: resolveGate(cfg, sc)})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	return report
}

// TestResolveThresholdsUseConfigByDefault proves promptiter.json is load-bearing:
// scenarios without overrides use the config thresholds, and only overfit
// overrides them.
func TestResolveThresholdsUseConfigByDefault(t *testing.T) {
	cfg, err := loadLoopConfig("./data")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	success, _ := scenarioByName(scenarioSuccess)
	if got := resolveMinScoreGain(cfg, success); got != cfg.MinScoreGain {
		t.Fatalf("success minScoreGain=%v want config %v", got, cfg.MinScoreGain)
	}
	// The whole gate must come from the config, not just the threshold.
	got := resolveGate(cfg, success)
	want := cfg.releaseGate()
	if got.MinTotalGain != want.MinTotalGain ||
		got.AllowNewHardFail != want.AllowNewHardFail ||
		got.MaxRounds != want.MaxRounds ||
		len(got.ProtectedCaseIDs) != len(want.ProtectedCaseIDs) {
		t.Fatalf("success gate=%+v must come from config %+v", got, want)
	}
	overfit, _ := scenarioByName(scenarioOverfit)
	if got := resolveMinScoreGain(cfg, overfit); got != 0.2 {
		t.Fatalf("overfit minScoreGain=%v want override 0.2", got)
	}
	if resolveGate(cfg, overfit).MinTotalGain != 0.2 {
		t.Fatalf("overfit gate must override MinTotalGain to 0.2")
	}
}

// TestRuntimeCountsModelCallsPerRole verifies the audit call accounting: the
// worker roles are invoked and counted, and judge is never called (no llmJudge
// metric), so the report's cost data is factual.
func TestRuntimeCountsModelCallsPerRole(t *testing.T) {
	sc, err := scenarioByName(scenarioSuccess)
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	cfg, err := loadLoopConfig("./data")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rt, err := buildRuntime(context.Background(), "./data", t.TempDir(), cfg.BaselineInstruction, sc)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.close()
	if _, err := rt.engine.Run(context.Background(), buildRunRequest(rt.targetSurfaceID, cfg, sc)); err != nil {
		t.Fatalf("engine run: %v", err)
	}
	calls := rt.calls.snapshot()
	for _, role := range []string{"candidate", "backwarder", "aggregator", "optimizer"} {
		if calls[role] == 0 {
			t.Fatalf("%s calls must be counted, got %v", role, calls)
		}
	}
	if calls["judge"] != 0 {
		t.Fatalf("judge is never invoked (no llmJudge metric), got %d", calls["judge"])
	}
}

// TestScenarioSuccess: optimization fixes every case -> accepted and released.
func TestScenarioSuccess(t *testing.T) {
	report := analyzeScenario(t, scenarioSuccess)
	if !report.Gate.Released {
		t.Fatalf("success must be released, reasons=%v", report.Gate.Reasons)
	}
	if report.Delta.Summary.NewlyPassed != 3 || report.Delta.Summary.NewlyFailed != 0 {
		t.Fatalf("success delta=%+v want 3 newly passed, 0 newly failed", report.Delta.Summary)
	}
}

// TestScenarioIneffective: candidate never improves -> rejected for no gain.
func TestScenarioIneffective(t *testing.T) {
	report := analyzeScenario(t, scenarioIneffective)
	if report.Gate.Released {
		t.Fatalf("ineffective must be rejected")
	}
	if report.Candidate.ProfileAccepted {
		t.Fatalf("ineffective candidate must not be accepted by the engine")
	}
	if report.Candidate.OverallScore > report.Baseline.OverallScore {
		t.Fatalf("ineffective candidate should not improve: baseline=%.3f candidate=%.3f",
			report.Baseline.OverallScore, report.Candidate.OverallScore)
	}
}

// TestScenarioAttribution proves the live pipeline classifies more than one
// failure category (responseMismatch + toolError) and gives every failure a
// reason.
func TestScenarioAttribution(t *testing.T) {
	report := analyzeScenario(t, scenarioAttribution)
	baseline := report.FailureAttribution.Baseline
	if baseline[regloop.CategoryResponseMismatch] < 1 {
		t.Fatalf("expected a responseMismatch failure, got %v", baseline)
	}
	if baseline[regloop.CategoryToolError] < 1 {
		t.Fatalf("expected a toolError failure, got %v", baseline)
	}
	if len(baseline) < 2 {
		t.Fatalf("attribution demo must exhibit >=2 categories, got %v", baseline)
	}
	for _, detail := range report.FailureAttribution.Details {
		if detail.Reason == "" {
			t.Fatalf("failure %s/%s has no explainable reason", detail.EvalCaseID, detail.MetricName)
		}
	}
}

// TestScenarioOverfitRejected is the acceptance-critical case: training and
// overall validation improve (so the engine accepts), but one validation case
// regresses, and the harness gate MUST reject the candidate.
func TestScenarioOverfitRejected(t *testing.T) {
	report := analyzeScenario(t, scenarioOverfit)
	if !report.Candidate.ProfileAccepted {
		t.Fatalf("overfit setup expects the engine to accept on overall gain")
	}
	if report.Candidate.OverallScore <= report.Baseline.OverallScore {
		t.Fatalf("overfit setup expects overall validation to improve: baseline=%.3f candidate=%.3f",
			report.Baseline.OverallScore, report.Candidate.OverallScore)
	}
	if report.Delta.Summary.NewlyFailed < 1 {
		t.Fatalf("overfit must show at least one regressed case, delta=%+v", report.Delta.Summary)
	}
	if report.Gate.Released {
		t.Fatalf("overfit candidate MUST be rejected by the gate, reasons=%v", report.Gate.Reasons)
	}
}
