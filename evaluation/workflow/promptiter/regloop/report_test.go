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
