//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pipeline

import (
	"encoding/json"
	"strings"
	"testing"
)

func sampleReport() Report {
	baselineVal := makeResult("val",
		makeCase("a", false, 0.0, critFinalResponseText(), ""),
		makeCase("KEY", true, 1.0, critFinalResponseText(), ""),
	)
	candidateVal := makeResult("val",
		makeCase("a", true, 1.0, critFinalResponseText(), ""),
		makeCase("KEY", false, 0.0, critFinalResponseText(), ""),
	)
	baselineTrain := makeResult("train", makeCase("t", false, 0.0, critFinalResponseText(), ""))
	candidateTrain := makeResult("train", makeCase("t", true, 1.0, critFinalResponseText(), ""))
	gate := ApplyGate(GatePolicy{MinValidationGain: 0.01, KeyCaseIDs: []string{"KEY"}}, baselineVal, candidateVal, GateObservations{CandidateModelCalls: 12})
	return BuildReport(ReportInput{
		App:                  "test-app",
		ModelSource:          "fake",
		TargetSurfaceID:      "candidate#instruction",
		BaselineInstruction:  "baseline",
		CandidateInstruction: "candidate",
		EngineAccepted:       true,
		BaselineTrain:        baselineTrain,
		BaselineValidation:   baselineVal,
		CandidateTrain:       candidateTrain,
		CandidateValidation:  candidateVal,
		Gate:                 gate,
		Rounds:               []RoundSummary{{Round: 1, TrainScore: 0, ValidationScore: 0.5, Accepted: true, ScoreDelta: 0.5}},
	})
}

func TestBuildReportDecisionMirrorsGate(t *testing.T) {
	r := sampleReport()
	if r.GateAccepted {
		t.Fatalf("gate should have rejected the overfit candidate")
	}
	if r.Decision != "reject" {
		t.Errorf("Decision = %q, want reject", r.Decision)
	}
	if !r.EngineAccepted {
		t.Errorf("EngineAccepted should be true (engine accepted, gate vetoed)")
	}
}

func TestReportJSONHasRequiredKeys(t *testing.T) {
	r := sampleReport()
	raw, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	// issue #2003 requires baseline, candidate, per-case delta, gate decision, reasons, plus
	// failure-attribution statistics and a cost/latency summary in the JSON output.
	required := []string{
		"baselineValidation", "candidateValidation", "baselineTrain", "candidateTrain",
		"validationDeltas", "trainDeltas", "gate", "decision", "engineAccepted", "gateAccepted",
		"config", "determinism", "costLatency",
	}
	for _, key := range required {
		if _, ok := decoded[key]; !ok {
			t.Errorf("report JSON missing required key %q", key)
		}
	}
	// attributionStats must be present on each set report.
	var setReport map[string]json.RawMessage
	if err := json.Unmarshal(decoded["baselineValidation"], &setReport); err != nil {
		t.Fatalf("unmarshal baselineValidation: %v", err)
	}
	if _, ok := setReport["attributionStats"]; !ok {
		t.Errorf("baselineValidation missing attributionStats")
	}
	if _, ok := setReport["executionTimeMs"]; !ok {
		t.Errorf("baselineValidation missing executionTimeMs")
	}
}

func TestReportMarkdownContainsKeySections(t *testing.T) {
	md := sampleReport().Markdown()
	for _, want := range []string{
		"# PromptIter Regression-Loop Optimization Report",
		"## Decision: REJECT",
		"## Validation per-case delta",
		"## Gate criteria",
		"overfitting",
		"new_fail",
		"## Cost & latency",
		"Candidate model calls",
		"## Run configuration",
		"Determinism:",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}
