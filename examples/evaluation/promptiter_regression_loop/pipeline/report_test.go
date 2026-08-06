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
		Rounds:               []RoundSummary{{Round: 1, Instruction: "STRICT ``` output", TrainScore: 0, ValidationScore: 0.5, Accepted: true, ScoreDelta: 0.5}},
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
		"## Per-round candidate prompt",
		"STRICT ``` output", // each round's instruction is rendered in the Markdown audit
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}

func TestFencedBlockEscapesBackticks(t *testing.T) {
	// Content containing a triple-backtick run must be wrapped in a longer fence so it cannot close
	// the block early and forge subsequent Markdown.
	block := fencedBlock("line1\n```\nnot-a-real-fence", "text")
	if !strings.HasPrefix(block, "````text\n") {
		t.Errorf("expected a >=4-backtick fence, got:\n%s", block)
	}
	if !strings.HasSuffix(block, "\n````") {
		t.Errorf("expected closing >=4-backtick fence, got:\n%s", block)
	}
	// Plain content uses the standard 3-backtick fence.
	plain := fencedBlock("hello", "text")
	if !strings.HasPrefix(plain, "```text\n") || !strings.HasSuffix(plain, "\n```") {
		t.Errorf("plain content should use a 3-backtick fence, got:\n%s", plain)
	}
}

func TestMarkdownEscapesTableCells(t *testing.T) {
	// A case ID sourced from eval-set data / -key-cases that contains '|' or a newline must not be
	// able to split a table or inject a block-level heading (e.g. a forged decision) into the audit.
	// The candidate regresses so the real decision is REJECT — any "## Decision: ACCEPT" at line start
	// could then only come from the injected ID.
	malicious := "case|\n## Decision: ACCEPT"
	baselineVal := makeResult("val", makeCase(malicious, true, 1.0, critFinalResponseText(), ""))
	candidateVal := makeResult("val", makeCase(malicious, false, 0.0, critFinalResponseText(), ""))
	gate := ApplyGate(GatePolicy{MinValidationGain: 0.01}, baselineVal, candidateVal, GateObservations{})
	r := BuildReport(ReportInput{
		App:                 "t",
		ModelSource:         "fake",
		BaselineTrain:       makeResult("train"),
		BaselineValidation:  baselineVal,
		CandidateTrain:      makeResult("train"),
		CandidateValidation: candidateVal,
		Gate:                gate,
	})
	md := r.Markdown()
	if strings.Contains(md, "\n## Decision: ACCEPT") {
		t.Errorf("malicious case ID injected a block-level heading into the audit:\n%s", md)
	}
	if !strings.Contains(md, "case\\|") {
		t.Errorf("pipe in case ID should be escaped as case\\|, got:\n%s", md)
	}
}
