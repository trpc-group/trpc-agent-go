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
