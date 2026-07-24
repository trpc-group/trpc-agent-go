//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderReport(t *testing.T) {
	report := optimizationReport{
		SchemaVersion: "1.0",
		RunID:         "seed-42",
		Baseline: promptEvaluation{
			Prompt:     "baseline",
			Validation: evaluationSummary{Score: 0.50},
		},
		Candidate: promptEvaluation{
			CandidateID: "balanced",
			Prompt:      "candidate",
			Validation:  evaluationSummary{Score: 0.90},
		},
		Delta: evaluationDelta{
			ScoreDelta:  0.40,
			NewlyPassed: 1,
			Cases: []caseDelta{{
				CaseID:         "case-1",
				BaselineScore:  0,
				CandidateScore: 1,
				ScoreDelta:     1,
				Class:          caseNewlyPassed,
			}},
		},
		GateDecision: gateDecision{
			Accepted: true,
			Reasons:  []string{"all acceptance checks passed"},
		},
		FailureAttribution: attributionSummary{
			Baseline: map[failureCategory]int{failureRoute: 1},
		},
		Rounds: []roundAudit{{
			Round:       1,
			CandidateID: "balanced",
			Decision:    gateDecision{Accepted: true},
		}},
	}

	jsonData, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, field := range []string{`"baseline"`, `"candidate"`, `"delta"`, `"gateDecision"`, `"failureAttribution"`, `"costLatency"`} {
		if !strings.Contains(string(jsonData), field) {
			t.Fatalf("JSON report does not contain %s", field)
		}
	}

	markdown, err := renderMarkdown(report)
	if err != nil {
		t.Fatalf("renderMarkdown returned error: %v", err)
	}
	for _, text := range []string{
		"# Prompt Optimization Report",
		"ACCEPT",
		"0.5000",
		"0.9000",
		"case-1",
		"newly_passed",
		"all acceptance checks passed",
	} {
		if !strings.Contains(markdown, text) {
			t.Fatalf("Markdown report does not contain %q:\n%s", text, markdown)
		}
	}
}

func TestRenderReportRecommendsBaselineWhenRejected(t *testing.T) {
	report := optimizationReport{
		SchemaVersion: "1.0",
		Baseline: promptEvaluation{
			Prompt: "safe baseline",
		},
		Candidate: promptEvaluation{
			Prompt: "rejected candidate",
		},
		GateDecision: gateDecision{Accepted: false},
	}
	markdown, err := renderMarkdown(report)
	if err != nil {
		t.Fatalf("renderMarkdown returned error: %v", err)
	}
	recommended := markdown[strings.LastIndex(markdown, "## Recommended Prompt"):]
	if !strings.Contains(recommended, "safe baseline") {
		t.Fatalf("recommended prompt does not contain baseline:\n%s", recommended)
	}
	if strings.Contains(recommended, "rejected candidate") {
		t.Fatalf("recommended prompt contains rejected candidate:\n%s", recommended)
	}
}
