//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReporter_GenerateReport_Empty(t *testing.T) {
	r := NewReporter()
	report := r.GenerateReport(nil)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.TotalCases != 0 {
		t.Errorf("expected 0 cases, got %d", report.TotalCases)
	}
}

func TestReporter_GenerateReport_WithDiffs(t *testing.T) {
	r := NewReporter()
	diffs := []DiffEntry{
		{
			CaseName: "case1", BackendA: "A", BackendB: "B",
			FieldPath: "events[0].content", Baseline: "hello", Actual: "world",
			DiffReason: "content differs",
		},
		{
			CaseName: "case1", BackendA: "A", BackendB: "B",
			FieldPath: "events[0].role", Baseline: "user", Actual: "assistant",
			AllowedDiff: true,
			DiffReason:  "allowed tolerance",
		},
	}
	caseResults := map[string][]DiffEntry{"case1": diffs}
	report := r.GenerateReport(caseResults)

	if report.TotalCases != 1 {
		t.Errorf("expected 1 case, got %d", report.TotalCases)
	}
	if report.TotalDiffs != 2 {
		t.Errorf("expected 2 diffs, got %d", report.TotalDiffs)
	}
	if report.AllowedDiffs != 1 {
		t.Errorf("expected 1 allowed diff, got %d", report.AllowedDiffs)
	}
	if report.UnallowedDiffs != 1 {
		t.Errorf("expected 1 unallowed diff, got %d", report.UnallowedDiffs)
	}
}

func TestReporter_ToJSON(t *testing.T) {
	r := NewReporter()
	report := &DiffReport{
		GeneratedAt: time.Now().UTC(),
		TotalCases:  2,
		TotalDiffs:  3,
		Diffs: []DiffEntry{
			{CaseName: "c1", BackendA: "A", BackendB: "B", FieldPath: "f1", Baseline: "x", Actual: "y"},
		},
		CaseResults: map[string]*CaseDiffSummary{
			"c1": {CaseName: "c1", DiffCount: 3},
		},
	}

	data, err := r.ToJSON(report)
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("JSON data is empty")
	}

	// Verify it's valid JSON.
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestReporter_ToText(t *testing.T) {
	r := NewReporter()
	report := &DiffReport{
		GeneratedAt: time.Now().UTC(),
		TotalCases:  1,
		TotalDiffs:  1,
		Summary:     "Test report summary.",
		Diffs: []DiffEntry{
			{CaseName: "c1", BackendA: "A", BackendB: "B", FieldPath: "f1", Baseline: "x", Actual: "y", DiffReason: "differs"},
		},
	}
	text := r.ToText(report)
	if len(text) == 0 {
		t.Error("text output is empty")
	}
	if text != report.Summary+"\n\n--- Detailed Differences ---\n\n"+"=== Case: c1 ===\n"+"  ⚠ [1] f1\n       A <-> B\n       Baseline: x\n       Actual:   y\n       Reason: differs\n\n" {
		t.Errorf("unexpected text report:\n%s", text)
	}
}

func TestReporter_NoDiffs(t *testing.T) {
	r := NewReporter()
	report := r.GenerateReport(map[string][]DiffEntry{})
	if report.TotalCases != 0 {
		t.Errorf("expected 0 cases, got %d", report.TotalCases)
	}
	if report.TotalDiffs != 0 {
		t.Errorf("expected 0 diffs, got %d", report.TotalDiffs)
	}
}
