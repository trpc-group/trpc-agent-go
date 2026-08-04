// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReportKeepsLastAcceptedCandidate(t *testing.T) {
	report := newTestReport(t)
	accepted := testRound(t, 1, "candidate ``` one", true)
	if err := AppendRound(report, accepted); err != nil {
		t.Fatalf("AppendRound(accepted) error = %v", err)
	}
	for attempt := 2; attempt <= 3; attempt++ {
		if err := AppendRound(report, testRound(t, attempt, "rejected", false)); err != nil {
			t.Fatalf("AppendRound(rejected %d) error = %v", attempt, err)
		}
	}
	if err := FinalizeReport(report, nil); err != nil {
		t.Fatalf("FinalizeReport() error = %v", err)
	}
	if report.SelectedAttempt != 1 || report.SelectedCandidate == nil || report.SelectedCandidate.Text != accepted.CandidatePrompt.Text {
		t.Fatalf("selected candidate = attempt %d %+v, want attempt 1", report.SelectedAttempt, report.SelectedCandidate)
	}
	var markdown bytes.Buffer
	if err := WriteMarkdown(&markdown, report); err != nil {
		t.Fatalf("WriteMarkdown() error = %v", err)
	}
	if !strings.Contains(markdown.String(), "````text\ncandidate ``` one\n````") {
		t.Fatalf("Markdown did not use a safe dynamic fence:\n%s", markdown.String())
	}
	if !strings.Contains(markdown.String(), "Run duration:") || !strings.Contains(markdown.String(), "Cost basis:") {
		t.Fatalf("Markdown has no cost and latency summary:\n%s", markdown.String())
	}
}

func TestReportRejectsIncompleteRound(t *testing.T) {
	report := newTestReport(t)
	round := testRound(t, 1, "candidate", true)
	round.Delta = nil
	if err := AppendRound(report, round); err == nil {
		t.Fatal("AppendRound() error = nil, want incomplete artifact error")
	}
}

func TestReportRejectsMismatchedOrDuplicateDeltaCases(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RoundReport)
	}{
		{
			name: "missing baseline case",
			mutate: func(round *RoundReport) {
				round.Delta.Cases = append(round.Delta.Cases, CaseDelta{
					EvalSetID: "validation",
					CaseID:    "case-2",
				})
			},
		},
		{
			name: "duplicate baseline case",
			mutate: func(round *RoundReport) {
				round.BaselineDelta.Cases = append(
					round.BaselineDelta.Cases,
					round.BaselineDelta.Cases[0],
				)
			},
		},
		{
			name: "empty case sets",
			mutate: func(round *RoundReport) {
				round.Delta.Cases = nil
				round.BaselineDelta.Cases = nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := newTestReport(t)
			round := testRound(t, 1, "candidate", true)
			baselineDelta := *round.BaselineDelta
			baselineDelta.Cases = append([]CaseDelta(nil), round.BaselineDelta.Cases...)
			round.BaselineDelta = &baselineDelta
			test.mutate(&round)
			if err := AppendRound(report, round); err == nil {
				t.Fatal("AppendRound() error = nil, want invalid delta cases error")
			}
		})
	}
}

func TestWriteMarkdownUsesAcceptedDeltaTransition(t *testing.T) {
	report := newTestReport(t)
	original := testEvaluation("validation", testCaseSpec{id: "case-1", score: 0, passed: false})
	accepted := testEvaluation("validation", testCaseSpec{id: "case-1", score: 1, passed: true})
	candidate := testEvaluation("validation", testCaseSpec{id: "case-1", score: 1, passed: true})
	acceptedDelta, err := Compare(accepted, candidate)
	if err != nil {
		t.Fatalf("Compare(accepted) error = %v", err)
	}
	baselineDelta, err := Compare(original, candidate)
	if err != nil {
		t.Fatalf("Compare(original) error = %v", err)
	}
	round := testRound(t, 1, "candidate", false)
	round.Delta = acceptedDelta
	round.BaselineDelta = baselineDelta
	if err := AppendRound(report, round); err != nil {
		t.Fatalf("AppendRound() error = %v", err)
	}
	if err := FinalizeReport(report, nil); err != nil {
		t.Fatalf("FinalizeReport() error = %v", err)
	}
	var markdown bytes.Buffer
	if err := WriteMarkdown(&markdown, report); err != nil {
		t.Fatalf("WriteMarkdown() error = %v", err)
	}
	want := "| case-1 | 0.0000 | 1.0000 | 1.0000 | +1.0000 | +0.0000 | unchanged |"
	if !strings.Contains(markdown.String(), want) {
		t.Fatalf("Markdown transition did not match accepted delta:\n%s", markdown.String())
	}
}

func TestWriteReportsRejectsGenerationCollision(t *testing.T) {
	report := newTestReport(t)
	if err := FinalizeReport(report, nil); err != nil {
		t.Fatalf("FinalizeReport() error = %v", err)
	}
	outputDir := t.TempDir()
	if _, err := WriteReports(outputDir, report); err != nil {
		t.Fatalf("first WriteReports() error = %v", err)
	}
	if _, err := WriteReports(outputDir, report); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second WriteReports() error = %v, want collision", err)
	}
}

func TestFailedReportDisablesWriteback(t *testing.T) {
	report := newTestReport(t)
	if err := AppendRound(report, testRound(t, 1, "candidate", true)); err != nil {
		t.Fatalf("AppendRound() error = %v", err)
	}
	if err := FinalizeReport(report, errors.New("interrupted")); err != nil {
		t.Fatalf("FinalizeReport() error = %v", err)
	}
	if report.ShouldWriteBack || report.SelectedCandidate != nil || report.Run.Status != "failed" {
		t.Fatalf("failed report retained writeback state: %+v", report)
	}
}

func newTestReport(t *testing.T) *Report {
	t.Helper()
	baseline := testEvaluation("validation", testCaseSpec{id: "case-1", score: 0, passed: false})
	report, err := NewReport(RunMetadata{
		ID: "test-run", Status: "running", Mode: "test", StartedAt: time.Unix(0, 0).UTC(),
	}, baseline, baseline, AttributionResult{})
	if err != nil {
		t.Fatalf("NewReport() error = %v", err)
	}
	return report
}

func testRound(t *testing.T, attempt int, prompt string, accepted bool) RoundReport {
	t.Helper()
	baseline := testEvaluation("validation", testCaseSpec{id: "case-1", score: 0, passed: false})
	candidate := testEvaluation("validation", testCaseSpec{id: "case-1", score: 1, passed: true})
	delta, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	reasons := []string{"candidate rejected for test"}
	if accepted {
		reasons = []string{"candidate accepted for test"}
	}
	return RoundReport{
		Attempt:         attempt,
		InputPrompt:     PromptRecord{SurfaceID: "candidate#instruction", Text: "input"},
		CandidatePrompt: PromptRecord{SurfaceID: "candidate#instruction", Text: prompt},
		Train:           candidate,
		Validation:      candidate,
		Delta:           delta,
		BaselineDelta:   delta,
		Gate:            GateDecision{Accepted: accepted, Reasons: reasons},
		Patches:         []PatchRecord{},
		Usage:           Usage{Measured: true},
	}
}
